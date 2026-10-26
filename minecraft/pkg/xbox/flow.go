package xbox

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StateTTL is long enough for a password and a second factor.
const StateTTL = 10 * time.Minute

// maxUsedStates bounds the memory of finished sign-ins. When it is full the
// oldest is forgotten: that state's cookie is already gone from an honest
// browser and its code is single use at Microsoft, so forgetting it opens
// nothing a flood of requests could use that it could not already.
const maxUsedStates = 4096

// Why a callback's cookie did not do.
var (
	ErrNoState  = errors.New("no usable sign-in state")
	ErrMismatch = errors.New("state does not match")
	ErrReplayed = errors.New("state was already used")
)

// StateKey derives the key that signs the state cookie from the service's
// session key. The label keeps a service's cookie from verifying anywhere
// else, and keeps that key from being the session key itself.
func StateKey(sessionKey []byte, label string) []byte {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(label + "\x00"))
	return mac.Sum(nil)
}

const keyBytes = 32

var encoding = base64.RawURLEncoding

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// A guessable state is worse than no service.
		panic(err)
	}
	return b
}

type flowState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
}

// Flow keeps no sign-in in memory: the state and the verifier travel in
// a signed cookie. What it remembers is only which states have finished,
// so that one callback cannot be played twice.
type Flow struct {
	cookie string
	key    []byte
	now    func() time.Time

	mu    sync.Mutex
	used  map[string]time.Time
	order list.List // of string, oldest first
}

// NewFlow signs the state in a cookie called cookie, with a key derived
// from sessionKey under label.
func NewFlow(cookie, label string, sessionKey []byte, now func() time.Time) (*Flow, error) {
	if len(sessionKey) < keyBytes {
		return nil, errors.New("xbox sign-in needs the session key")
	}
	return &Flow{cookie: cookie, key: StateKey(sessionKey, label), now: now, used: map[string]time.Time{}}, nil
}

func (f *Flow) sign(payload string) string {
	mac := hmac.New(sha256.New, f.key)
	mac.Write([]byte(payload))
	return encoding.EncodeToString(mac.Sum(nil))
}

// Begin sets the cookie and returns the state to send to Microsoft and the
// verifier's S256 challenge.
func (f *Flow) Begin(w http.ResponseWriter) (state, challenge string) {
	state = encoding.EncodeToString(randomBytes(32))
	verifier := encoding.EncodeToString(randomBytes(32))
	raw, _ := json.Marshal(flowState{State: state, Verifier: verifier, Expires: f.now().Add(StateTTL).Unix()})
	payload := encoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: f.cookie, Value: payload + "." + f.sign(payload), Path: "/",
		MaxAge: int(StateTTL.Seconds()), HttpOnly: true, Secure: true,
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
func (f *Flow) Finish(w http.ResponseWriter, r *http.Request, state string) (verifier string, err error) {
	http.SetCookie(w, &http.Cookie{Name: f.cookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	c, cerr := r.Cookie(f.cookie)
	if cerr != nil {
		return "", ErrNoState
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(f.sign(payload))) {
		return "", ErrNoState
	}
	raw, derr := encoding.DecodeString(payload)
	var st flowState
	if derr != nil || json.Unmarshal(raw, &st) != nil || st.State == "" || st.Verifier == "" {
		return "", ErrNoState
	}
	now := f.now()
	if !now.Before(time.Unix(st.Expires, 0)) || time.Unix(st.Expires, 0).Sub(now) > StateTTL+time.Second {
		return "", ErrNoState
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(st.State)) != 1 {
		return "", ErrMismatch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgetExpired(now)
	if _, seen := f.used[st.State]; seen {
		return "", ErrReplayed
	}
	for f.order.Len() >= maxUsedStates {
		f.drop(f.order.Front())
	}
	f.used[st.State] = time.Unix(st.Expires, 0)
	f.order.PushBack(st.State)
	return st.Verifier, nil
}

func (f *Flow) forgetExpired(now time.Time) {
	for e := f.order.Front(); e != nil; e = f.order.Front() {
		if now.Before(f.used[e.Value.(string)]) {
			return
		}
		f.drop(e)
	}
}

func (f *Flow) drop(e *list.Element) {
	delete(f.used, e.Value.(string))
	f.order.Remove(e)
}
