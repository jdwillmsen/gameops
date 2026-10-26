package auth

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func signedIn(t *testing.T, e *env) *client {
	t.Helper()
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	return c
}

func TestSessionRenewsPastHalfLife(t *testing.T) {
	e := newEnv(t)
	c := signedIn(t, e)
	first := sessionOf(t, c)

	e.clk.advance(sessionTTL/2 - time.Hour)
	mustOK(t, c.me(), "me before half-life")
	if sessionOf(t, c) != first {
		t.Fatal("renewed before half its life was gone")
	}

	e.clk.advance(2 * time.Hour)
	mustOK(t, c.me(), "me after half-life")
	if sessionOf(t, c) == first {
		t.Fatal("not renewed past half-life")
	}
	// The renewed cookie carries the session a full lifetime again.
	e.clk.advance(sessionTTL - time.Hour)
	mustOK(t, c.me(), "inside the renewed lifetime")
}

func TestSessionNeverOutlivesItsMonth(t *testing.T) {
	e := newEnv(t)
	c := signedIn(t, e)
	for i := 0; i < 5; i++ {
		e.clk.advance(sessionTTL/2 + time.Minute)
		if c.me().Code != http.StatusOK {
			break
		}
	}
	e.clk.advance(sessionMax)
	if c.me().Code != http.StatusUnauthorized {
		t.Fatal("a renewed session outlived the absolute limit")
	}
}

func TestIdleSessionLapses(t *testing.T) {
	e := newEnv(t)
	c := signedIn(t, e)
	e.clk.advance(sessionTTL + time.Minute)
	if c.me().Code != http.StatusUnauthorized {
		t.Fatal("idle session survived its lifetime")
	}
}

func TestLogoutEndsACopiedCookie(t *testing.T) {
	e := newEnv(t)
	c := signedIn(t, e)
	stolen := sessionOf(t, c)
	mustOK(t, c.post("/auth/logout", map[string]string{}), "logout")

	replay := func(e *env) int {
		cc := e.client()
		cc.cookies["__Host-crew_session"] = &http.Cookie{Name: "__Host-crew_session", Value: stolen}
		return cc.me().Code
	}
	if replay(e) != http.StatusUnauthorized {
		t.Fatal("a signed-out cookie still works")
	}
	// The sign-out is on disk, so a restart does not revive the cookie.
	e.build(opJSON)
	if replay(e) != http.StatusUnauthorized {
		t.Fatal("a signed-out cookie works again after restart")
	}
}

func TestLogoutEndsCookieBeforeRenewal(t *testing.T) {
	e := newEnv(t)
	c := signedIn(t, e)
	old := sessionOf(t, c)
	e.clk.advance(sessionTTL/2 + time.Hour)
	mustOK(t, c.me(), "renew")
	mustOK(t, c.post("/auth/logout", map[string]string{}), "logout")
	cc := e.client()
	cc.cookies["__Host-crew_session"] = &http.Cookie{Name: "__Host-crew_session", Value: old}
	if cc.me().Code != http.StatusUnauthorized {
		t.Fatal("the cookie from before a renewal survives sign-out")
	}
}

func TestMeReportsExpiry(t *testing.T) {
	e := newEnv(t)
	c := signedIn(t, e)
	rec := c.me()
	var got struct {
		Expires int64 `json:"expires"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Expires != e.clk.now().Add(sessionTTL).Unix() {
		t.Fatalf("expires = %d, err %v", got.Expires, err)
	}
}
