package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const serviceSecret = "a-made-up-secret-for-tests-0123456789"

func withService(t *testing.T, c *clock) *Sessions {
	t.Helper()
	s := newSessions(t, c)
	s.ServiceKey = ServiceKey(s.Key, serviceSecret)
	s.ServiceTTL = 10 * time.Minute
	return s
}

func issueService(t *testing.T, s *Sessions) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.IssueService(rec)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("IssueService set %d cookies", len(cookies))
	}
	return cookies[0]
}

// through sends one request through a gate and reports the status and what
// the handler behind it saw.
func through(gate func(http.Handler) http.Handler, method string, c *http.Cookie) (status int, id Identity, service bool) {
	req := httptest.NewRequest(method, "/", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ = FromContext(r.Context())
		service = IsService(r.Context())
	})).ServeHTTP(rec, req)
	return rec.Code, id, service
}

// forge signs claims with a key, as someone holding that key could.
func forge(key []byte, c claims) *http.Cookie {
	raw, _ := json.Marshal(c)
	payload := encoding.EncodeToString(raw)
	return &http.Cookie{Name: sessionCookie, Value: payload + "." + sign(key, payload)}
}

func TestService_ASessionReadsWhatIsShownAndNothingElse(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := withService(t, c)
	var seen []bool
	s.ServiceSeen = func(allowed bool) { seen = append(seen, allowed) }
	cookie := issueService(t, s)

	status, id, service := through(s.RequireViewer, "GET", cookie)
	if status != http.StatusOK || !service || id.XUID != "" || id.Gamertag != ServiceName {
		t.Errorf("viewer gate = %d as %+v service=%v, want 200 as %q with no XUID", status, id, service, ServiceName)
	}
	if status, _, _ := through(s.Require, "GET", cookie); status != http.StatusForbidden {
		t.Errorf("player gate = %d, want 403", status)
	}
	// Whatever is put behind the viewer gate, a service session only reads.
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if status, _, _ := through(s.RequireViewer, method, cookie); status != http.StatusForbidden {
			t.Errorf("%s through the viewer gate = %d, want 403", method, status)
		}
	}
	if want := []bool{true, false, false, false, false, false}; !slices.Equal(seen, want) {
		t.Errorf("ServiceSeen was told %v, want %v", seen, want)
	}
	// It is nobody's login.
	if id, ok := verify(s, cookie); ok {
		t.Errorf("Verify took a service session for the player %+v", id)
	}
}

func TestService_APlayerIsUnaffected(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := withService(t, c)
	s.ServiceSeen = func(bool) { t.Error("a player's request was counted as a service session's") }
	steve := Identity{XUID: "2535400000000001", Gamertag: "Steve"}
	cookie := issue(t, s, steve)
	for name, gate := range map[string]func(http.Handler) http.Handler{"player": s.Require, "viewer": s.RequireViewer} {
		for _, method := range []string{"GET", "POST"} {
			status, id, service := through(gate, method, cookie)
			if status != http.StatusOK || service || id != steve {
				t.Errorf("%s through the %s gate = %d as %+v service=%v", method, name, status, id, service)
			}
		}
	}
}

func TestService_TheCookieIsAPlayersInAllButWhatItSaysAndHowLongItLasts(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := withService(t, c)
	player, service := issue(t, s, Identity{XUID: "1", Gamertag: "Steve"}), issueService(t, s)
	if service.Name != player.Name || service.Path != player.Path || service.Domain != player.Domain ||
		service.Secure != player.Secure || service.HttpOnly != player.HttpOnly || service.SameSite != player.SameSite {
		t.Errorf("service cookie %+v differs from a player's %+v", service, player)
	}
	if !service.Secure || !service.HttpOnly || service.SameSite != http.SameSiteLaxMode || service.Path != "/" || service.Domain != "" || !strings.HasPrefix(service.Name, "__Host-") {
		t.Errorf("service cookie %+v is weaker than it should be", service)
	}
	if service.MaxAge != 600 {
		t.Errorf("MaxAge = %d, want the service TTL of 600", service.MaxAge)
	}
}

func TestService_ASessionEndsAndCannotBeStretched(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := withService(t, c)
	cookie := issueService(t, s)
	c.t = c.t.Add(10*time.Minute - time.Second)
	if status, _, _ := through(s.RequireViewer, "GET", cookie); status != http.StatusOK {
		t.Errorf("a second before the end = %d, want 200", status)
	}
	c.t = c.t.Add(time.Second)
	if status, _, _ := through(s.RequireViewer, "GET", cookie); status != http.StatusUnauthorized {
		t.Errorf("at the end = %d, want 401", status)
	}
	// Signed with the right key and still refused: no service session
	// lasts longer than the longest that can be configured.
	now := c.t
	long := forge(s.ServiceKey, claims{Identity: Identity{Gamertag: ServiceName}, Service: true, IssuedMilli: now.UnixMilli(), Expires: now.Add(MaxServiceTTL + time.Minute).Unix()})
	if status, _, _ := through(s.RequireViewer, "GET", long); status != http.StatusUnauthorized {
		t.Errorf("a session longer than the limit = %d, want 401", status)
	}
}

func TestService_NoSessionWithoutTheKeyItWasIssuedUnder(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := withService(t, c)
	cookie := issueService(t, s)

	off := newSessions(t, c)
	if status, _, _ := through(off.RequireViewer, "GET", cookie); status != http.StatusUnauthorized {
		t.Errorf("with service sessions off = %d, want 401", status)
	}
	rotated := newSessions(t, c)
	rotated.ServiceKey = ServiceKey(rotated.Key, serviceSecret+"x")
	if status, _, _ := through(rotated.RequireViewer, "GET", cookie); status != http.StatusUnauthorized {
		t.Errorf("after the secret changed = %d, want 401", status)
	}
	// A key too short to be one signs nothing.
	short := newSessions(t, c)
	short.ServiceKey = []byte("short")
	forged := forge(short.ServiceKey, claims{Identity: Identity{Gamertag: ServiceName}, Service: true, IssuedMilli: c.t.UnixMilli(), Expires: c.t.Add(time.Minute).Unix()})
	if status, _, _ := through(short.RequireViewer, "GET", forged); status != http.StatusUnauthorized {
		t.Errorf("under a key too short to be one = %d, want 401", status)
	}
}

// The two kinds of session cannot be passed off as each other, whichever
// key someone holds.
func TestService_NeitherKindOfSessionCanClaimToBeTheOther(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := withService(t, c)
	now, soon := c.t.UnixMilli(), c.t.Add(5*time.Minute).Unix()
	steve := Identity{XUID: "2535400000000001", Gamertag: "Steve"}
	for name, cookie := range map[string]*http.Cookie{
		"the service key naming a player":                  forge(s.ServiceKey, claims{Identity: steve, Service: true, IssuedMilli: now, Expires: soon}),
		"the service key naming a player, not marked":      forge(s.ServiceKey, claims{Identity: steve, IssuedMilli: now, Expires: soon}),
		"the service key under another name":               forge(s.ServiceKey, claims{Identity: Identity{Gamertag: "service:admin"}, Service: true, IssuedMilli: now, Expires: soon}),
		"the service key with an XUID beside the name":     forge(s.ServiceKey, claims{Identity: Identity{XUID: "1", Gamertag: ServiceName}, Service: true, IssuedMilli: now, Expires: soon}),
		"the player key marked as a service session":       forge(s.Key, claims{Identity: Identity{Gamertag: ServiceName}, Service: true, IssuedMilli: now, Expires: soon}),
		"the player key marked as one and naming a player": forge(s.Key, claims{Identity: steve, Service: true, IssuedMilli: now, Expires: soon}),
		"the player key with no XUID":                      forge(s.Key, claims{Identity: Identity{Gamertag: ServiceName}, IssuedMilli: now, Expires: soon}),
	} {
		for gate, require := range map[string]func(http.Handler) http.Handler{"player": s.Require, "viewer": s.RequireViewer} {
			if status, id, _ := through(require, "GET", cookie); status != http.StatusUnauthorized {
				t.Errorf("%s, through the %s gate = %d as %+v, want 401", name, gate, status, id)
			}
		}
	}
}

func TestService_NoPlayerCanBeClaimedUnderTheServiceName(t *testing.T) {
	codes := &Codes{TTL: time.Minute, Max: 4, Now: time.Now}
	for _, name := range []string{ServiceName, "Service:E2E", "service:anything"} {
		code, _ := codes.Start("")
		if err := codes.Claim(code, Identity{XUID: "2535400000000001", Gamertag: name}); err == nil {
			t.Errorf("a claim as %q was accepted", name)
		}
	}
}

func TestExchange_OnlyTheSecretEarnsASession(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	e := NewExchange(serviceSecret, c.now)
	for _, wrong := range []string{"", "x", serviceSecret[:len(serviceSecret)-1], serviceSecret + "x", strings.ToUpper(serviceSecret)} {
		if got := e.Attempt(wrong); got != ExchangeDenied {
			t.Errorf("Attempt(a wrong secret of %d characters) = %s, want denied", len(wrong), got)
		}
		// Spaced out, so that this is about the comparison alone.
		c.t = c.t.Add(time.Hour)
		if got := e.Attempt(serviceSecret); got != ExchangeOK {
			t.Fatalf("Attempt(the secret) = %s, want ok", got)
		}
	}
}

func TestExchange_WrongSecretsLockItForEverybody(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	e := NewExchange(serviceSecret, c.now)
	for i := range exchangeMaxFailures {
		if got := e.Attempt("wrong"); got != ExchangeDenied {
			t.Fatalf("wrong secret %d = %s, want denied", i+1, got)
		}
	}
	// Locked: not even the right secret is looked at.
	if got := e.Attempt(serviceSecret); got != ExchangeLocked {
		t.Errorf("the secret while locked = %s, want locked", got)
	}
	if wait := e.RetryAfter(); wait != exchangeLockout {
		t.Errorf("RetryAfter = %s, want %s", wait, exchangeLockout)
	}
	c.t = c.t.Add(exchangeLockout - time.Second)
	if got := e.Attempt(serviceSecret); got != ExchangeLocked {
		t.Errorf("a second before the lock lifts = %s, want locked", got)
	}
	c.t = c.t.Add(time.Second)
	// One more wrong one is enough to lock it again.
	if got := e.Attempt("wrong"); got != ExchangeDenied {
		t.Errorf("a wrong secret after the lock = %s, want denied", got)
	}
	if got := e.Attempt(serviceSecret); got != ExchangeLocked {
		t.Errorf("the secret after that = %s, want locked again", got)
	}
	c.t = c.t.Add(exchangeLockout)
	if got := e.Attempt(serviceSecret); got != ExchangeOK {
		t.Errorf("the secret once the second lock lifted = %s, want ok", got)
	}
	// A success forgives what came before it.
	for i := range exchangeMaxFailures - 1 {
		c.t = c.t.Add(exchangeRefill)
		if got := e.Attempt("wrong"); got != ExchangeDenied {
			t.Fatalf("wrong secret %d after a success = %s, want denied", i+1, got)
		}
	}
	c.t = c.t.Add(exchangeRefill)
	if got := e.Attempt(serviceSecret); got != ExchangeOK {
		t.Errorf("the secret after four wrong ones = %s, want ok", got)
	}
}

func TestExchange_EvenTheSecretIsRateLimited(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	e := NewExchange(serviceSecret, c.now)
	for i := range exchangeBurst {
		if got := e.Attempt(serviceSecret); got != ExchangeOK {
			t.Fatalf("exchange %d = %s, want ok", i+1, got)
		}
	}
	if got := e.Attempt(serviceSecret); got != ExchangeLimited {
		t.Errorf("one past the burst = %s, want limited", got)
	}
	c.t = c.t.Add(exchangeRefill)
	if got := e.Attempt(serviceSecret); got != ExchangeOK {
		t.Errorf("after one refill = %s, want ok", got)
	}
	if got := e.Attempt(serviceSecret); got != ExchangeLimited {
		t.Errorf("straight after = %s, want limited", got)
	}
}
