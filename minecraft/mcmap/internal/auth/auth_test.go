package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var steve = Identity{XUID: "2535412345678901", Gamertag: "Steve Builds"}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newSessions(t *testing.T, c *clock) *Sessions {
	t.Helper()
	return &Sessions{Key: []byte(strings.Repeat("k", 32)), TTL: 30 * 24 * time.Hour, Now: c.now}
}

func issue(t *testing.T, s *Sessions, id Identity) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Issue(rec, id)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Issue set %d cookies", len(cookies))
	}
	return cookies[0]
}

func verify(s *Sessions, c *http.Cookie) (Identity, bool) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	return s.Verify(req)
}

func TestSession_RoundTripsAndIsLockedDown(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	cookie := issue(t, s, steve)

	if got, ok := verify(s, cookie); !ok || got != steve {
		t.Fatalf("Verify = %+v, %v", got, ok)
	}
	// The cookie is the whole login; script on the page must not be able to
	// read it, and it must never travel over plain HTTP.
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Errorf("cookie flags = %+v", cookie)
	}
	// The prefix makes the browser refuse a cookie of this name set by any
	// other host, so a sibling site on the same domain cannot plant a login.
	if !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Errorf("cookie name %q lacks the __Host- prefix", cookie.Name)
	}
	if cookie.MaxAge != int((30 * 24 * time.Hour).Seconds()) {
		t.Errorf("MaxAge = %d", cookie.MaxAge)
	}
}

func TestSession_RejectsAnythingItDidNotIssue(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	good := issue(t, s, steve)

	payload, sig, _ := strings.Cut(good.Value, ".")
	other := &Sessions{Key: []byte(strings.Repeat("x", 32)), TTL: time.Hour, Now: c.now}
	forged := issue(t, other, steve)

	for name, value := range map[string]string{
		"empty":                   "",
		"no signature":            payload,
		"signature only":          "." + sig,
		"another key's signature": forged.Value,
		"payload swapped":         strings.Split(forged.Value, ".")[0] + "." + sig,
		"one character changed":   flip(good.Value),
		"garbage":                 "not-a-cookie",
		"extra segment":           good.Value + ".x",
	} {
		if id, ok := verify(s, &http.Cookie{Name: good.Name, Value: value}); ok {
			t.Errorf("%s: accepted as %+v", name, id)
		}
	}
	if _, ok := s.Verify(httptest.NewRequest("GET", "/", nil)); ok {
		t.Error("a request with no cookie was accepted")
	}
}

// A key that never got loaded must not sign logins anyone could reproduce.
func TestSession_WithoutAFullKeyAcceptsNothing(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	for name, key := range map[string][]byte{"no key": nil, "short key": []byte("short")} {
		s := &Sessions{Key: key, TTL: time.Hour, Now: c.now}
		if id, ok := verify(s, issue(t, s, steve)); ok {
			t.Errorf("%s: accepted its own cookie as %+v", name, id)
		}
	}
}

func flip(s string) string {
	b := []byte(s)
	if b[3] == 'A' {
		b[3] = 'B'
	} else {
		b[3] = 'A'
	}
	return string(b)
}

func TestSession_ExpiresOnTheServersClockNotTheCookies(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	cookie := issue(t, s, steve)

	c.t = c.t.Add(30*24*time.Hour - time.Second)
	if _, ok := verify(s, cookie); !ok {
		t.Error("rejected a second before expiry")
	}
	c.t = c.t.Add(2 * time.Second)
	if _, ok := verify(s, cookie); ok {
		t.Error("a browser that kept the cookie past its lifetime was still let in")
	}
}

func TestRequire_GatesTheHandler(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	var seen Identity
	h := s.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/tiles/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d, want 401", rec.Code)
	}

	req := httptest.NewRequest("GET", "/tiles/x", nil)
	req.AddCookie(issue(t, s, steve))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || seen != steve {
		t.Fatalf("with a session = %d, identity %+v", rec.Code, seen)
	}
}

func TestLoadKey_CreatesOnceThenReuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", "session.key")
	first, err := LoadKey(path)
	if err != nil || len(first) != 32 {
		t.Fatalf("LoadKey = %d bytes, %v", len(first), err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	// Written whole or not at all: a crash part-way must not leave a short
	// key that stops the service starting until someone deletes it.
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("%d files beside the key, want only the key", len(entries)-1)
	}
	second, err := LoadKey(path)
	if err != nil || string(second) != string(first) {
		t.Fatal("a restart changed the key, which would log everyone out")
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(path); err == nil {
		t.Error("a truncated key file was accepted")
	}
}

// A session cannot be taken back one cookie at a time, since none is stored.
// What can be done is to stop honouring everything a player was issued up to
// now: that is what a player who typed someone else's code needs.
func TestSession_RevokingAPlayerEndsTheirSessionsAndOnlyTheirs(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	path := filepath.Join(t.TempDir(), "auth", "revoked.json")
	var err error
	if s.Revoked, err = LoadRevocations(path); err != nil {
		t.Fatal(err)
	}
	alex := Identity{XUID: "2535499999999999", Gamertag: "Alex"}
	stolen, other := issue(t, s, steve), issue(t, s, alex)

	c.t = c.t.Add(time.Minute)
	if err := s.Revoked.Revoke(steve.XUID, c.now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := verify(s, stolen); ok {
		t.Error("a session issued before the revocation is still honoured")
	}
	if _, ok := verify(s, other); !ok {
		t.Error("revoking one player logged another out")
	}

	c.t = c.t.Add(time.Second)
	if _, ok := verify(s, issue(t, s, steve)); !ok {
		t.Error("the player cannot log in again after revoking")
	}

	// A restart must not bring the revoked sessions back.
	if s.Revoked, err = LoadRevocations(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := verify(s, stolen); ok {
		t.Error("a restart forgot the revocation")
	}
}

func TestLoadRevocations_RefusesAFileItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "revoked.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Starting with an empty list would quietly restore every revoked login.
	if _, err := LoadRevocations(path); err == nil {
		t.Error("a corrupt revocation file was read as no revocations")
	}
}
