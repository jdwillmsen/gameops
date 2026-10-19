package auth

import (
	"errors"
	"net/http"
	"time"
)

// cookieJar collects the Set-Cookie of a response that is never sent.
type cookieJar struct{ h http.Header }

func (j cookieJar) Header() http.Header       { return j.h }
func (cookieJar) Write(b []byte) (int, error) { return len(b), nil }
func (cookieJar) WriteHeader(int)             {}

// SessionForTest signs op in without a WebAuthn ceremony: it stores a
// throwaway passkey and returns the session cookie that names it. Tests of
// other packages use it to drive routes that need a real session.
func (a *Auth) SessionForTest(op string) (*http.Cookie, error) {
	if !a.Enabled() {
		return nil, errors.New("auth is disabled")
	}
	id := randomBytes(16)
	if err := a.keys.Add(Passkey{ID: id, PublicKey: randomBytes(32), UserHandle: a.keys.UserHandle(op), Operator: op, Created: time.Now()}); err != nil {
		return nil, err
	}
	j := cookieJar{h: http.Header{}}
	a.issue(j, op, KeyOf(id))
	resp := http.Response{Header: j.h}
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			return c, nil
		}
	}
	return nil, errors.New("no session cookie issued")
}
