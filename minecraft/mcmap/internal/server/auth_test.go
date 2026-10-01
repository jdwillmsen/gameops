package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

const internalToken = "internal-token-0123456789"

func withLogin(t *testing.T) *Server {
	t.Helper()
	s, _ := fixture(t)
	now := time.Now
	s.Sessions = &auth.Sessions{Key: []byte(strings.Repeat("k", 32)), TTL: time.Hour, Now: now}
	s.Codes = &auth.Codes{TTL: 10 * time.Minute, Max: 2, Now: now}
	revoked, err := auth.LoadRevocations(filepath.Join(t.TempDir(), "revoked.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Sessions.Revoked = revoked
	s.InternalToken = internalToken
	return s
}

func do(h http.Handler, method, path, body string, cookies []*http.Cookie, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLogin_MapDataNeedsASessionButThePageDoesNot(t *testing.T) {
	s := withLogin(t)
	h := s.Handler()
	for _, path := range []string{"/api/map", "/api/me", "/tiles/overworld/0/37/-4.webp"} {
		if rec := do(h, "GET", path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
	}
	// The page has to load to show the login, and it holds no world data.
	if rec := do(h, "GET", "/", "", nil); rec.Code != http.StatusOK {
		t.Errorf("GET / without a session = %d, want 200", rec.Code)
	}
}

// The whole login, as a browser and the agent would drive it.
func TestLogin_CodeTypedInGameUnlocksTheBrowserThatShowedIt(t *testing.T) {
	s := withLogin(t)
	public, internal := s.Handler(), s.InternalHandler()

	start := do(public, "POST", "/auth/start", "", nil)
	if start.Code != http.StatusOK {
		t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	var started struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expiresIn"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	pending := cookie(start, "__Host-mcmap_pending")
	if len(started.Code) != 6 || started.ExpiresIn != 600 || pending == nil {
		t.Fatalf("start = %+v, pending cookie %v", started, pending)
	}
	// The secret that collects the login must not be readable by script,
	// nor sent along with a request another site started.
	if !pending.HttpOnly || !pending.Secure || pending.Path != "/" || pending.SameSite != http.SameSiteStrictMode {
		t.Errorf("pending cookie flags = %+v", pending)
	}
	if strings.Contains(start.Body.String(), pending.Value) {
		t.Error("the response body carries the secret")
	}

	if rec := do(public, "GET", "/auth/status", "", []*http.Cookie{pending}); !strings.Contains(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("status before the claim = %s", rec.Body)
	}

	claim := do(internal, "POST", "/internal/v1/claims", `{"code":"`+strings.ToLower(started.Code)+`","xuid":"2535412345678901","gamertag":"Steve Builds"}`, nil,
		"Authorization", "Bearer "+internalToken)
	if claim.Code != http.StatusNoContent {
		t.Fatalf("claim = %d %s", claim.Code, claim.Body)
	}

	status := do(public, "GET", "/auth/status", "", []*http.Cookie{pending})
	session := cookie(status, "__Host-mcmap_session")
	if !strings.Contains(status.Body.String(), `"state":"ok"`) || session == nil {
		t.Fatalf("status after the claim = %s, session cookie %v", status.Body, session)
	}

	if rec := do(public, "GET", "/api/map", "", []*http.Cookie{session}); rec.Code != http.StatusOK {
		t.Errorf("GET /api/map with the session = %d", rec.Code)
	}
	if rec := do(public, "GET", "/api/me", "", []*http.Cookie{session}); !strings.Contains(rec.Body.String(), `"gamertag":"Steve Builds"`) {
		t.Errorf("GET /api/me = %s", rec.Body)
	}

	// One login per code, and one hand-over per login.
	if rec := do(public, "GET", "/auth/status", "", []*http.Cookie{pending}); cookie(rec, "__Host-mcmap_session") != nil || !strings.Contains(rec.Body.String(), `"state":"expired"`) {
		t.Errorf("a second collection = %s", rec.Body)
	}
	again := do(internal, "POST", "/internal/v1/claims", `{"code":"`+started.Code+`","xuid":"999","gamertag":"Alex"}`, nil, "Authorization", "Bearer "+internalToken)
	if again.Code != http.StatusNotFound {
		t.Errorf("a second claim of a used code = %d, want 404", again.Code)
	}

	out := do(public, "POST", "/auth/logout", "", []*http.Cookie{session})
	if c := cookie(out, "__Host-mcmap_session"); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout did not clear the session cookie: %+v", c)
	}
}

func TestLogin_SomeoneElsesBrowserGetsNothing(t *testing.T) {
	s := withLogin(t)
	public, internal := s.Handler(), s.InternalHandler()

	start := do(public, "POST", "/auth/start", "", nil)
	var started struct{ Code string }
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	do(internal, "POST", "/internal/v1/claims", `{"code":"`+started.Code+`","xuid":"1","gamertag":"Steve"}`, nil, "Authorization", "Bearer "+internalToken)

	// Knowing the code is not enough: only the browser holding the secret
	// collects the login.
	for name, cookies := range map[string][]*http.Cookie{
		"no cookie":    nil,
		"wrong secret": {{Name: "__Host-mcmap_pending", Value: "not-the-secret"}},
	} {
		rec := do(public, "GET", "/auth/status", "", cookies)
		if cookie(rec, "__Host-mcmap_session") != nil || !strings.Contains(rec.Body.String(), `"state":"expired"`) {
			t.Errorf("%s: %s, session cookie set = %v", name, rec.Body, cookie(rec, "__Host-mcmap_session") != nil)
		}
	}
}

func TestClaims_AreForTheAgentOnly(t *testing.T) {
	s := withLogin(t)
	public, internal := s.Handler(), s.InternalHandler()
	body := `{"code":"ABCDEF","xuid":"1","gamertag":"Steve"}`

	// Not routed from the internet at all.
	if rec := do(public, "POST", "/internal/v1/claims", body, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("claims on the public handler = %d, want it not served there at all", rec.Code)
	}
	if rec := do(public, "GET", "/metrics", "", nil); rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "mcmap_") {
		t.Error("the public handler serves metrics")
	}
	if rec := do(internal, "GET", "/metrics", "", nil); rec.Code != http.StatusOK {
		t.Errorf("metrics on the internal handler = %d", rec.Code)
	}

	for name, header := range map[string][]string{
		"no token":    nil,
		"wrong token": {"Authorization", "Bearer nope"},
		"not bearer":  {"Authorization", internalToken},
	} {
		if rec := do(internal, "POST", "/internal/v1/claims", body, nil, header...); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, rec.Code)
		}
	}
	auth := []string{"Authorization", "Bearer " + internalToken}
	if rec := do(internal, "POST", "/internal/v1/claims", body, nil, auth...); rec.Code != http.StatusNotFound {
		t.Errorf("unknown code = %d, want 404", rec.Code)
	}
	for _, bad := range []string{
		`{`,
		`{"code":"ABCDEF","gamertag":"Steve"}`,
		`{"code":"ABCDEF","xuid":"1","extra":true}`,
		// Only a player has an XUID; the console's stand-in is not one.
		`{"code":"ABCDEF","xuid":"<server>","gamertag":"Server"}`,
		`{"code":"ABCDEF","xuid":"12345678901234567890123","gamertag":"Steve"}`,
		`{"code":"ABCDEF","xuid":"1","gamertag":"` + strings.Repeat("x", 65) + `"}`,
		`{"code":"ABCDEF","xuid":"1","gamertag":"Ste\u0000ve"}`,
	} {
		if rec := do(internal, "POST", "/internal/v1/claims", bad, nil, auth...); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", bad, rec.Code)
		}
	}
}

// A burst of logins nobody finishes must not shut out the next real one.
func TestLogin_AFullTableStillStartsNewLogins(t *testing.T) {
	s := withLogin(t) // room for 2
	h := s.Handler()
	for range 10 {
		if rec := do(h, "POST", "/auth/start", "", nil); rec.Code != http.StatusOK {
			t.Fatalf("start = %d %s", rec.Code, rec.Body)
		}
	}
}

// With no login configured the service behaves as it did before logins
// existed, and says so rather than half-offering one.
func TestLogin_DisabledLeavesTheMapOpenAndTheLoginRoutesAbsent(t *testing.T) {
	s, _ := fixture(t)
	h := s.Handler()
	if rec := do(h, "GET", "/api/map", "", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /api/map = %d", rec.Code)
	}
	if rec := do(h, "POST", "/auth/start", "", nil); rec.Code == http.StatusOK {
		t.Error("a login was offered with none configured")
	}
	if rec := do(h, "GET", "/api/config", "", nil); !strings.Contains(rec.Body.String(), `"login":false`) {
		t.Errorf("GET /api/config = %s", rec.Body)
	}
}

// Before a login the page learns only that there is one. What the world is
// called is for players.
func TestConfig_SaysThereIsALoginAndNothingAboutTheWorld(t *testing.T) {
	s := withLogin(t)
	rec := do(s.Handler(), "GET", "/api/config", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"login":true`) {
		t.Errorf("GET /api/config = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "FWB") {
		t.Errorf("GET /api/config names the world before a login: %s", rec.Body)
	}
}

// However the path is spelled and whatever the method, map data never comes
// back without a session.
func TestLogin_NoSpellingOfAPathGetsAroundTheGate(t *testing.T) {
	s := withLogin(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, method := range []string{"GET", "HEAD"} {
		for _, path := range []string{
			"/tiles/overworld/0/37/-4.webp",
			"/%74iles/overworld/0/37/-4.webp",
			"/tiles/overworld/0/37/-4%2ewebp",
			"/tiles/overworld/0/37/-4.webp?x=1",
			"//tiles/overworld/0/37/-4.webp",
			"/x/../tiles/overworld/0/37/-4.webp",
			"/tiles/overworld/0/37/-4.webp/",
			"/TILES/overworld/0/37/-4.webp",
			"/api/map",
			"/api/map/",
			"/api/../api/map",
			"/api/me",
		} {
			req, _ := http.NewRequest(method, srv.URL+path, nil)
			res, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				t.Errorf("%s %s = 200 without a session", method, path)
			}
		}
	}
}

func TestLogin_SecurityHeadersCoverRefusalsAndTheLoginItself(t *testing.T) {
	s := withLogin(t)
	h := s.Handler()
	for _, rec := range []*httptest.ResponseRecorder{
		do(h, "GET", "/api/map", "", nil),
		do(h, "GET", "/tiles/overworld/0/37/-4.webp", "", nil),
		do(h, "POST", "/auth/start", "", nil),
		do(h, "GET", "/auth/status", "", nil),
		do(h, "POST", "/auth/start", "", nil, "Sec-Fetch-Site", "cross-site"),
	} {
		for _, name := range []string{"Content-Security-Policy", "Strict-Transport-Security", "X-Content-Type-Options", "Referrer-Policy"} {
			if rec.Header().Get(name) == "" {
				t.Errorf("status %d response lacks %s", rec.Code, name)
			}
		}
	}
}

// Another site must not be able to start, burn or end a login in a
// visitor's browser. Sites under the same parent domain count as other
// sites: they are not this service.
func TestLogin_OtherSitesCannotDriveIt(t *testing.T) {
	s := withLogin(t)
	h := s.Handler()
	for _, path := range []string{"/auth/start", "/auth/logout"} {
		for name, header := range map[string][]string{
			"cross-site":             {"Sec-Fetch-Site", "cross-site"},
			"same-site sibling":      {"Sec-Fetch-Site", "same-site"},
			"older browser, foreign": {"Origin", "https://evil.example"},
		} {
			rec := do(h, "POST", path, "", nil, header...)
			if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
				t.Errorf("%s POST %s = %d with %d cookies, want 403 and none", name, path, rec.Code, len(rec.Result().Cookies()))
			}
		}
		for name, header := range map[string][]string{
			"the page itself":      {"Sec-Fetch-Site", "same-origin"},
			"older browser, ours":  {"Origin", "https://example.com"},
			"not a browser at all": nil,
		} {
			if rec := do(h, "POST", path, "", nil, header...); rec.Code == http.StatusForbidden {
				t.Errorf("%s POST %s was refused", name, path)
			}
		}
	}
}

// Sessions cannot be listed or revoked one by one, so the log is the only
// record of who was let in.
func TestLogin_IssuingASessionIsLogged(t *testing.T) {
	s := withLogin(t)
	var out bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&out, nil))
	public, internal := s.Handler(), s.InternalHandler()

	start := do(public, "POST", "/auth/start", "", nil)
	var started struct{ Code string }
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	pending := cookie(start, "__Host-mcmap_pending")
	do(internal, "POST", "/internal/v1/claims", `{"code":"`+started.Code+`","xuid":"2535412345678901","gamertag":"Steve Builds"}`, nil, "Authorization", "Bearer "+internalToken)
	if out.Len() != 0 {
		t.Errorf("logged before any session was issued: %s", out.String())
	}
	status := do(public, "GET", "/auth/status", "", []*http.Cookie{pending})

	logged := out.String()
	if !strings.Contains(logged, `"xuid":"2535412345678901"`) || !strings.Contains(logged, `"gamertag":"Steve Builds"`) {
		t.Errorf("log = %q", logged)
	}
	if session := cookie(status, "__Host-mcmap_session"); session == nil || strings.Contains(logged, session.Value) || strings.Contains(logged, pending.Value) {
		t.Error("the log carries a credential, or no session was issued")
	}
}

func logIn(t *testing.T, s *Server, xuid string) *http.Cookie {
	t.Helper()
	public, internal := s.Handler(), s.InternalHandler()
	start := do(public, "POST", "/auth/start", "", nil)
	var started struct{ Code string }
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	do(internal, "POST", "/internal/v1/claims", `{"code":"`+started.Code+`","xuid":"`+xuid+`","gamertag":"Steve"}`, nil, "Authorization", "Bearer "+internalToken)
	session := cookie(do(public, "GET", "/auth/status", "", []*http.Cookie{cookie(start, "__Host-mcmap_pending")}), "__Host-mcmap_session")
	if session == nil {
		t.Fatal("no session issued")
	}
	return session
}

// What the agent calls when a player types !map logout.
func TestRevocations_LogAPlayerOutEverywhere(t *testing.T) {
	s := withLogin(t)
	clock := time.Now()
	s.Sessions.Now = func() time.Time { return clock }
	public, internal := s.Handler(), s.InternalHandler()
	session := logIn(t, s, "2535412345678901")
	bearer := []string{"Authorization", "Bearer " + internalToken}

	if rec := do(public, "POST", "/internal/v1/revocations", `{"xuid":"2535412345678901"}`, nil, bearer...); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("revocations on the public handler = %d, want it not served there at all", rec.Code)
	}
	if rec := do(internal, "POST", "/internal/v1/revocations", `{"xuid":"2535412345678901"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	for _, bad := range []string{`{`, `{}`, `{"xuid":"<server>"}`, `{"xuid":"1","extra":true}`} {
		if rec := do(internal, "POST", "/internal/v1/revocations", bad, nil, bearer...); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", bad, rec.Code)
		}
	}
	if rec := do(public, "GET", "/api/map", "", []*http.Cookie{session}); rec.Code != http.StatusOK {
		t.Fatalf("the session stopped working before it was revoked: %d", rec.Code)
	}

	clock = clock.Add(time.Second)
	if rec := do(internal, "POST", "/internal/v1/revocations", `{"xuid":"2535412345678901"}`, nil, bearer...); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
	}
	if rec := do(public, "GET", "/api/map", "", []*http.Cookie{session}); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/map with a revoked session = %d, want 401", rec.Code)
	}
}
