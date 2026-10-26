package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	SessionCookie = "__Host-crew_session"
	sessionTTL    = 12 * time.Hour
	keyBytes      = 32
	clockSkew     = time.Minute
)

var b64 = base64.RawURLEncoding

type session struct {
	Op  string `json:"op"`
	Key string `json:"key"`
	// Via is how the operator signed in; empty is a passkey.
	Via string `json:"via,omitempty"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// loadSessionKey reads the signing key, making it on first start. A key of
// the wrong length is an error and is never replaced: replacing it would
// quietly sign every operator out.
func loadSessionKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := randomBytes(keyBytes)
		return key, writeAtomic(path, key)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) != keyBytes {
		return nil, errors.New("session.key is not 32 bytes")
	}
	return raw, nil
}

func (a *Auth) mac(body string) []byte {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}

func (a *Auth) issue(w http.ResponseWriter, op, key string) { a.issueVia(w, op, key, "") }

func (a *Auth) issueVia(w http.ResponseWriter, op, key, via string) {
	now := a.now()
	raw, _ := json.Marshal(session{Op: op, Key: key, Via: via, Iat: now.Unix(), Exp: now.Add(sessionTTL).Unix()})
	body := b64.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: body + "." + b64.EncodeToString(a.mac(body)), Path: "/",
		MaxAge: int(sessionTTL.Seconds()), Expires: now.Add(sessionTTL),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// verify checks the signature and the lifetime only; whether the operator
// and passkey still exist is Operator's job.
func (a *Auth) verify(r *http.Request) (session, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return session{}, false
	}
	body, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return session{}, false
	}
	got, err := b64.DecodeString(sig)
	if err != nil || !hmac.Equal(got, a.mac(body)) {
		return session{}, false
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return session{}, false
	}
	var s session
	if json.Unmarshal(raw, &s) != nil {
		return session{}, false
	}
	now := a.now()
	if s.Op == "" || s.Key == "" || !now.Before(time.Unix(s.Exp, 0)) || time.Unix(s.Iat, 0).After(now.Add(clockSkew)) ||
		time.Unix(s.Exp, 0).Sub(time.Unix(s.Iat, 0)) > sessionTTL {
		return session{}, false
	}
	return s, true
}
