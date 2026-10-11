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

// How a route is gated. public needs no session; shown is every player's
// and a service session's to read; gated is a player's alone.
const (
	routePublic = "public"
	routeShown  = "shown"
	routeGated  = "gated"
)

// Every route of the public listener: its gate, and an address that
// reaches its handler. A new route has to be added here, with its gate
// thought about, before this file passes.
var routes = map[string]struct{ gate, path string }{
	"GET /healthz":                          {routePublic, "/healthz"},
	"GET /api/config":                       {routePublic, "/api/config"},
	"GET /":                                 {routePublic, "/"},
	"POST /auth/start":                      {routePublic, ""},
	"GET /auth/status":                      {routePublic, "/auth/status"},
	"POST /auth/logout":                     {routePublic, ""},
	"GET /api/map":                          {routeShown, "/api/map"},
	"GET /tiles/{dimension}/{zoom}/{x}/{y}": {routeShown, "/tiles/overworld/0/37/-4.webp"},
	"GET /api/live":                         {routeShown, ""},
	"GET /api/markers":                      {routeShown, "/api/markers?dimension=overworld"},
	"GET /api/icons":                        {routeShown, "/api/icons"},
	"GET /api/icons/picture/{group}/{name}": {routeShown, "/api/icons/picture/container/chest"},
	"GET /api/names":                        {routeShown, "/api/names"},
	"GET /api/icons/mob/{type}":             {routeShown, "/api/icons/mob/cow"},
	"GET /api/icons/head":                   {routeShown, "/api/icons/head?name=nobody"},
	"GET /api/structures":                   {routeShown, "/api/structures?dimension=overworld"},
	"GET /api/structures/detail":            {routeShown, villageDetailPath},
	"GET /api/biomes":                       {routeShown, "/api/biomes?dimension=overworld"},
	"GET /api/biomes/at":                    {routeShown, "/api/biomes/at?dimension=overworld&x=0&z=0"},
	"GET /api/biomes/nearest":               {routeShown, "/api/biomes/nearest?dimension=overworld&biome=plains&x=0&z=0"},
	"GET /api/biomes/region":                {routeShown, "/api/biomes/region?dimension=overworld&x=0&z=0"},
	"GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}": {routeShown, "/api/biomes/tiles/overworld/0/0/0.png"},
	"GET /api/trails":    {routeShown, "/api/trails?dimension=overworld"},
	"GET /api/search":    {routeShown, "/api/search?dimension=overworld&x=0&z=0&q=a"},
	"GET /api/me":        {routeShown, "/api/me"},
	"GET /api/waypoints": {routeGated, "/api/waypoints"},
}

// routesInSource reads the public handler's routes, and the gate each is
// registered behind, out of the source: a mux does not list its own.
func routesInSource(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Handler" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Errorf("a route is registered under something other than a literal pattern")
				return true
			}
			pattern, _ := strconv.Unquote(lit.Value)
			gate := routePublic
			if wrap, ok := call.Args[1].(*ast.CallExpr); ok {
				if by, ok := wrap.Fun.(*ast.SelectorExpr); ok && (by.Sel.Name == routeShown || by.Sel.Name == routeGated) {
					gate = by.Sel.Name
				}
			}
			found[pattern] = gate
			return true
		})
	}
	if len(found) == 0 {
		t.Fatal("no routes found in server.go")
	}
	return found
}

func TestService_EveryRouteHasAGateThatWasChosenForIt(t *testing.T) {
	found := routesInSource(t)
	for pattern, gate := range found {
		want, listed := routes[pattern]
		switch {
		case !listed:
			t.Errorf("%s is not in this test's list. Decide whether a service session may read it (shown) or not (gated), which it may not if it is one player's own or changes anything, and add it", pattern)
		case want.gate != gate:
			t.Errorf("%s is behind %s in server.go and %s here", pattern, gate, want.gate)
		}
		// The invariant the roles to come lean on: nothing that is not a
		// plain read is ever open to a service session.
		if gate == routeShown && !strings.HasPrefix(pattern, "GET ") {
			t.Errorf("%s is shown to a service session and is not a GET", pattern)
		}
	}
	for pattern := range routes {
		if _, ok := found[pattern]; !ok {
			t.Errorf("%s is listed here and is not a route any more", pattern)
		}
	}
}

func TestService_ASessionReachesWhatIsShownAndIsForbiddenTheRest(t *testing.T) {
	s, waypoints, _ := withService(t)
	h := s.Handler()
	service, player := serviceSession(t, s), session(s, steve)
	allowed, forbidden := testutil.ToFloat64(metricServiceRequests.WithLabelValues(serviceAllowed)), testutil.ToFloat64(metricServiceRequests.WithLabelValues(serviceForbidden))
	var wantAllowed, wantForbidden float64
	for pattern, route := range routes {
		if route.path == "" {
			continue
		}
		asService := do(h, "GET", route.path, "", []*http.Cookie{service})
		asPlayer := do(h, "GET", route.path, "", []*http.Cookie{player})
		// A player is turned away from nothing by any of this.
		if asPlayer.Code == http.StatusForbidden || asPlayer.Code == http.StatusUnauthorized {
			t.Errorf("%s as a player = %d", pattern, asPlayer.Code)
		}
		switch route.gate {
		case routeGated:
			wantForbidden++
			if asService.Code != http.StatusForbidden {
				t.Errorf("%s as a service session = %d, want 403", pattern, asService.Code)
			}
		case routeShown:
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

// The invariant for the roles to come: a service session cannot do
// anything but read, on any route, and cannot be turned into a player's.
func TestService_ASessionOnlyEverReads(t *testing.T) {
	s, _, _ := withService(t)
	h := s.Handler()
	service := serviceSession(t, s)
	for pattern, route := range routes {
		if route.path == "" || route.gate == routePublic {
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

func TestService_WrongSecretsLockTheExchange(t *testing.T) {
	s, _, _ := withService(t)
	for range 5 {
		if rec := exchange(s, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("a wrong secret = %d, want 401", rec.Code)
		}
	}
	rec := exchange(s, serviceSecret)
	if rec.Code != http.StatusTooManyRequests || cookie(rec, "__Host-mcmap_session") != nil {
		t.Errorf("the right secret after five wrong ones = %d, want 429 and no session", rec.Code)
	}
	if wait, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || wait < 200 || wait > 301 {
		t.Errorf("Retry-After = %q, want about five minutes", rec.Header().Get("Retry-After"))
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
