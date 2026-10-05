package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

const hostile = `<img src=x onerror="alert(1)"></span><script>alert(2)</script>`

var alex = auth.Identity{XUID: "2535400000000002", Gamertag: "Alex"}

// fakeWaypoints holds each player's waypoints by XUID, as the agent does,
// and remembers who was asked for.
type fakeWaypoints struct {
	mu    sync.Mutex
	by    map[string][]markers.Waypoint
	asked []string
	err   error
}

func (f *fakeWaypoints) Waypoints(_ context.Context, xuid string) ([]markers.Waypoint, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, xuid)
	return f.by[xuid], 0, f.err
}

func withMarkers(t *testing.T) (*Server, *fakeWaypoints) {
	t.Helper()
	s := withLogin(t)
	store := markers.NewStore()
	store.Set(time.Date(2026, 10, 5, 4, 1, 1, 0, time.UTC), markers.World{
		chunks.Overworld: {
			Beds:       []markers.Marker{{X: 3, Y: 64, Z: 5}},
			Containers: []markers.Marker{{X: 8, Y: 70, Z: 9, Kind: "chest", Name: hostile}},
			Mobs:       []markers.Marker{{X: 173, Y: 66, Z: 263, Kind: "cat", Name: "OJ"}},
		},
		chunks.Nether: {Beds: []markers.Marker{{X: -1, Y: 2, Z: -3}}},
	})
	s.Markers = store
	waypoints := &fakeWaypoints{by: map[string][]markers.Waypoint{
		steve.XUID: {{Name: "steve's base", X: 100, Y: 64, Z: -200, Dimension: "overworld"}},
		alex.XUID:  {{Name: "alex's vault", X: -7000, Y: 12, Z: 7000, Dimension: "nether"}},
	}}
	s.Waypoints = waypoints
	return s, waypoints
}

func TestMarkers_NeedASession(t *testing.T) {
	s, waypoints := withMarkers(t)
	h := s.Handler()
	forged := &http.Cookie{Name: "__Host-mcmap_session", Value: "e30.nope"}
	for _, path := range []string{"/api/markers?dimension=overworld", "/api/waypoints"} {
		for name, cookies := range map[string][]*http.Cookie{"no session": nil, "a forged session": {forged}} {
			rec := do(h, "GET", path, "", cookies)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s with %s = %d, want 401", path, name, rec.Code)
			}
			if body := rec.Body.String(); strings.Contains(body, "beds") || strings.Contains(body, "base") {
				t.Errorf("GET %s with %s: the refusal carries data: %s", path, name, body)
			}
		}
	}
	if len(waypoints.asked) != 0 {
		t.Errorf("the agent was asked for %v on behalf of nobody", waypoints.asked)
	}
}

func TestMarkers_ServeOneDimensionToAnyLoggedInPlayer(t *testing.T) {
	s, _ := withMarkers(t)
	h := s.Handler()
	type answer struct {
		At         time.Time        `json:"at"`
		Beds       []markers.Marker `json:"beds"`
		Containers []markers.Marker `json:"containers"`
		Mobs       []markers.Marker `json:"mobs"`
	}
	for _, id := range []auth.Identity{steve, alex} {
		rec := do(h, "GET", "/api/markers?dimension=overworld", "", []*http.Cookie{session(s, id)})
		var got answer
		if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("%s: %d %v", id.Gamertag, rec.Code, err)
		}
		if len(got.Beds) != 1 || got.Beds[0].X != 3 || len(got.Containers) != 1 || len(got.Mobs) != 1 || got.Mobs[0].Name != "OJ" || got.At.IsZero() {
			t.Errorf("%s: overworld = %+v", id.Gamertag, got)
		}
	}
	rec := do(h, "GET", "/api/markers?dimension=nether", "", []*http.Cookie{session(s, steve)})
	var nether answer
	_ = json.Unmarshal(rec.Body.Bytes(), &nether)
	if len(nether.Beds) != 1 || nether.Beds[0].X != -1 || len(nether.Containers) != 0 {
		t.Errorf("nether = %+v", nether)
	}
	for _, query := range []string{"", "?dimension=", "?dimension=aether", "?dimension=../overworld"} {
		if rec := do(h, "GET", "/api/markers"+query, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/markers%s = %d, want 400", query, rec.Code)
		}
	}
}

// A name from the world is whatever a player typed on an anvil. It must
// reach the browser as a JSON string in a response nothing will read as a
// page, with the characters that could end a script block escaped.
func TestMarkers_SendAPlayersTextAsDataOnly(t *testing.T) {
	s, _ := withMarkers(t)
	rec := do(s.Handler(), "GET", "/api/markers?dimension=overworld", "", []*http.Cookie{session(s, steve)})
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the response may be sniffed into another type")
	}
	if body := rec.Body.String(); strings.ContainsAny(body, "<>") {
		t.Errorf("the body carries markup characters unescaped: %s", body)
	}
	var got struct {
		Containers []markers.Marker `json:"containers"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Containers) != 1 || got.Containers[0].Name != hostile {
		t.Errorf("the name did not survive as text: %+v", got.Containers)
	}
}

func TestMarkers_AreNotSentAgainUnchanged(t *testing.T) {
	s, _ := withMarkers(t)
	h, cookies := s.Handler(), []*http.Cookie{session(s, steve)}
	first := do(h, "GET", "/api/markers?dimension=overworld", "", cookies)
	etag := first.Header().Get("ETag")
	if etag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("ETag %q, Cache-Control %q", etag, first.Header().Get("Cache-Control"))
	}
	again := do(h, "GET", "/api/markers?dimension=overworld", "", cookies, "If-None-Match", etag)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("unchanged = %d with %d bytes, want 304 and none", again.Code, again.Body.Len())
	}
	// A tag is not a way round the gate.
	if rec := do(h, "GET", "/api/markers?dimension=overworld", "", nil, "If-None-Match", etag); rec.Code != http.StatusUnauthorized {
		t.Errorf("with the tag and no session = %d, want 401", rec.Code)
	}
}

// Waypoints are base locations. A player sees their own and has no way to
// name anyone else's.
func TestWaypoints_AreOnlyEverTheSessionsOwn(t *testing.T) {
	s, waypoints := withMarkers(t)
	h := s.Handler()
	names := func(body []byte) []string {
		var got waypointsJSON
		_ = json.Unmarshal(body, &got)
		var out []string
		for _, w := range got.Waypoints {
			out = append(out, w.Name)
		}
		return out
	}
	for _, c := range []struct {
		who  auth.Identity
		path string
		want string
	}{
		{steve, "/api/waypoints", "steve's base"},
		{alex, "/api/waypoints", "alex's vault"},
		// Every way a request might try to say whose it wants.
		{steve, "/api/waypoints?xuid=" + alex.XUID, "steve's base"},
		{steve, "/api/waypoints?x=" + alex.XUID + "&player=" + alex.XUID + "&gamertag=Alex", "steve's base"},
	} {
		rec := do(h, "GET", c.path, "", []*http.Cookie{session(s, c.who)}, "X-XUID", alex.XUID, "X-Forwarded-User", alex.XUID)
		if got := names(rec.Body.Bytes()); rec.Code != http.StatusOK || len(got) != 1 || got[0] != c.want {
			t.Errorf("%s asking %s = %d %v, want only %q", c.who.Gamertag, c.path, rec.Code, got, c.want)
		}
		if strings.Contains(rec.Body.String(), "alex") != (c.who == alex) {
			t.Errorf("%s asking %s was sent: %s", c.who.Gamertag, c.path, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("a player's waypoints may be cached: %q", rec.Header().Get("Cache-Control"))
		}
	}
	if want := []string{steve.XUID, alex.XUID, steve.XUID, steve.XUID}; strings.Join(waypoints.asked, ",") != strings.Join(want, ",") {
		t.Errorf("the agent was asked for %v, want %v", waypoints.asked, want)
	}
	// No path under it either: the XUID is never part of the address.
	if rec := do(h, "GET", "/api/waypoints/"+alex.XUID, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/waypoints/<xuid> = %d, want 404", rec.Code)
	}
}

func TestWaypoints_SayNothingOfWhyTheyFailed(t *testing.T) {
	s, waypoints := withMarkers(t)
	h, cookies := s.Handler(), []*http.Cookie{session(s, steve)}
	waypoints.err = errors.New(`Get "http://agent.internal:8080/v1/players/1/waypoints": connection refused`)
	rec := do(h, "GET", "/api/waypoints", "", cookies)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "agent.internal") {
		t.Errorf("a failed read = %d %q", rec.Code, rec.Body)
	}
	waypoints.err = markers.ErrBusy
	if rec := do(h, "GET", "/api/waypoints", "", cookies); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("a busy read = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	waypoints.err, waypoints.by = nil, nil
	if rec := do(h, "GET", "/api/waypoints", "", cookies); strings.TrimSpace(rec.Body.String()) != `{"waypoints":[],"more":0}` {
		t.Errorf("a player with none = %s", rec.Body)
	}
}

func TestMarkers_RoutesAreAbsentWhenThereIsNothingBehindThem(t *testing.T) {
	s := withLogin(t)
	h, cookies := s.Handler(), []*http.Cookie{session(s, steve)}
	for _, path := range []string{"/api/markers?dimension=overworld", "/api/waypoints"} {
		if rec := do(h, "GET", path, "", cookies); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	// With no login the world's markers are as open as the map is, but
	// there is no player to have waypoints.
	open, waypoints := withMarkers(t)
	open.Sessions, open.Codes = nil, nil
	h = open.Handler()
	if rec := do(h, "GET", "/api/markers?dimension=overworld", "", nil); rec.Code != http.StatusOK {
		t.Errorf("markers with no login = %d, want 200", rec.Code)
	}
	if rec := do(h, "GET", "/api/waypoints", "", nil); rec.Code != http.StatusNotFound || len(waypoints.asked) != 0 {
		t.Errorf("waypoints with no login = %d, agent asked for %v", rec.Code, waypoints.asked)
	}
}
