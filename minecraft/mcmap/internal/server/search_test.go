package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

type searchAnswer struct {
	Hits []struct {
		Kind      string   `json:"kind"`
		Name      string   `json:"name"`
		Detail    string   `json:"detail"`
		Colour    string   `json:"colour"`
		Trapped   bool     `json:"trapped"`
		Baby      bool     `json:"baby"`
		Dimension string   `json:"dimension"`
		X         int32    `json:"x"`
		Y         *int32   `json:"y"`
		Z         int32    `json:"z"`
		Distance  *float64 `json:"distance"`
	} `json:"hits"`
	More      int    `json:"more"`
	Waypoints string `json:"waypoints"`
}

// withEverything is a logged-in map holding biomes, structures, markers
// and two players' waypoints.
func withEverything(t *testing.T) (*Server, *fakeWaypoints) {
	t.Helper()
	s, waypoints := withMarkers(t)
	store := &biomes.Store{}
	store.Set(biomeWorld())
	s.Biomes = store
	s.Structures = surveyed()
	return s, waypoints
}

func search(t *testing.T, s *Server, query string, cookies ...*http.Cookie) searchAnswer {
	t.Helper()
	return decodeBody[searchAnswer](t, do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q="+url.QueryEscape(query), "", cookies))
}

func describe(a searchAnswer) string {
	var out []string
	for _, h := range a.Hits {
		out = append(out, fmt.Sprintf("%s:%s@%s", h.Kind, h.Name, h.Dimension))
	}
	return strings.Join(out, " ")
}

func TestSearchFindsABiomeAStructureAndAMarkerByName(t *testing.T) {
	s, _ := withEverything(t)
	me := session(s, steve)

	island := search(t, s, "Mushroom Fields", me)
	if len(island.Hits) != 1 {
		t.Fatalf("mushroom fields: %s", describe(island))
	}
	if h := island.Hits[0]; h.Kind != "biome" || h.Name != "Mushroom Fields" || h.Detail != "mushroom_island" || h.Dimension != "overworld" ||
		h.X != 960 || h.Z != 960 || h.Y != nil || h.Distance == nil || *h.Distance != 1358 {
		t.Errorf("mushroom fields = %+v", h)
	}
	// The game's own name for it finds it too.
	if got := search(t, s, "mushroom_island", me); describe(got) != "biome:Mushroom Fields@overworld" {
		t.Errorf("mushroom_island: %s", describe(got))
	}

	monument := search(t, s, "monument", me)
	if len(monument.Hits) != 1 {
		t.Fatalf("monument: %s", describe(monument))
	}
	// The middle of the recorded box, at its floor.
	if h := monument.Hits[0]; h.Kind != "structure" || h.Name != "Ocean Monument" || h.Detail != "monument" || h.X != 55 || h.Z != 5271 || h.Y == nil || *h.Y != 39 {
		t.Errorf("monument = %+v", h)
	}

	cat := search(t, s, "oj", me)
	if len(cat.Hits) != 1 {
		t.Fatalf("oj: %s", describe(cat))
	}
	if h := cat.Hits[0]; h.Kind != "mob" || h.Name != "OJ" || h.Detail != "cat" || h.X != 173 || h.Z != 263 || *h.Y != 66 {
		t.Errorf("the cat = %+v", h)
	}
	if got := search(t, s, "cat", me); describe(got) != "mob:OJ@overworld" {
		t.Errorf("a mob by its type: %s", describe(got))
	}
	if got := search(t, s, "spawn", me); describe(got) != "spawn:World Spawn@overworld" || got.Hits[0].X != 40 || got.Hits[0].Z != -72 {
		t.Errorf("the spawn: %s", describe(got))
	}
	if got := search(t, s, "chest", me); len(got.Hits) != 1 || got.Hits[0].Kind != "container" || got.Hits[0].Name != hostile {
		t.Errorf("a container by its kind: %s", describe(got))
	}
	if got := search(t, s, "zzz", me); got.Hits == nil || len(got.Hits) != 0 || got.More != 0 {
		t.Errorf("nothing matching = %+v", got)
	}
}

// The page calls a marker by the game's name for it, colour and all, and
// draws it by its colour, so a search has to find it under that name and
// pass on what the marker carried.
func TestSearchFindsMarkersByWhatThePageCallsThemAndSaysWhatTheyAre(t *testing.T) {
	s := withFetchedArt(t)
	store := markers.NewStore()
	store.Set(time.Now(), markers.World{chunks.Overworld: {
		Beds: []markers.Marker{{X: 1, Y: 64, Z: 1, Colour: "red"}, {X: 2, Y: 64, Z: 2}},
		Containers: []markers.Marker{
			{X: 3, Y: 64, Z: 3, Kind: "chest", Trapped: true}, {X: 4, Y: 64, Z: 4, Kind: "chest"},
			{X: 5, Y: 64, Z: 5, Kind: "shulker", Colour: "light_blue"}, {X: 6, Y: 64, Z: 6, Kind: "shulker", Colour: "undyed"},
		},
		Mobs: []markers.Marker{{X: 7, Y: 64, Z: 7, Kind: "villager_v2", Name: "Tesch", Baby: true}, {X: 8, Y: 64, Z: 8, Kind: "cow", Name: "Bess"}},
	}})
	s.Markers = store
	me := session(s, steve)

	if got := search(t, s, "red bed", me); len(got.Hits) != 1 || got.Hits[0].Name != "Red Bed" || got.Hits[0].Colour != "red" || got.Hits[0].X != 1 {
		t.Errorf("red bed: %+v", got.Hits)
	}
	if got := search(t, s, "bed", me); describe(got) != "bed:Red Bed@overworld bed:Bed@overworld" || got.Hits[1].Colour != "" {
		t.Errorf("bed: %s", describe(got))
	}
	if got := search(t, s, "trapped", me); len(got.Hits) != 1 || got.Hits[0].Name != "Trapped Chest" || got.Hits[0].Detail != "chest" || !got.Hits[0].Trapped {
		t.Errorf("trapped: %+v", got.Hits)
	}
	// The language file's own word for a chest, which the id is not.
	if got := search(t, s, "synthetic chest", me); len(got.Hits) != 1 || got.Hits[0].X != 4 || got.Hits[0].Trapped {
		t.Errorf("synthetic chest: %+v", got.Hits)
	}
	if got := search(t, s, "light blue", me); len(got.Hits) != 1 || got.Hits[0].Name != "Light Blue Shulker Box" || got.Hits[0].Detail != "shulker" || got.Hits[0].Colour != "light_blue" {
		t.Errorf("light blue: %+v", got.Hits)
	}
	if got := search(t, s, "shulker", me); describe(got) != "container:Light Blue Shulker Box@overworld container:Shulker Box@overworld" || got.Hits[1].Colour != "undyed" {
		t.Errorf("shulker: %s", describe(got))
	}
	if got := search(t, s, "synthetic villager", me); len(got.Hits) != 1 || got.Hits[0].Name != "Tesch" || got.Hits[0].Detail != "villager_v2" || !got.Hits[0].Baby {
		t.Errorf("a mob by the game's name for its type: %+v", got.Hits)
	}
	if got := search(t, s, "bess", me); len(got.Hits) != 1 || got.Hits[0].Baby {
		t.Errorf("a grown mob: %+v", got.Hits)
	}
}

// A kind without a name would be listed as a hit with nothing to read, so a
// kind added to the survey has to be named here before it can be found.
func TestSearchNamesEveryKindOfStructure(t *testing.T) {
	for _, kind := range structures.Kinds {
		if structureNames[kind] == "" {
			t.Errorf("%s has no name to be found by", kind)
		}
	}
	if len(structureNames) != len(structures.Kinds) {
		t.Errorf("%d names for %d kinds", len(structureNames), len(structures.Kinds))
	}
}

// Nearest first within the dimension asked from; then the other dimensions,
// which no distance is given for.
func TestSearchListsNearestFirstAndOtherDimensionsAfter(t *testing.T) {
	s, _ := withEverything(t)
	got := search(t, s, "e", session(s, steve))
	// Every kind of thing has an "e" in it somewhere.
	want := "bed:Bed@overworld biome:Desert@overworld container:" + hostile + "@overworld waypoint:steve's base@overworld " +
		"structure:Village@overworld biome:Mushroom Fields@overworld structure:Village@overworld structure:Ocean Monument@overworld " +
		"biome:Nether Wastes@nether bed:Bed@nether structure:Nether Fortress@nether"
	if describe(got) != want {
		t.Fatalf("got  %s\nwant %s", describe(got), want)
	}
	last := -1.0
	for _, h := range got.Hits {
		switch {
		case h.Dimension == "overworld" && (h.Distance == nil || *h.Distance < last):
			t.Errorf("%s %s is out of order or has no distance", h.Kind, h.Name)
		case h.Dimension != "overworld" && h.Distance != nil:
			t.Errorf("%s %s in the %s carries a distance", h.Kind, h.Name, h.Dimension)
		}
		if h.Distance != nil {
			last = *h.Distance
		}
	}
	// Asked from the nether, the nether's come first.
	rec := do(s.Handler(), "GET", "/api/search?dimension=nether&x=0&z=0&q=fortress", "", []*http.Cookie{session(s, steve)})
	if fromNether := decodeBody[searchAnswer](t, rec); len(fromNether.Hits) != 1 || fromNether.Hits[0].Distance == nil {
		t.Errorf("from the nether: %+v", fromNether)
	}
}

// A waypoint is its owner's. Search reads them for the player the session
// names and nobody else, exactly as /api/waypoints does.
func TestSearchFindsOnlyThePlayersOwnWaypoints(t *testing.T) {
	s, waypoints := withEverything(t)

	mine := search(t, s, "base", session(s, steve))
	if describe(mine) != "waypoint:steve's base@overworld" || mine.Waypoints != "searched" {
		t.Errorf("steve searching for his base: %s (%s)", describe(mine), mine.Waypoints)
	}
	if h := mine.Hits[0]; h.X != 100 || h.Z != -200 || *h.Y != 64 {
		t.Errorf("the waypoint = %+v", h)
	}
	for _, query := range []string{"vault", "alex", "a", "s"} {
		got := search(t, s, query, session(s, steve))
		for _, h := range got.Hits {
			if h.Kind == "waypoint" && h.Name != "steve's base" {
				t.Errorf("steve searching %q was shown %q", query, h.Name)
			}
		}
	}
	theirs := search(t, s, "vault", session(s, alex))
	if describe(theirs) != "waypoint:alex's vault@nether" {
		t.Errorf("alex searching for her vault: %s", describe(theirs))
	}
	// Nothing in a request names a player: not a parameter, not a header.
	rec := do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q=vault&xuid="+alex.XUID+"&player=Alex", "", []*http.Cookie{session(s, steve)})
	if got := decodeBody[searchAnswer](t, rec); len(got.Hits) != 0 {
		t.Errorf("steve naming alex in the request was shown %s", describe(got))
	}
	for _, asked := range waypoints.asked {
		if asked != steve.XUID && asked != alex.XUID {
			t.Errorf("the agent was asked for %q", asked)
		}
	}
	if n := len(waypoints.asked); n != 2 {
		t.Errorf("the agent was asked %d times, want once a player", n)
	}
}

func TestSearchWithoutALoginHasNoWaypoints(t *testing.T) {
	s, waypoints := withEverything(t)
	s.Sessions, s.Codes = nil, nil
	got := search(t, s, "base")
	if len(got.Hits) != 0 || got.Waypoints != "off" || len(waypoints.asked) != 0 {
		t.Errorf("with no login: %s (%s), agent asked %v", describe(got), got.Waypoints, waypoints.asked)
	}
	// Everything that is the same for every player is still found.
	if got := search(t, s, "monument"); len(got.Hits) != 1 {
		t.Errorf("monument: %s", describe(got))
	}
}

func TestSearchStillAnswersWhenTheAgentCannotBeRead(t *testing.T) {
	s, waypoints := withEverything(t)
	waypoints.err = errors.New("dial tcp 10.0.0.7:8080: connection refused")
	rec := do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q=e", "", []*http.Cookie{session(s, steve)})
	got := decodeBody[searchAnswer](t, rec)
	if got.Waypoints != "unavailable" || len(got.Hits) == 0 {
		t.Errorf("waypoints %q with %d hits", got.Waypoints, len(got.Hits))
	}
	if strings.Contains(rec.Body.String(), "10.0.0.7") {
		t.Errorf("the answer names an internal address: %s", rec.Body)
	}
}

func TestSearchBoundsItsAnswerAndItsQuestion(t *testing.T) {
	s, _ := withEverything(t)
	beds := markers.World{chunks.Overworld: {}}
	for i := range int32(300) {
		beds[chunks.Overworld].Beds = append(beds[chunks.Overworld].Beds, markers.Marker{X: i * 10, Y: 64, Z: 0})
	}
	store := markers.NewStore()
	store.Set(time.Now(), beds)
	s.Markers = store
	me := []*http.Cookie{session(s, steve)}

	for limit, want := range map[string]int{"": 20, "&limit=5": 5, "&limit=50": 50, "&limit=51": 50, "&limit=100000": 50} {
		got := decodeBody[searchAnswer](t, do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q=bed"+limit, "", me))
		if len(got.Hits) != want || got.More != 300-want {
			t.Errorf("limit %q: %d hits and %d more, want %d and %d", limit, len(got.Hits), got.More, want, 300-want)
		}
		for i, h := range got.Hits {
			if h.X != int32(i)*10 {
				t.Fatalf("limit %q: hit %d at x %d, want nearest first", limit, i, h.X)
			}
		}
	}
	// A new scan's markers replace the ones search remembered.
	store.Set(time.Now(), markers.World{chunks.Overworld: {Beds: []markers.Marker{{X: 1, Y: 2, Z: 3}}}})
	if got := decodeBody[searchAnswer](t, do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q=bed", "", me)); len(got.Hits) != 1 {
		t.Errorf("after a new scan: %d beds, want 1", len(got.Hits))
	}

	for _, path := range []string{
		"/api/search?dimension=overworld&x=0&z=0",
		"/api/search?dimension=overworld&x=0&z=0&q=",
		"/api/search?dimension=overworld&x=0&z=0&q=+_+",
		"/api/search?dimension=overworld&x=0&z=0&q=" + strings.Repeat("a", 65),
		"/api/search?dimension=overworld&x=0&q=bed",
		"/api/search?dimension=overworld&z=0&q=bed",
		"/api/search?x=0&z=0&q=bed",
		"/api/search?dimension=aether&x=0&z=0&q=bed",
		"/api/search?dimension=overworld&x=0&z=0&q=bed&limit=0",
		"/api/search?dimension=overworld&x=1e99&z=0&q=bed",
	} {
		if rec := do(s.Handler(), "GET", path, "", me); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
	if rec := do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q="+strings.Repeat("a", 64), "", me); rec.Code != http.StatusOK {
		t.Errorf("a query of 64 characters = %d, want 200", rec.Code)
	}
}

// Typing a query is a search a keystroke, and the agent's calls are few
// and shared with the waypoints list.
func TestSearchAsksTheAgentOncePerPlayerInAHalfMinute(t *testing.T) {
	s, waypoints := withEverything(t)
	clock := time.Now()
	s.Sessions.Now = func() time.Time { return clock }
	me, her := session(s, steve), session(s, alex)

	for _, query := range []string{"b", "ba", "bas", "base"} {
		clock = clock.Add(5 * time.Second)
		if got := search(t, s, query, me); got.Waypoints != "searched" {
			t.Fatalf("%q: waypoints %q", query, got.Waypoints)
		}
	}
	if !reflect.DeepEqual(waypoints.asked, []string{steve.XUID}) {
		t.Errorf("after four searches the agent was asked %v", waypoints.asked)
	}
	if got := search(t, s, "vault", her); describe(got) != "waypoint:alex's vault@nether" {
		t.Errorf("alex: %s", describe(got))
	}
	if got := search(t, s, "vault", me); describe(got) != "" {
		t.Errorf("steve was shown %s", describe(got))
	}
	if got := search(t, s, "base", me); describe(got) != "waypoint:steve's base@overworld" {
		t.Errorf("steve: %s", describe(got))
	}
	if !reflect.DeepEqual(waypoints.asked, []string{steve.XUID, alex.XUID}) {
		t.Errorf("the agent was asked %v, want one call each", waypoints.asked)
	}

	clock = clock.Add(searchWaypointTTL)
	search(t, s, "base", me)
	if n := len(waypoints.asked); n != 3 {
		t.Errorf("after the list expired the agent was asked %d times in all, want 3", n)
	}
}

func TestSearchRetriesAfterTheAgentFailed(t *testing.T) {
	s, waypoints := withEverything(t)
	me := session(s, steve)
	waypoints.err = errors.New("connection refused")
	if got := search(t, s, "base", me); got.Waypoints != "unavailable" {
		t.Fatalf("waypoints %q", got.Waypoints)
	}
	waypoints.mu.Lock()
	waypoints.err = nil
	waypoints.mu.Unlock()
	if got := search(t, s, "base", me); got.Waypoints != "searched" || describe(got) != "waypoint:steve's base@overworld" {
		t.Errorf("after the agent recovered: %s (%s)", describe(got), got.Waypoints)
	}
	if n := len(waypoints.asked); n != 2 {
		t.Errorf("the agent was asked %d times, want a retry after the failure", n)
	}
}

type slowWaypoints struct {
	release chan struct{}
	calls   int32
	mu      sync.Mutex
}

func (f *slowWaypoints) Waypoints(context.Context, string) ([]markers.Waypoint, int, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	<-f.release
	return []markers.Waypoint{{Name: "base", Dimension: "overworld"}}, 0, nil
}

func TestSearchSharesOneAgentCallBetweenConcurrentSearches(t *testing.T) {
	s, _ := withEverything(t)
	slow := &slowWaypoints{release: make(chan struct{})}
	s.Waypoints = slow
	me := session(s, steve)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); search(t, s, "base", me) }()
	}
	time.Sleep(50 * time.Millisecond)
	close(slow.release)
	wg.Wait()
	if slow.calls != 1 {
		t.Errorf("eight simultaneous searches made %d agent calls, want 1", slow.calls)
	}
}

func TestSearchHoldsWaypointsForAFixedNumberOfPlayers(t *testing.T) {
	var c searchCache
	clock := time.Now()
	now := func() time.Time { return clock }
	src := &fakeWaypoints{by: map[string][]markers.Waypoint{}}
	for i := range searchWaypointPlayers + 20 {
		clock = clock.Add(time.Millisecond)
		if _, err := c.playerWaypoints(context.Background(), src, fmt.Sprint(i), now); err != nil {
			t.Fatal(err)
		}
		if len(c.waypoints) > searchWaypointPlayers {
			t.Fatalf("%d players held", len(c.waypoints))
		}
	}
	// The oldest went first; the newest is still held.
	if _, ok := c.waypoints["0"]; ok {
		t.Error("the oldest player is still held")
	}
	if _, ok := c.waypoints[fmt.Sprint(searchWaypointPlayers+19)]; !ok {
		t.Error("the newest player was dropped")
	}
	clock = clock.Add(time.Hour)
	if _, err := c.playerWaypoints(context.Background(), src, "new", now); err != nil {
		t.Fatal(err)
	}
	if len(c.waypoints) != 1 {
		t.Errorf("%d players held after everyone expired, want 1", len(c.waypoints))
	}
}
