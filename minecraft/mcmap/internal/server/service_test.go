package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/players"
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
	s.Players = &players.Store{}

	s.Sessions.ServiceKey = auth.ServiceKey(s.Sessions.Key, serviceSecret)
	s.Sessions.ServiceTTL = 10 * time.Minute
	s.Sessions.ServiceSeen = CountServiceRequest
	s.Service = auth.NewExchange(serviceSecret, time.Now)
	s.Admin, s.Operators = true, fakeOperators{}
	withPasskeys(t, s)
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

// Every route of both listeners, by listener and pattern: the gate it is
// behind, and for the public listener's an address that reaches its
// handler. A new route has to be added here, with its gate thought about,
// before this file passes.
var routes = map[string]struct{ gate, path string }{
	"public GET /healthz":                                         {"gatePublic", "/healthz"},
	"public GET /api/config":                                      {"gatePublic", "/api/config"},
	"public GET /":                                                {"gatePublic", "/"},
	"public POST /auth/start":                                     {"gatePublic", ""},
	"public GET /auth/status":                                     {"gatePublic", "/auth/status"},
	"public POST /auth/logout":                                    {"gatePublic", ""},
	"public POST /auth/passkey/options":                           {"gatePublic", ""},
	"public POST /auth/passkey/finish":                            {"gatePublic", ""},
	"public POST /auth/passkey/register/options":                  {"gateAdmin", "/auth/passkey/register/options"},
	"public POST /auth/passkey/register/finish":                   {"gateAdmin", "/auth/passkey/register/finish"},
	"public POST /auth/passkey/register/confirm":                  {"gateAdmin", "/auth/passkey/register/confirm"},
	"public POST /auth/passkey/register/cancel":                   {"gateAdmin", "/auth/passkey/register/cancel"},
	"public GET /api/map":                                         {"gateViewer", "/api/map"},
	"public GET /api/diagnostics":                                 {"gateViewer", "/api/diagnostics"},
	"public GET /tiles/{dimension}/{zoom}/{x}/{y}":                {"gateViewer", "/tiles/overworld/0/37/-4.webp"},
	"public GET /api/live":                                        {"gateViewer", ""},
	"public GET /api/markers":                                     {"gateViewer", "/api/markers?dimension=overworld"},
	"public GET /api/icons":                                       {"gateViewer", "/api/icons"},
	"public GET /api/icons/picture/{group}/{name}":                {"gateViewer", "/api/icons/picture/container/chest"},
	"public GET /api/names":                                       {"gateViewer", "/api/names"},
	"public GET /api/icons/mob/{type}":                            {"gateViewer", "/api/icons/mob/cow"},
	"public GET /api/icons/head":                                  {"gateViewer", "/api/icons/head?name=nobody"},
	"public GET /api/structures":                                  {"gateViewer", "/api/structures?dimension=overworld"},
	"public GET /api/structures/detail":                           {"gateViewer", villageDetailPath},
	"public GET /api/biomes":                                      {"gateViewer", "/api/biomes?dimension=overworld"},
	"public GET /api/biomes/at":                                   {"gateViewer", "/api/biomes/at?dimension=overworld&x=0&z=0"},
	"public GET /api/biomes/nearest":                              {"gateViewer", "/api/biomes/nearest?dimension=overworld&biome=plains&x=0&z=0"},
	"public GET /api/biomes/region":                               {"gateViewer", "/api/biomes/region?dimension=overworld&x=0&z=0"},
	"public GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}":     {"gateViewer", "/api/biomes/tiles/overworld/0/0/0.png"},
	"public GET /api/topography":                                  {"gateViewer", "/api/topography?dimension=overworld"},
	"public GET /api/topography/tiles/{dimension}/{zoom}/{x}/{y}": {"gateViewer", "/api/topography/tiles/overworld/0/0/0.png?styles=tint"},
	"public GET /api/trails":                                      {"gateViewer", "/api/trails?dimension=overworld"},
	// The map as it was on each day, which is every player's to look at.
	"public GET /api/history": {"gateViewer", ""},
	"public GET /api/history/tiles/{day}/{dimension}/{zoom}/{x}/{y}": {"gateViewer", ""},
	"public GET /api/history/markers/{day}":                          {"gateViewer", ""},
	"public GET /api/history/changes/{day}":                          {"gateViewer", ""},
	"public GET /api/players/{id}":                                   {"gateViewer", "/api/players/42"},
	"public GET /api/search":                                         {"gateViewer", "/api/search?dimension=overworld&x=0&z=0&q=a"},
	"public GET /api/me":                                             {"gateViewer", "/api/me"},
	"public GET /api/admin/status":                                   {"gateAdmin", "/api/admin/status"},
	"public GET /api/waypoints":                                      {"gatePlayer", "/api/waypoints"},
	// Registered only with passkeys on, as they are in withService.
	"public GET /api/passkeys":         {"gatePlayer", "/api/passkeys"},
	"public DELETE /api/passkeys/{id}": {"gatePlayer", "/api/passkeys/none"},
	"public POST /api/logout-all":      {"gatePlayer", "/api/logout-all"},
	// The internal listener's. Its liveness route is the public one's twin.
	"internal GET /healthz":                        {"gatePublic", ""},
	"internal GET /metrics":                        {"gatePublic", ""},
	"internal GET /internal/v1/status":             {"gatePublic", ""},
	"internal GET /internal/v1/world":              {"gateAgent", ""},
	"internal POST /internal/v1/world/acknowledge": {"gateAgent", ""},
	"internal PUT /internal/v1/heads":              {"gateAgent", ""},
	"internal POST /internal/v1/claims":            {"gateAgent", ""},
	"internal POST /internal/v1/revocations":       {"gateAgent", ""},
	"internal POST /internal/v1/service-sessions":  {"gateExchange", ""},
	"internal POST /internal/v1/passkey-codes":     {"gateAgent", ""},
}

// The two functions that may make a mux, and the listener each makes it for.
var listeners = map[string]string{"Handler": "public", "InternalHandler": "internal"}

// routesIn reads every route registered by the Go files of dir that are not
// tests, with the gate each is behind, keyed by listener and pattern: a mux
// does not list its own. Everything that could put a handler on a mux some
// other way is a problem it reports: a registration made directly, held as
// a method value or made in a package-level variable, a route registered
// outside the two functions that build a listener, and a mux made anywhere
// else. server is whether dir is this package, which may have routes; any
// other may have none of this at all.
func routesIn(dir string, server bool) (found map[string]string, problems []string, err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, err
	}
	found = map[string]string{}
	set := token.NewFileSet()
	problem := func(at token.Pos, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s: %s", set.Position(at), fmt.Sprintf(format, args...)))
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			return nil, nil, err
		}
		// Every declaration, not only functions: a variable's initialiser
		// runs too. within is the function a node is in, or "" for none.
		for _, decl := range file.Decls {
			within := ""
			if fn, ok := decl.(*ast.FuncDecl); ok {
				within = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					switch node.Sel.Name {
					// Wherever it appears and whatever it is on: called,
					// or only named to be called later, on a mux by any
					// name or on the default one.
					case "Handle", "HandleFunc":
						if !server || within != "route" {
							problem(node.Pos(), "a handler is registered with %s directly; every route goes through route, with its gate", node.Sel.Name)
						}
					case "NewServeMux", "DefaultServeMux":
						if !server || listeners[within] == "" {
							problem(node.Pos(), "a mux is made or used outside the two functions that build a listener")
						}
					}
				case *ast.CallExpr:
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "route" {
						return true
					}
					listener := listeners[within]
					if !server || listener == "" {
						problem(node.Pos(), "a route is registered outside the two functions that build a listener")
						return true
					}
					if len(node.Args) != 4 {
						problem(node.Pos(), "route called with %d arguments", len(node.Args))
						return true
					}
					behind, isName := node.Args[1].(*ast.Ident)
					pattern, isLiteral := node.Args[2].(*ast.BasicLit)
					if !isName || !isLiteral || !strings.HasPrefix(behind.Name, "gate") {
						problem(node.Pos(), "a route's gate and pattern are written out where it is registered, not worked out")
						return true
					}
					text, _ := strconv.Unquote(pattern.Value)
					key := listener + " " + text
					if was, twice := found[key]; twice && was != behind.Name {
						problem(node.Pos(), "%s is registered behind %s and behind %s", key, was, behind.Name)
					}
					found[key] = behind.Name
				}
				return true
			})
		}
	}
	return found, problems, nil
}

func TestService_EveryRouteHasAGateThatWasChosenForIt(t *testing.T) {
	found, problems, err := routesIn(".", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
	if len(found) < 30 {
		t.Fatalf("only %d routes found in the package's source", len(found))
	}
	for key, gate := range found {
		want, listed := routes[key]
		switch {
		case !listed:
			t.Errorf("%s is not in this test's list. Decide whether a service session may read it (gateViewer) or not (gatePlayer), which it may not if it is one player's own or changes anything, and add it", key)
		case want.gate != gate:
			t.Errorf("%s is behind %s in the source and %s here", key, gate, want.gate)
		}
		listener, pattern, _ := strings.Cut(key, " ")
		// The invariant the roles to come lean on: nothing that is not a
		// plain read is ever open to a service session.
		if gate == "gateViewer" && !strings.HasPrefix(pattern, "GET ") {
			t.Errorf("%s is shown to a service session and is not a GET", key)
		}
		// And each kind of gate belongs to one listener.
		if (gate == "gateViewer" || gate == "gatePlayer" || gate == "gateAdmin") != (listener == "public") && gate != "gatePublic" {
			t.Errorf("%s is behind %s, which is not a gate of the %s listener", key, gate, listener)
		}
	}
	for key := range routes {
		if _, ok := found[key]; !ok {
			t.Errorf("%s is listed here and is not a route any more", key)
		}
	}
	// What starts the service gives each handler to a listener and wraps
	// neither in a mux of its own, where a route would be out of sight of
	// all of the above.
	for _, dir := range []string{"../../cmd/mcmap", "../../e2e/harness"} {
		routed, problems, err := routesIn(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range problems {
			t.Error(problem)
		}
		if len(routed) != 0 {
			t.Errorf("%s registers routes of its own: %v", dir, routed)
		}
	}
}

// The reader has to see what it is there to catch, so it is run, the same
// function, over source that does each thing it must not miss.
func TestService_ARouteRegisteredAnotherWayIsSeen(t *testing.T) {
	for name, source := range map[string]struct {
		code   string
		server bool
		want   string
	}{
		"called on a mux in a helper":       {"func (s *Server) more(m *http.ServeMux) { m.HandleFunc(\"POST /api/reset\", s.handleMe) }", true, "registered with HandleFunc directly"},
		"called on a mux by another name":   {"func (s *Server) more() { s.side.Handle(\"GET /x\", nil) }", true, "registered with Handle directly"},
		"held as a method value":            {"func (s *Server) more(m *http.ServeMux) { add := m.HandleFunc; add(\"POST /api/reset\", s.handleMe) }", true, "registered with HandleFunc directly"},
		"in a package-level variable":       {"var _ = func() bool { http.HandleFunc(\"/api/reset\", nil); return true }()", true, "registered with HandleFunc directly"},
		"on the default mux":                {"func init() { http.Handle(\"/x\", nil) }", true, "registered with Handle directly"},
		"through route, outside a listener": {"func (s *Server) more(m *http.ServeMux) { s.route(m, gateViewer, \"POST /api/reset\", s.handleMe) }", true, "outside the two functions"},
		"with a gate that is worked out":    {"func (s *Server) Handler() { s.route(m, pick(), \"GET /x\", s.handleMe) }", true, "written out"},
		"with a pattern that is worked out": {"func (s *Server) Handler() { s.route(m, gateViewer, prefix+\"/x\", s.handleMe) }", true, "written out"},
		"a mux made in a helper":            {"func wrap(h http.Handler) http.Handler { m := http.NewServeMux(); return m }", true, "a mux is made"},
		"a mux wrapped round it at start":   {"func main() { m := http.NewServeMux(); m.Handle(\"/\", app.Handler()) }", false, "a mux is made"},
		"a handler registered at start":     {"func main() { http.HandleFunc(\"/debug\", nil) }", false, "registered with HandleFunc directly"},
		"a route registered at start":       {"func main() { app.route(nil, gatePublic, \"GET /x\", nil) }", false, "outside the two functions"},
		"twice behind two gates":            {"func (s *Server) Handler() { s.route(m, gateViewer, \"GET /x\", nil); s.route(m, gatePlayer, \"GET /x\", nil) }", true, "registered behind"},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "elsewhere.go"), []byte("package server\n"+source.code+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, problems, err := routesIn(dir, source.server)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, source.want) }) {
			t.Errorf("%s was not seen: %v", name, problems)
		}
	}
	// And what is allowed is not complained of.
	dir := t.TempDir()
	fine := "package server\nfunc (s *Server) Handler() { m := http.NewServeMux(); s.route(m, gateViewer, \"GET /x\", s.h) }\nfunc (s *Server) route(m *http.ServeMux, g gate, p string, h http.HandlerFunc) { m.Handle(p, h) }\n"
	if err := os.WriteFile(filepath.Join(dir, "fine.go"), []byte(fine), 0o600); err != nil {
		t.Fatal(err)
	}
	found, problems, err := routesIn(dir, true)
	if err != nil || len(problems) != 0 || found["public GET /x"] != "gateViewer" {
		t.Errorf("a route registered properly gave %v, %v, %v", found, problems, err)
	}
}

// What is on the internal listener is not on the public one, asked for
// there with everything that would open it where it lives.
func TestService_NothingOfTheInternalListenerAnswersOnThePublicOne(t *testing.T) {
	s, _, _ := withService(t)
	s.Chunks, s.Generations = damaged(), &fakeGenerations{}
	public := s.Handler()
	found, _, err := routesIn(".", true)
	if err != nil {
		t.Fatal(err)
	}
	asked := 0
	for key := range found {
		listener, pattern, _ := strings.Cut(key, " ")
		method, path, _ := strings.Cut(pattern, " ")
		if listener != "internal" || path == "/healthz" {
			continue
		}
		asked++
		for _, secret := range []string{internalToken, serviceSecret} {
			rec := do(public, method, path, `{}`, []*http.Cookie{session(s, steve)}, "Authorization", "Bearer "+secret, "Sec-Fetch-Site", "same-origin")
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s on the public listener = %d, want 404 or 405", method, path, rec.Code)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Errorf("%s %s on the public listener set a cookie", method, path)
			}
		}
	}
	if asked < 7 {
		t.Errorf("only %d internal routes were asked for on the public listener", asked)
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
		method := "GET"
		if route.gate == "gateAdmin" || route.gate == "gatePlayer" {
			// The method is the pattern's, and the request is from the
			// same site, so that it is the session that is refused and not
			// the request's origin.
			_, rest, _ := strings.Cut(pattern, " ")
			method, _, _ = strings.Cut(rest, " ")
		}
		asService := do(h, method, route.path, "", []*http.Cookie{service}, "Sec-Fetch-Site", "same-origin")
		// Logging out everywhere ends every other session the player holds,
		// so it is asked as someone nothing else here depends on.
		asWho := player
		if strings.HasSuffix(pattern, "/api/logout-all") {
			asWho = session(s, auth.Identity{XUID: "2535400000000099", Gamertag: "Disposable"})
		}
		asPlayer := do(h, method, route.path, "", []*http.Cookie{asWho}, "Sec-Fetch-Site", "same-origin")
		// A player is turned away from nothing by any of this.
		if route.gate != "gateAdmin" && (asPlayer.Code == http.StatusForbidden || asPlayer.Code == http.StatusUnauthorized) {
			t.Errorf("%s as a player = %d", pattern, asPlayer.Code)
		}
		switch route.gate {
		case "gateAdmin":
			// Steve is no operator here: both are turned away, and the
			// service session is turned away before the list is read.
			wantForbidden++
			if asService.Code != http.StatusForbidden || asPlayer.Code != http.StatusForbidden {
				t.Errorf("%s: service %d, member %d; want 403 for both", pattern, asService.Code, asPlayer.Code)
			}
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
		if route.path == "" || route.gate == "gateAdmin" {
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

func TestService_TheSecretIsTakenAsABearerTokenAndNoOtherWay(t *testing.T) {
	s, _, _ := withService(t)
	// The secret itself, not as a bearer token: a value in the wrong place
	// is a wrong value.
	for _, header := range []string{serviceSecret, "Basic " + serviceSecret, "bearer" + serviceSecret, "Token " + serviceSecret} {
		if rec := do(s.InternalHandler(), "POST", "/internal/v1/service-sessions", "", nil, "Authorization", header); rec.Code == http.StatusOK || cookie(rec, "__Host-mcmap_session") != nil {
			t.Errorf("the secret presented without the Bearer scheme was answered %d", rec.Code)
		}
	}
	if rec := exchange(s, serviceSecret); rec.Code != http.StatusOK {
		t.Errorf("the secret as a bearer token = %d", rec.Code)
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
