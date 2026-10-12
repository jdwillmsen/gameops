package server

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
)

// Made up, like every credential in these tests.
const serviceSecret = "a-made-up-service-secret-0123456789abcdef"

// withService is a map with every route there is, a login, service
// sessions switched on, and a log that can be read back.
func withService(t *testing.T) (*Server, *fakeWaypoints, *bytes.Buffer) {
	t.Helper()
	s, waypoints := withMarkers(t)
	s.Live = live.New(10*time.Second, 1000, nil)
	mobs := mobIcons(t, func(context.Context) (icons.Set, error) { return fetched(t), nil })
	mobs.Run(t.Context())
	s.MobIcons, s.Art, s.Heads = mobs, mobs, &icons.Heads{}
	store := &biomes.Store{}
	store.Set(biomeWorld())
	s.Biomes = store
	s.Structures = detailed()
	trailed, _ := withTrails(t)
	s.Trails = trailed.Trails

	s.Sessions.ServiceKey = auth.ServiceKey(s.Sessions.Key, serviceSecret)
	s.Sessions.ServiceTTL = 10 * time.Minute
	s.Sessions.ServiceSeen = CountServiceRequest
	s.Service = auth.NewExchange(serviceSecret, time.Now)
	var log bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&log, nil))
	return s, waypoints, &log
}

func exchange(s *Server, secret string) *httptest.ResponseRecorder {
	return do(s.InternalHandler(), "POST", "/internal/v1/service-sessions", "", nil, "Authorization", "Bearer "+secret)
}

func serviceSession(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	rec := exchange(s, serviceSecret)
	c := cookie(rec, "__Host-mcmap_session")
	if rec.Code != http.StatusOK || c == nil {
		t.Fatalf("exchange = %d %s, cookie %v", rec.Code, rec.Body, c)
	}
	return c
}

// Every route of both listeners: the gate it is behind, and for the public
// listener's an address that reaches its handler. A new route has to be
// added here, with its gate thought about, before this file passes.
var routes = map[string]struct{ gate, path string }{
	"GET /healthz":                          {"gatePublic", "/healthz"},
	"GET /api/config":                       {"gatePublic", "/api/config"},
	"GET /":                                 {"gatePublic", "/"},
	"POST /auth/start":                      {"gatePublic", ""},
	"GET /auth/status":                      {"gatePublic", "/auth/status"},
	"POST /auth/logout":                     {"gatePublic", ""},
	"GET /api/map":                          {"gateViewer", "/api/map"},
	"GET /tiles/{dimension}/{zoom}/{x}/{y}": {"gateViewer", "/tiles/overworld/0/37/-4.webp"},
	"GET /api/live":                         {"gateViewer", ""},
	"GET /api/markers":                      {"gateViewer", "/api/markers?dimension=overworld"},
	"GET /api/icons":                        {"gateViewer", "/api/icons"},
	"GET /api/icons/picture/{group}/{name}": {"gateViewer", "/api/icons/picture/container/chest"},
	"GET /api/names":                        {"gateViewer", "/api/names"},
	"GET /api/icons/mob/{type}":             {"gateViewer", "/api/icons/mob/cow"},
	"GET /api/icons/head":                   {"gateViewer", "/api/icons/head?name=nobody"},
	"GET /api/structures":                   {"gateViewer", "/api/structures?dimension=overworld"},
	"GET /api/structures/detail":            {"gateViewer", villageDetailPath},
	"GET /api/biomes":                       {"gateViewer", "/api/biomes?dimension=overworld"},
	"GET /api/biomes/at":                    {"gateViewer", "/api/biomes/at?dimension=overworld&x=0&z=0"},
	"GET /api/biomes/nearest":               {"gateViewer", "/api/biomes/nearest?dimension=overworld&biome=plains&x=0&z=0"},
	"GET /api/biomes/region":                {"gateViewer", "/api/biomes/region?dimension=overworld&x=0&z=0"},
	"GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}": {"gateViewer", "/api/biomes/tiles/overworld/0/0/0.png"},
	"GET /api/trails": {"gateViewer", "/api/trails?dimension=overworld"},
	// The map as it was on each day, which is every player's to look at.
	"GET /api/history": {"gateViewer", ""},
	"GET /api/history/tiles/{day}/{dimension}/{zoom}/{x}/{y}": {"gateViewer", ""},
	"GET /api/search":    {"gateViewer", "/api/search?dimension=overworld&x=0&z=0&q=a"},
	"GET /api/me":        {"gateViewer", "/api/me"},
	"GET /api/waypoints": {"gatePlayer", "/api/waypoints"},
	// The internal listener's.
	"GET /metrics":                        {"gatePublic", ""},
	"GET /internal/v1/world":              {"gateAgent", ""},
	"POST /internal/v1/world/acknowledge": {"gateAgent", ""},
	"PUT /internal/v1/heads":              {"gateAgent", ""},
	"POST /internal/v1/claims":            {"gateAgent", ""},
	"POST /internal/v1/revocations":       {"gateAgent", ""},
	"POST /internal/v1/service-sessions":  {"gateExchange", ""},
}

// routesInSource reads every route this package registers, and the gate
// each is behind, out of every file of it that is not a test: a mux does
// not list its own. It fails on a handler given to a mux anywhere but in
// route, which is how a route would get past this file.
func routesInSource(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	set := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				at := set.Position(call.Pos())
				switch sel.Sel.Name {
				case "Handle", "HandleFunc":
					// Whatever it is called on: a mux by another name
					// is still a mux.
					if fn.Name.Name != "route" {
						t.Errorf("%s: %s registers a handler with %s directly; every route goes through route, with its gate", at, fn.Name.Name, sel.Sel.Name)
					}
				case "route":
					if len(call.Args) != 4 {
						t.Errorf("%s: route called with %d arguments", at, len(call.Args))
						return true
					}
					behind, isName := call.Args[1].(*ast.Ident)
					pattern, isLiteral := call.Args[2].(*ast.BasicLit)
					if !isName || !isLiteral || !strings.HasPrefix(behind.Name, "gate") {
						t.Errorf("%s: a route's gate and pattern are written out where it is registered, not worked out", at)
						return true
					}
					text, _ := strconv.Unquote(pattern.Value)
					if was, twice := found[text]; twice && was != behind.Name {
						t.Errorf("%s: %s is registered behind %s and behind %s", at, text, was, behind.Name)
					}
					found[text] = behind.Name
				}
				return true
			})
		}
	}
	if len(found) < 20 {
		t.Fatalf("only %d routes found in the package's source", len(found))
	}
	return found
}

func TestService_EveryRouteHasAGateThatWasChosenForIt(t *testing.T) {
	found := routesInSource(t)
	for pattern, gate := range found {
		want, listed := routes[pattern]
		switch {
		case !listed:
			t.Errorf("%s is not in this test's list. Decide whether a service session may read it (gateViewer) or not (gatePlayer), which it may not if it is one player's own or changes anything, and add it", pattern)
		case want.gate != gate:
			t.Errorf("%s is behind %s in the source and %s here", pattern, gate, want.gate)
		}
		// The invariant the roles to come lean on: nothing that is not a
		// plain read is ever open to a service session.
		if gate == "gateViewer" && !strings.HasPrefix(pattern, "GET ") {
			t.Errorf("%s is shown to a service session and is not a GET", pattern)
		}
	}
	for pattern := range routes {
		if _, ok := found[pattern]; !ok {
			t.Errorf("%s is listed here and is not a route any more", pattern)
		}
	}
}

// The reader above has to see what it is there to catch.
func TestService_ARouteRegisteredAnotherWayIsSeen(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "elsewhere.go", `package server
func (s *Server) more(m *http.ServeMux) { m.HandleFunc("POST /api/reset", s.handleMe); s.side.Handle("GET /x", nil) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc") {
				seen++
			}
		}
		return true
	})
	if seen != 2 {
		t.Errorf("the reader's test for a direct registration saw %d of 2", seen)
	}
}

func TestService_ASessionReachesWhatIsShownAndIsForbiddenTheRest(t *testing.T) {
	s, waypoints, _ := withService(t)
	h := s.Handler()
	service, player := serviceSession(t, s), session(s, steve)
	allowed, forbidden := testutil.ToFloat64(metricServiceRequests.WithLabelValues(serviceAllowed)), testutil.ToFloat64(metricServiceRequests.WithLabelValues(serviceForbidden))
	var wantAllowed, wantForbidden float64
	for pattern, route := range routes {
		if route.path == "" || route.gate == "gateAgent" || route.gate == "gateExchange" {
			continue
		}
		asService := do(h, "GET", route.path, "", []*http.Cookie{service})
		asPlayer := do(h, "GET", route.path, "", []*http.Cookie{player})
		// A player is turned away from nothing by any of this.
		if asPlayer.Code == http.StatusForbidden || asPlayer.Code == http.StatusUnauthorized {
			t.Errorf("%s as a player = %d", pattern, asPlayer.Code)
		}
		switch route.gate {
		case "gatePlayer":
			wantForbidden++
			if asService.Code != http.StatusForbidden {
				t.Errorf("%s as a service session = %d, want 403", pattern, asService.Code)
			}
		case "gateViewer":
			wantAllowed++
			if asService.Code != asPlayer.Code {
				t.Errorf("%s as a service session = %d, as a player %d", pattern, asService.Code, asPlayer.Code)
			}
		default:
			if asService.Code != asPlayer.Code {
				t.Errorf("%s as a service session = %d, as a player %d", pattern, asService.Code, asPlayer.Code)
			}
		}
	}
	if got := testutil.ToFloat64(metricServiceRequests.WithLabelValues(serviceAllowed)) - allowed; got != wantAllowed {
		t.Errorf("allowed requests counted = %v, want %v", got, wantAllowed)
	}
	if got := testutil.ToFloat64(metricServiceRequests.WithLabelValues(serviceForbidden)) - forbidden; got != wantForbidden {
		t.Errorf("forbidden requests counted = %v, want %v", got, wantForbidden)
	}
	// The agent holds the waypoints, and was asked for the player's only:
	// once to list them and once to search them.
	if len(waypoints.asked) != 2 || waypoints.asked[0] != steve.XUID || waypoints.asked[1] != steve.XUID {
		t.Errorf("the agent was asked for the waypoints of %v, want %s twice and nobody else", waypoints.asked, steve.XUID)
	}
}

// What a shown route says of the player asking is left out for a session
// that is no player's.
func TestService_NothingOfAPlayersOwnIsInWhatASessionIsShown(t *testing.T) {
	s, waypoints, _ := withService(t)
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	seenAt(s, renderedAt.Add(-time.Hour))
	service, player := serviceSession(t, s), session(s, steve)

	if got := listing(t, s, player); got.Me == "" {
		t.Fatal("the fixture's player is not listed as themselves, so this test proves nothing")
	}
	if got := listing(t, s, service); got.Me != "" || len(got.Mobs.Types) == 0 {
		t.Errorf("icons for a service session: me = %q, mob types = %v; want nobody as me and the icons everyone sees", got.Me, got.Mobs.Types)
	}

	if got, body := detailOf(t, s, villageDetailPath, player); got.Standing == nil || got.Standing.State != standingKnown {
		t.Fatalf("the fixture's player has no standing, so this test proves nothing: %s", body)
	}
	if got, body := detailOf(t, s, villageDetailPath, service); got.Standing != nil || got.Detail == nil || strings.Contains(body, "standing") {
		t.Errorf("a village for a service session = %s, want its detail and no standing", body)
	}

	waypoints.asked = nil
	if got := search(t, s, "base", service); got.Waypoints != waypointsOff {
		t.Errorf("search for a service session says waypoints are %q, want %q", got.Waypoints, waypointsOff)
	}
	for _, hit := range search(t, s, "base", service).Hits {
		if hit.Kind == "waypoint" {
			t.Errorf("a service session's search found the waypoint %+v", hit)
		}
	}
	if len(waypoints.asked) != 0 {
		t.Errorf("the agent was asked for waypoints of %q for a service session", waypoints.asked)
	}

	var me struct {
		Gamertag string `json:"gamertag"`
		Service  bool   `json:"service"`
	}
	_ = json.Unmarshal(do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{service}).Body.Bytes(), &me)
	if me.Gamertag != auth.ServiceName || !me.Service {
		t.Errorf("/api/me for a service session = %+v, want %q marked as a service", me, auth.ServiceName)
	}
	if body := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{player}).Body.String(); strings.Contains(body, "service") {
		t.Errorf("/api/me for a player = %s", body)
	}
}

// recordingHeads notes every XUID anyone is looked up by.
type recordingHeads struct {
	PlayerHeads
	asked *[]string
}

func (h recordingHeads) Listing(xuid string) (map[string]string, string) {
	*h.asked = append(*h.asked, xuid)
	return h.PlayerHeads.Listing(xuid)
}

// A service session is no player by how the answer is made, not by each
// handler remembering: asked as one on every route there is, nothing that
// is kept or looked up by who is asking is touched, with any key at all.
func TestService_NoCodePathTreatsASessionAsAPlayer(t *testing.T) {
	s, waypoints, _ := withService(t)
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	var asked []string
	s.Heads = recordingHeads{s.Heads, &asked}
	waypoints.asked = nil
	service := serviceSession(t, s)
	h := s.Handler()
	for pattern, route := range routes {
		if route.path == "" {
			continue
		}
		for _, path := range []string{route.path, strings.Replace(route.path, "q=a", "q=base", 1)} {
			if rec := do(h, "GET", path, "", []*http.Cookie{service}); rec.Code >= 500 {
				t.Errorf("%s as a service session = %d", pattern, rec.Code)
			}
		}
	}
	if len(asked) != 0 {
		t.Errorf("a service session had the heads looked up by %q", asked)
	}
	if len(waypoints.asked) != 0 {
		t.Errorf("a service session had waypoints asked for by %q", waypoints.asked)
	}
	s.mu.Lock()
	known := len(s.players)
	s.mu.Unlock()
	if known != 0 {
		t.Errorf("a service session is remembered among %d players of the world", known)
	}
	s.search.mu.Lock()
	cached := len(s.search.waypoints)
	s.search.mu.Unlock()
	if cached != 0 {
		t.Errorf("a service session has %d entries in the waypoint cache", cached)
	}
	// And should a handler ever ask for one by no XUID, there is nothing
	// kept under it to hand back.
	if list, err := s.search.playerWaypoints(t.Context(), s.Waypoints, "", time.Now); err == nil || list != nil || len(waypoints.asked) != 0 {
		t.Errorf("waypoints were read for no XUID: %v, %v", list, err)
	}
	if _, self := s.Heads.Listing(""); self != "" {
		t.Errorf("no XUID is listed as %q", self)
	}
}

// The invariant for the roles to come: a service session cannot do
// anything but read, on any route, and cannot be turned into a player's.
func TestService_ASessionOnlyEverReads(t *testing.T) {
	s, _, _ := withService(t)
	h := s.Handler()
	service := serviceSession(t, s)
	for pattern, route := range routes {
		if route.path == "" || (route.gate != "gateViewer" && route.gate != "gatePlayer") {
			continue
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			// The same site, so that it is the session that is refused
			// and not the request's origin.
			rec := do(h, method, route.path, "", []*http.Cookie{service}, "Sec-Fetch-Site", "same-origin")
			if rec.Code < 400 {
				t.Errorf("%s %s as a service session = %d", method, pattern, rec.Code)
			}
		}
	}
	// Neither login step hands a player's session to whoever holds a
	// service one.
	for _, step := range [][2]string{{"POST", "/auth/start"}, {"GET", "/auth/status"}} {
		rec := do(h, step[0], step[1], "", []*http.Cookie{service}, "Sec-Fetch-Site", "same-origin")
		if c := cookie(rec, "__Host-mcmap_session"); c != nil {
			t.Errorf("%s %s set a session cookie for a service session", step[0], step[1])
		}
	}
	// The internal API is the agent's, and a session is not its token.
	for _, path := range []string{"/internal/v1/claims", "/internal/v1/revocations", "/internal/v1/world/acknowledge"} {
		rec := do(s.InternalHandler(), "POST", path, `{}`, []*http.Cookie{service}, "Authorization", "Bearer "+serviceSecret)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
			t.Errorf("POST %s with the service secret = %d, want 401", path, rec.Code)
		}
	}
}

func TestService_TheExchangeIsOnTheInternalListenerOnly(t *testing.T) {
	s, _, _ := withService(t)
	rec := do(s.Handler(), "POST", "/internal/v1/service-sessions", "", nil, "Authorization", "Bearer "+serviceSecret, "Sec-Fetch-Site", "same-origin")
	if rec.Code < 400 || cookie(rec, "__Host-mcmap_session") != nil {
		t.Errorf("the public listener answered the exchange with %d and cookie %v", rec.Code, cookie(rec, "__Host-mcmap_session"))
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		rec := do(s.InternalHandler(), method, "/internal/v1/service-sessions", "", nil, "Authorization", "Bearer "+serviceSecret)
		if rec.Code != http.StatusMethodNotAllowed || cookie(rec, "__Host-mcmap_session") != nil {
			t.Errorf("%s on the exchange = %d", method, rec.Code)
		}
	}
}

func TestService_OffUnlessConfigured(t *testing.T) {
	for name, strip := range map[string]func(*Server){
		"no exchange":        func(s *Server) { s.Service = nil },
		"no service key":     func(s *Server) { s.Sessions.ServiceKey = nil },
		"no login":           func(s *Server) { s.Sessions = nil },
		"nothing set at all": func(s *Server) { s.Service, s.Sessions.ServiceKey = nil, nil },
	} {
		s, _, _ := withService(t)
		strip(s)
		if rec := exchange(s, serviceSecret); rec.Code != http.StatusNotFound {
			t.Errorf("%s: the exchange answered %d, want 404", name, rec.Code)
		}
	}
	// A cookie from when it was on is worth nothing once it is off.
	s, _, _ := withService(t)
	held := serviceSession(t, s)
	s.Service, s.Sessions.ServiceKey = nil, nil
	if rec := do(s.Handler(), "GET", "/api/map", "", []*http.Cookie{held}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a service session after the feature was switched off = %d, want 401", rec.Code)
	}
}

func TestService_ASessionIsShortAndASecretChangeEndsIt(t *testing.T) {
	s, _, _ := withService(t)
	now := time.Now()
	s.Sessions.Now = func() time.Time { return now }
	rec := exchange(s, serviceSecret)
	var out serviceSessionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	held := cookie(rec, "__Host-mcmap_session")
	if out.Identity != auth.ServiceName || out.TTLSeconds != 600 || held.MaxAge != 600 || !out.ExpiresAt.Equal(now.Add(10*time.Minute).Truncate(time.Second)) {
		t.Errorf("exchange answered %+v with a cookie of MaxAge %d", out, held.MaxAge)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the exchange's answer may be cached: %q", rec.Header().Get("Cache-Control"))
	}
	if code := do(s.Handler(), "GET", "/api/map", "", []*http.Cookie{held}).Code; code != http.StatusOK {
		t.Fatalf("a fresh service session = %d", code)
	}
	// Rotating the Secret is the revocation.
	s.Sessions.ServiceKey = auth.ServiceKey(s.Sessions.Key, serviceSecret+"-rotated")
	if code := do(s.Handler(), "GET", "/api/map", "", []*http.Cookie{held}).Code; code != http.StatusUnauthorized {
		t.Errorf("a service session after the secret changed = %d, want 401", code)
	}
	s.Sessions.ServiceKey = auth.ServiceKey(s.Sessions.Key, serviceSecret)
	now = now.Add(10 * time.Minute)
	if code := do(s.Handler(), "GET", "/api/map", "", []*http.Cookie{held}).Code; code != http.StatusUnauthorized {
		t.Errorf("a service session after its ten minutes = %d, want 401", code)
	}
}

// A live stream opened by a service session ends with the session.
func TestService_ALiveStreamEndsWithTheSession(t *testing.T) {
	s, _, _ := withService(t)
	s.Sessions.ServiceTTL = time.Second
	srv := serve(t, s)
	st := open(t, srv, "/api/live?dimension=overworld", serviceSession(t, s))
	st.next(t)
	select {
	case <-st.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived the session it was opened with")
	}
}

func TestService_EveryExchangeIsLoggedAndCountedAndTheSecretIsInNeither(t *testing.T) {
	s, _, log := withService(t)
	count := func(result, source string) float64 {
		return testutil.ToFloat64(metricServiceExchanges.WithLabelValues(result, source))
	}
	// httptest requests come from 192.0.2.1, a documentation address.
	okBefore, deniedBefore, malformedBefore := count(auth.ExchangeOK, sourceOther), count(auth.ExchangeDenied, sourceOther), count(exchangeMalformed, sourceOther)

	wrong := "a-wrong-secret-that-must-not-be-logged-000"
	if rec := exchange(s, wrong); rec.Code != http.StatusUnauthorized || cookie(rec, "__Host-mcmap_session") != nil || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("a wrong secret = %d", rec.Code)
	}
	if rec := do(s.InternalHandler(), "POST", "/internal/v1/service-sessions", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no secret at all = %d, want 401", rec.Code)
	}
	// In the address is where a secret gets written down along the way.
	if rec := do(s.InternalHandler(), "POST", "/internal/v1/service-sessions?secret="+serviceSecret, "", nil); rec.Code != http.StatusBadRequest || cookie(rec, "__Host-mcmap_session") != nil {
		t.Errorf("the secret in the address = %d, want 400 and no session", rec.Code)
	}
	if rec := do(s.InternalHandler(), "POST", "/internal/v1/service-sessions?x=1", "", nil, "Authorization", "Bearer "+serviceSecret); rec.Code != http.StatusBadRequest {
		t.Errorf("a query beside the right secret = %d, want 400", rec.Code)
	}
	held := serviceSession(t, s)

	if got := count(auth.ExchangeOK, sourceOther) - okBefore; got != 1 {
		t.Errorf("ok counted %v times, want 1", got)
	}
	if got := count(auth.ExchangeDenied, sourceOther) - deniedBefore; got != 2 {
		t.Errorf("denied counted %v times, want 2", got)
	}
	if got := count(exchangeMalformed, sourceOther) - malformedBefore; got != 2 {
		t.Errorf("malformed counted %v times, want 2", got)
	}

	logged := log.String()
	if n := strings.Count(logged, `"msg":"map service session issued"`); n != 1 {
		t.Errorf("%d issue lines, want 1:\n%s", n, logged)
	}
	if n := strings.Count(logged, `"msg":"map service session refused"`); n != 4 {
		t.Errorf("%d refusal lines, want 4:\n%s", n, logged)
	}
	for _, want := range []string{`"identity":"service:e2e"`, `"source":"other"`, `"result":"denied"`, `"result":"malformed"`} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log has no %s:\n%s", want, logged)
		}
	}
	for name, secret := range map[string]string{"the secret": serviceSecret, "the wrong secret": wrong, "the session": held.Value} {
		if strings.Contains(logged, secret) {
			t.Errorf("%s is in the log", name)
		}
	}
}

// A caller chooses its user agent, and it is logged: it gets one field of
// one line, however it is written.
func TestService_WhatACallerSaysOfItselfCannotForgeALogLine(t *testing.T) {
	s, _, log := withService(t)
	forged := "checks\n{\"msg\":\"map login issued\",\"xuid\":\"1\"}\r" + strings.Repeat("x", 500)
	rec := do(s.InternalHandler(), "POST", "/internal/v1/service-sessions", "", nil, "Authorization", "Bearer "+serviceSecret, "User-Agent", forged)
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange = %d", rec.Code)
	}
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d log lines for one exchange:\n%s", len(lines), log)
	}
	var line struct {
		Msg   string `json:"msg"`
		Agent string `json:"user_agent"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	if line.Msg != "map service session issued" || strings.ContainsAny(line.Agent, "\r\n") || len(line.Agent) != 200 {
		t.Errorf("logged %q with a user agent of %d characters", line.Msg, len(line.Agent))
	}
}

// Anything in the cluster can reach the exchange. A flood of wrong secrets
// is turned away, counted, written down only so often, and does not keep
// out the caller that holds the right one.
func TestService_AStormOfWrongSecretsNeitherLocksOutTheRightOneNorFloodsTheLog(t *testing.T) {
	s, _, log := withService(t)
	limited := testutil.ToFloat64(metricServiceExchanges.WithLabelValues(auth.ExchangeLimited, sourceOther))
	codes := map[int]int{}
	for range 200 {
		codes[exchange(s, "wrong").Code]++
	}
	if codes[http.StatusUnauthorized] != 5 || codes[http.StatusTooManyRequests] != 195 {
		t.Errorf("200 wrong secrets were answered %v, want 5 judged wrong and the rest turned away", codes)
	}
	rec := exchange(s, "wrong")
	if wait, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || wait < 1 || wait > 61 {
		t.Errorf("Retry-After = %q, want within a minute", rec.Header().Get("Retry-After"))
	}
	if got := testutil.ToFloat64(metricServiceExchanges.WithLabelValues(auth.ExchangeLimited, sourceOther)) - limited; got != 196 {
		t.Errorf("%v refusals past the budget were counted, want every one of 196", got)
	}
	if lines := strings.Count(log.String(), "map service session refused"); lines != 6 {
		t.Errorf("%d refusals were logged, want the 5 judged and one for the flood", lines)
	}
	held := serviceSession(t, s)
	if code := do(s.Handler(), "GET", "/api/map", "", []*http.Cookie{held}).Code; code != http.StatusOK {
		t.Errorf("the session issued through the storm = %d", code)
	}
}

func TestService_WhereAnExchangeCameFrom(t *testing.T) {
	for remote, want := range map[string]string{
		"127.0.0.1:4000":   sourceLoopback,
		"[::1]:4000":       sourceLoopback,
		"10.244.3.17:4000": sourcePrivate,
		"192.168.1.9:4000": sourcePrivate,
		"[fd00::1]:4000":   sourcePrivate,
		"203.0.113.9:4000": sourceOther,
		"not an address":   sourceOther,
		"":                 sourceOther,
	} {
		if got := exchangeSource(remote); got != want {
			t.Errorf("exchangeSource(%q) = %s, want %s", remote, got, want)
		}
	}
}
