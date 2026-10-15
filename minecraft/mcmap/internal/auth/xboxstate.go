package auth

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// XboxStateCookie holds an Xbox sign-in's state and PKCE verifier between
// the browser leaving for Microsoft and coming back.
const XboxStateCookie = cookiePrefix + "mcmap_xbox"

// XboxStateTTL is long enough for a password and a second factor.
const XboxStateTTL = 10 * time.Minute

// maxUsedStates bounds the memory of finished sign-ins. When it is full the
// oldest is forgotten: that state's cookie is already gone from an honest
// browser and its code is single use at Microsoft, so forgetting it opens
// nothing a flood of requests could use that it could not already.
const maxUsedStates = 4096

// Why a callback's cookie did not do.
var (
	ErrXboxNoState  = errors.New("no usable sign-in state")
	ErrXboxMismatch = errors.New("state does not match")
	ErrXboxReplayed = errors.New("state was already used")
)

func XboxStateKey(sessionKey []byte) []byte {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte("mcmap xbox sign-in state v1\x00"))
	return mac.Sum(nil)
}

type xboxState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
}

// XboxFlow keeps no sign-in in memory: the state and the verifier travel in
// a signed cookie. What it remembers is only which states have finished,
// so that one callback cannot be played twice.
type XboxFlow struct {
	key []byte
	now func() time.Time

	mu    sync.Mutex
	used  map[string]time.Time
	order list.List // of string, oldest first
}

func NewXboxFlow(sessionKey []byte, now func() time.Time) (*XboxFlow, error) {
	if len(sessionKey) < keyBytes {
		return nil, errors.New("xbox sign-in needs the session key")
	}
	return &XboxFlow{key: XboxStateKey(sessionKey), now: now, used: map[string]time.Time{}}, nil
}

func (f *XboxFlow) sign(payload string) string {
	mac := hmac.New(sha256.New, f.key)
	mac.Write([]byte(payload))
	return encoding.EncodeToString(mac.Sum(nil))
}

// Begin sets the cookie and returns the state to send to Microsoft and the
// verifier's S256 challenge.
func (f *XboxFlow) Begin(w http.ResponseWriter) (state, challenge string) {
	state = encoding.EncodeToString(randomBytes(32))
	verifier := encoding.EncodeToString(randomBytes(32))
	raw, _ := json.Marshal(xboxState{State: state, Verifier: verifier, Expires: f.now().Add(XboxStateTTL).Unix()})
	payload := encoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: XboxStateCookie, Value: payload + "." + f.sign(payload), Path: "/",
		MaxAge: int(XboxStateTTL.Seconds()), HttpOnly: true, Secure: true,
		// Lax and not Strict: the browser returns from Microsoft by a
		// top-level navigation that Microsoft started, which Strict
		// cookies are not sent on. Nothing is changed by the request
		// until Microsoft's code, this cookie and the query agree.
		SameSite: http.SameSiteLaxMode,
	})
	return state, s256Challenge(verifier)
}

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return encoding.EncodeToString(sum[:])
}

// Finish checks the callback's state against the cookie, clears the cookie
// whatever the answer, marks the state used and returns the verifier. A
// state can be finished once.
func (f *XboxFlow) Finish(w http.ResponseWriter, r *http.Request, state string) (verifier string, err error) {
	http.SetCookie(w, &http.Cookie{Name: XboxStateCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	c, cerr := r.Cookie(XboxStateCookie)
	if cerr != nil {
		return "", ErrXboxNoState
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(f.sign(payload))) {
		return "", ErrXboxNoState
	}
	raw, derr := encoding.DecodeString(payload)
	var st xboxState
	if derr != nil || json.Unmarshal(raw, &st) != nil || st.State == "" || st.Verifier == "" {
		return "", ErrXboxNoState
	}
	now := f.now()
	if !now.Before(time.Unix(st.Expires, 0)) || time.Unix(st.Expires, 0).Sub(now) > XboxStateTTL+time.Second {
		return "", ErrXboxNoState
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(st.State)) != 1 {
		return "", ErrXboxMismatch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgetExpired(now)
	if _, seen := f.used[st.State]; seen {
		return "", ErrXboxReplayed
	}
	for f.order.Len() >= maxUsedStates {
		f.drop(f.order.Front())
	}
	f.used[st.State] = time.Unix(st.Expires, 0)
	f.order.PushBack(st.State)
	return st.Verifier, nil
}

func (f *XboxFlow) forgetExpired(now time.Time) {
	for e := f.order.Front(); e != nil; e = f.order.Front() {
		if now.Before(f.used[e.Value.(string)]) {
			return
		}
		f.drop(e)
	}
}

func (f *XboxFlow) drop(e *list.Element) {
	delete(f.used, e.Value.(string))
	f.order.Remove(e)
}
