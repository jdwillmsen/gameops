package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func xboxSessions(t *testing.T, c *clock) *Sessions {
	t.Helper()
	s := newSessions(t, c)
	s.XboxOn, s.XboxTTL = true, 24*time.Hour
	return s
}

func TestXboxSession_LastsADayAndSaysHowItWasHad(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := xboxSessions(t, c)
	rec := httptest.NewRecorder()
	if _, err := s.IssueXbox(rec, steve); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.MaxAge != 86400 || cookie.Name != "__Host-mcmap_session" {
		t.Errorf("cookie = %+v", cookie)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	var seen Session
	s.Require(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = SessionFromContext(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), req)
	if seen.Identity != steve || seen.Method != MethodXbox {
		t.Errorf("session = %+v", seen)
	}
	c.t = c.t.Add(24 * time.Hour)
	if _, ok := verify(s, cookie); ok {
		t.Error("an Xbox session outlived a day")
	}
}

func TestXboxSession_IsRefusedWhileSwitchedOffAndComesBackWithIt(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := xboxSessions(t, c)
	rec := httptest.NewRecorder()
	_, _ = s.IssueXbox(rec, steve)
	cookie := rec.Result().Cookies()[0]
	s.XboxOn = false
	if _, ok := verify(s, cookie); ok {
		t.Error("an Xbox session verifies with the switch off")
	}
	if _, err := s.IssueXbox(httptest.NewRecorder(), steve); err == nil {
		t.Error("one was issued with the switch off")
	}
	s.XboxOn = true
	if _, ok := verify(s, cookie); !ok {
		t.Error("it did not come back")
	}
}

func TestXboxSession_RevocationEndsIt(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := xboxSessions(t, c)
	rev, err := LoadRevocations(t.TempDir() + "/revoked.json")
	if err != nil {
		t.Fatal(err)
	}
	s.Revoked = rev
	rec := httptest.NewRecorder()
	_, _ = s.IssueXbox(rec, steve)
	cookie := rec.Result().Cookies()[0]
	if _, ok := verify(s, cookie); !ok {
		t.Fatal("not valid to begin with")
	}
	c.t = c.t.Add(time.Second)
	if err := rev.Revoke(steve.XUID, c.t); err != nil {
		t.Fatal(err)
	}
	if _, ok := verify(s, cookie); ok {
		t.Error("a revoked player's Xbox session still verifies")
	}
}

func TestXboxSession_ANonPlayerIsNotIssuedOne(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := xboxSessions(t, c)
	for _, id := range []Identity{
		{XUID: "", Gamertag: "x"},
		{XUID: "console", Gamertag: "x"},
		{XUID: steve.XUID, Gamertag: "service:e2e"},
		{XUID: steve.XUID, Gamertag: "bad\nname"},
	} {
		rec := httptest.NewRecorder()
		if _, err := s.IssueXbox(rec, id); err == nil || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%+v was issued a session", id)
		}
	}
}

func TestXboxSession_ACookieSayingXboxWithAKeyIsRefused(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := xboxSessions(t, c)
	rec := httptest.NewRecorder()
	s.issue(rec, s.Key, time.Hour, claims{Identity: steve, Method: MethodXbox, Key: "k"})
	if _, ok := verify(s, rec.Result().Cookies()[0]); ok {
		t.Error("an xbox session carrying a passkey key verifies")
	}
}
