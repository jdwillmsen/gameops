package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type activeKeys map[string]string // key to XUID

func (a activeKeys) Active(key, xuid string) bool { return a[key] == xuid }

func passkeyCookie(t *testing.T, s *Sessions, key string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.IssuePasskey(rec, steve, key)
	return rec.Result().Cookies()[0]
}

func TestPasskeySession_LastsTwelveHoursAndNamesItsKey(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	s.Passkeys, s.PasskeyTTL = activeKeys{"key-1": steve.XUID}, 12*time.Hour
	cookie := passkeyCookie(t, s, "key-1")
	if cookie.MaxAge != int((12 * time.Hour).Seconds()) {
		t.Errorf("MaxAge = %d", cookie.MaxAge)
	}
	if got, ok := verify(s, cookie); !ok || got != steve {
		t.Fatalf("Verify = %+v, %v", got, ok)
	}
	c.t = c.t.Add(12 * time.Hour)
	if _, ok := verify(s, cookie); ok {
		t.Error("a passkey session outlived twelve hours")
	}
}

// Removing a passkey, or switching passkeys off, ends its sessions on the
// next request and not when they would have run out.
func TestPasskeySession_EndsWithItsKeyOrTheSwitch(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	keys := activeKeys{"key-1": steve.XUID}
	s.Passkeys, s.PasskeyTTL = keys, 12*time.Hour
	cookie := passkeyCookie(t, s, "key-1")

	delete(keys, "key-1")
	if _, ok := verify(s, cookie); ok {
		t.Error("a removed passkey's session still verifies")
	}
	keys["key-1"] = steve.XUID
	s.Passkeys = nil
	if _, ok := verify(s, cookie); ok {
		t.Error("a passkey session verifies with passkeys switched off")
	}
	// A key that names another player is no key of this one's.
	s.Passkeys = activeKeys{"key-1": "2535412345678902"}
	if _, ok := verify(s, cookie); ok {
		t.Error("a passkey session verifies against another player's key")
	}
}

func TestPasskeySession_CookiesThatCannotBeOursAreRefused(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	s.Passkeys, s.PasskeyTTL = activeKeys{"key-1": steve.XUID}, 12*time.Hour
	now := c.t
	for name, cl := range map[string]claims{
		"unknown method":       {Identity: steve, IssuedMilli: now.UnixMilli(), Expires: now.Add(time.Hour).Unix(), Method: "dex"},
		"passkey with no key":  {Identity: steve, IssuedMilli: now.UnixMilli(), Expires: now.Add(time.Hour).Unix(), Method: MethodPasskey},
		"game code with a key": {Identity: steve, IssuedMilli: now.UnixMilli(), Expires: now.Add(time.Hour).Unix(), Key: "key-1"},
	} {
		if _, ok := verify(s, forge(s.Key, cl)); ok {
			t.Errorf("%s: verified", name)
		}
	}
	// A game-code session made before any of this verifies as it did.
	old := issue(t, s, steve)
	if _, ok := verify(s, old); !ok {
		t.Error("a game-code session no longer verifies")
	}
}

func TestSessionFromContext_SaysHowAndWhen(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	s := newSessions(t, c)
	s.Passkeys, s.PasskeyTTL = activeKeys{"key-1": steve.XUID}, 12*time.Hour
	var got Session
	h := s.Require(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got, _ = SessionFromContext(r.Context()) }))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(passkeyCookie(t, s, "key-1"))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.Identity != steve || got.Method != MethodPasskey || got.IssuedMilli != c.t.UnixMilli() {
		t.Errorf("SessionFromContext = %+v", got)
	}
}
