package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

var (
	hexUpTo12   = regexp.MustCompile(`^[0-9a-f]{0,12}$`)
	goVersion   = regexp.MustCompile(`^(go[0-9.]+)?$`)
	rfc3339     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)
	hex8        = regexp.MustCompile(`^[0-9a-f]{8}$`)
	featureKeys = []string{"login", "live", "markers", "waypoints", "icons", "structures", "biomes", "trails", "history", "admin", "passkeys"}
)

// checkShape fails on any key or string outside D8's table: every pod in
// the cluster can read the internal copy, so it has nowhere to put a name.
func checkShape(t *testing.T, raw []byte, admin bool) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"build": true, "go": true, "now": true, "problem": true, "quiet": true, "features": true, "snapshotAt": true, "renderedAt": true, "assets": true, "operators": true}
	if admin {
		allowed["live"], allowed["sessions"] = true, true
	}
	for k := range top {
		if !allowed[k] {
			t.Errorf("a field the spec does not list: %s", k)
		}
	}
	var s struct {
		Build, Go, Now, Problem, Operators string
		SnapshotAt                         string
		Quiet                              bool
		Features                           map[string]bool
		RenderedAt, Assets                 map[string]string
		Live                               *struct {
			LastFrameAgeSeconds map[string]float64 `json:"lastFrameAgeSeconds"`
		}
		Sessions *struct{ Issued, PasskeyLogins, Refused int64 }
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if !hexUpTo12.MatchString(s.Build) || !goVersion.MatchString(s.Go) || !rfc3339.MatchString(s.Now) {
		t.Errorf("build %q go %q now %q", s.Build, s.Go, s.Now)
	}
	if s.Problem != "" && s.Problem != "snapshot" && s.Problem != "render" {
		t.Errorf("problem %q", s.Problem)
	}
	if s.Operators != "live" && s.Operators != "last_known_good" && s.Operators != "none" {
		t.Errorf("operators %q", s.Operators)
	}
	if s.SnapshotAt != "" && !rfc3339.MatchString(s.SnapshotAt) {
		t.Errorf("snapshotAt %q", s.SnapshotAt)
	}
	for k := range s.Features {
		if !strings.Contains(" "+strings.Join(featureKeys, " ")+" ", " "+k+" ") {
			t.Errorf("feature %q", k)
		}
	}
	for dim, at := range s.RenderedAt {
		if !strings.Contains(" "+strings.Join(render.Dimensions, " ")+" ", " "+dim+" ") || !rfc3339.MatchString(at) {
			t.Errorf("renderedAt %q %q", dim, at)
		}
	}
	for file, print := range s.Assets {
		if !hex8.MatchString(print) || strings.ContainsAny(file, "/\\ ") {
			t.Errorf("asset %q %q", file, print)
		}
	}
	if admin {
		onlyKeys(t, top["sessions"], "issued", "passkeyLogins", "refused")
		if raw, ok := top["live"]; ok {
			onlyKeys(t, raw, "lastFrameAgeSeconds")
		}
	}
	if s.Live != nil {
		for dim := range s.Live.LastFrameAgeSeconds {
			if !strings.Contains(" "+strings.Join(render.Dimensions, " ")+" ", " "+dim+" ") {
				t.Errorf("live dimension %q", dim)
			}
		}
	}
}

// onlyKeys fails on any key of a nested admin object that the spec does not
// list, and on any listed key that is missing.
func onlyKeys(t *testing.T, raw json.RawMessage, keys ...string) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Errorf("nested object: %v: %s", err, raw)
		return
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
		if _, ok := obj[k]; !ok {
			t.Errorf("nested object lacks %q: %s", k, raw)
		}
	}
	for k := range obj {
		if !want[k] {
			t.Errorf("a nested field the spec does not list: %s", k)
		}
	}
}

func TestAbsent_AGetToAnUnregisteredRouteIsA404(t *testing.T) {
	if absent(http.MethodGet, http.StatusMethodNotAllowed) || !absent(http.MethodPost, http.StatusMethodNotAllowed) || !absent(http.MethodGet, http.StatusNotFound) {
		t.Error("absent accepts the wrong codes")
	}
}

func TestGoRelease_KeepsOnlyTheVersion(t *testing.T) {
	for in, want := range map[string]string{"go1.27.2 X:boringcrypto": "go1.27.2", "go1.24": "go1.24", "devel +abc": "", "": ""} {
		if got := goRelease(in); got != want {
			t.Errorf("goRelease(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatus_InternalCopyIsTheClosedShapeAndNamesNobody(t *testing.T) {
	s, _, _ := withService(t) // a server full of made-up players, markers and structures
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	rec := do(s.InternalHandler(), "GET", "/internal/v1/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("internal status = %d", rec.Code)
	}
	checkShape(t, rec.Body.Bytes(), false)
	for _, never := range []string{steve.XUID, steve.Gamertag, "FWB", "http", "buried_treasure", "/data", "Mid", fmt.Sprint(secretSeed)} {
		if strings.Contains(rec.Body.String(), never) {
			t.Errorf("the internal status holds %q: %s", never, rec.Body)
		}
	}
}

func TestStatus_AdminCopyAddsOnlyItsTwoFields(t *testing.T) {
	s, _, _ := withService(t)
	s.Operators = fakeOperators{steve.XUID: true}
	rec := do(s.Handler(), "GET", "/api/admin/status", "", []*http.Cookie{session(s, steve)})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d", rec.Code)
	}
	checkShape(t, rec.Body.Bytes(), true)
	if !strings.Contains(rec.Body.String(), `"sessions"`) {
		t.Errorf("the admin status lacks sessions: %s", rec.Body)
	}
	if do(s.Handler(), "GET", "/api/admin/status", "", []*http.Cookie{session(s, alex)}).Code != http.StatusForbidden {
		t.Error("a member read the admin status")
	}
}

func TestStatus_SaysTheProblemThePageShows(t *testing.T) {
	s, _, _ := withService(t)
	s.Status.MarkProblem("render")
	rec := do(s.InternalHandler(), "GET", "/internal/v1/status", "", nil)
	if !strings.Contains(rec.Body.String(), `"problem":"render"`) {
		t.Errorf("status = %s", rec.Body)
	}
}

// absent is what an unregistered route answers: 405 where the page's own
// GET / pattern shares the path, 404 elsewhere. Either way no handler ran.
func absent(method string, code int) bool {
	return code == http.StatusNotFound || (method != http.MethodGet && code == http.StatusMethodNotAllowed)
}

// The kill switches end what was issued under them, on the next request.
func TestKillSwitches_EndWhatWasIssuedUnderThem(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	game := session(s, steve)
	pk := cookie(passkeyLogin(t, s, d), "__Host-mcmap_session")
	adminRoutes := [][2]string{{"GET", "/api/admin/status"}, {"POST", "/auth/passkey/register/options"}, {"POST", "/auth/passkey/register/finish"}, {"POST", "/auth/passkey/register/confirm"}, {"POST", "/auth/passkey/register/cancel"}}
	passkeyRoutes := [][2]string{{"POST", "/auth/passkey/options"}, {"POST", "/auth/passkey/finish"}, {"GET", "/api/passkeys"}, {"DELETE", "/api/passkeys/x"}, {"POST", "/api/logout-all"}}

	// Passkeys off: the chart's map.admin.passkeys: false.
	s.Passkeys, s.Ceremonies, s.Sessions.Passkeys = nil, nil, nil
	h := s.Handler()
	for _, r := range passkeyRoutes {
		if rec := do(h, r[0], r[1], "", []*http.Cookie{game}); !absent(r[0], rec.Code) {
			t.Errorf("passkeys off: %s %s = %d, want 404", r[0], r[1], rec.Code)
		}
	}
	for _, path := range []string{"/api/map", "/api/me", "/api/admin/status"} {
		if rec := do(h, "GET", path, "", []*http.Cookie{pk}); rec.Code != http.StatusUnauthorized {
			t.Errorf("passkeys off: a passkey session's GET %s = %d, want 401", path, rec.Code)
		}
	}
	if rec := do(s.InternalHandler(), "POST", "/internal/v1/passkey-codes", `{"xuid":"`+steve.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusNotFound {
		t.Errorf("passkeys off: the code route = %d, want 404", rec.Code)
	}

	// Admin off as well: both values false in one change.
	s.Admin = false
	h = s.Handler()
	for _, r := range adminRoutes {
		if rec := do(h, r[0], r[1], "", []*http.Cookie{game}); !absent(r[0], rec.Code) {
			t.Errorf("admin off: %s %s for an operator's cookie = %d, want 404", r[0], r[1], rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{game}); strings.Contains(rec.Body.String(), "admin") {
		t.Errorf("admin off: /api/me = %s", rec.Body)
	}
}

// With the login off and both switches asked for, the map serves as an open
// map always has, and no admin or passkey route exists.
func TestAdmin_WithNoLoginNothingErrors(t *testing.T) {
	s, _ := fixture(t)
	s.Admin, s.Operators = true, fakeOperators{steve.XUID: true}
	h := s.Handler()
	for _, path := range []string{"/api/config", "/api/map", "/api/diagnostics"} {
		if rec := do(h, "GET", path, "", nil); rec.Code != http.StatusOK {
			t.Errorf("no login: GET %s = %d", path, rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/admin/status", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("no login: /api/admin/status = %d, want 404", rec.Code)
	}
	if rec := do(s.InternalHandler(), "GET", "/internal/v1/status", "", nil); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"admin":true`) {
		t.Errorf("no login: internal status = %d %s", rec.Code, rec.Body)
	}
}

// The policy that keeps foreign script off the page is the one control
// between a script and an admin's session: byte for byte what it was, on
// every new route as on the old.
func TestSecurityHeaders_TheCSPIsByteForByteWhatItWas(t *testing.T) {
	const want = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	h := s.Handler()
	for _, r := range [][2]string{{"GET", "/"}, {"GET", "/api/config"}, {"GET", "/api/me"}, {"GET", "/api/admin/status"}, {"GET", "/api/passkeys"}, {"POST", "/auth/passkey/options"}, {"POST", "/auth/passkey/finish"}, {"POST", "/auth/passkey/register/options"}} {
		rec := do(h, r[0], r[1], "", []*http.Cookie{session(s, steve)})
		if got := rec.Header().Get("Content-Security-Policy"); got != want {
			t.Errorf("%s %s: CSP = %q", r[0], r[1], got)
		}
	}
}
