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
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

type searchAnswer struct {
	Hits []struct {
		Kind      string   `json:"kind"`
		Name      string   `json:"name"`
		Detail    string   `json:"detail"`
		Certainty string   `json:"certainty"`
		Colour    string   `json:"colour"`
		Trapped   bool     `json:"trapped"`
		Baby      bool     `json:"baby"`
		ID        string   `json:"id"`
		Live      bool     `json:"live"`
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
		"biome:Nether Wastes@nether bed:Bed@nether structure:Nether Fortress@nether structure:Nether Fortress@nether structure:Nether Fortress@nether"
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
	fromNether := decodeBody[searchAnswer](t, rec)
	if len(fromNether.Hits) != 3 || fromNether.Hits[0].Distance == nil {
		t.Fatalf("from the nether: %+v", fromNether)
	}
	// The one the world recorded, then the two the seed implies, each
	// saying which it is.
	if h := fromNether.Hits; h[0].Certainty != "" || h[0].Y == nil || h[1].Certainty != "predicted" || h[1].X != 536 || h[1].Y != nil || h[2].Certainty != "predicted" {
		t.Errorf("from the nether: %+v", h)
	}
}

// A kind has hundreds of sites, most of them places the generator will
// only try. A search lists the nearest few, as what they are, and what the
// world has recorded is not pushed off the list by them.
func TestSearchListsTheNearestPredictionsAsPredictions(t *testing.T) {
	s, _ := withEverything(t)
	source := surveyed()
	layer := source.survey.Layers[chunks.Overworld]
	for i := range int32(40) {
		layer.Predicted = append(layer.Predicted, structures.Prediction{Kind: structures.Monument, X: 100 + i*100, Z: 0, Candidate: i%2 == 1})
	}
	layer.Predicted = append(layer.Predicted, structures.Prediction{Kind: structures.Outpost, X: 90, Z: 0, Candidate: true})
	source.survey.Layers[chunks.Overworld] = layer
	s.Structures = source

	got := search(t, s, "monument", session(s, steve))
	if len(got.Hits) != predictedPerKind+1 || got.More != 0 {
		t.Fatalf("monument: %d hits and %d more, want the %d nearest sites and the one recorded: %s", len(got.Hits), got.More, predictedPerKind, describe(got))
	}
	for i, h := range got.Hits[:predictedPerKind] {
		want := "predicted"
		if i%2 == 1 {
			want = "candidate"
		}
		if h.X != int32(100+i*100) || h.Certainty != want || h.Kind != "structure" || h.Detail != "monument" || h.Y != nil {
			t.Errorf("hit %d = %+v, want the site at %d as %s", i, h, 100+i*100, want)
		}
	}
	if h := got.Hits[predictedPerKind]; h.Certainty != "" || h.X != 55 {
		t.Errorf("the recorded monument = %+v", h)
	}
	if got := search(t, s, "pillager", session(s, steve)); len(got.Hits) != 1 || got.Hits[0].Certainty != "candidate" {
		t.Errorf("pillager: %+v", got.Hits)
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

// online puts players and mobs in the live picture of one dimension.
func online(s *Server, gen int, dimension string, players, mobs string) {
	now := time.Now()
	for kind, items := range map[string]string{"players": players, "mobs": mobs} {
		s.Live.Ingest(live.RawRecord{At: now, Data: fmt.Sprintf(`{"gen":%d,"dim":%q,"kind":%q,"part":0,"parts":1,"more":0,"items":[%s]}`,
			gen, dimension, kind, items)}, now)
	}
}

// A player is looked for by gamertag wherever they are: the live store has
// every dimension's picture, and the page only the one it is showing.
func TestSearchFindsThePlayersOnlineNowInAnyDimension(t *testing.T) {
	s, _ := withEverything(t)
	s.Live = live.New(10*time.Second, 1000, nil)
	online(s, 1, "overworld", `{"i":"-11","n":"Steve Builds","x":100.5,"y":64,"z":-200.5,"r":0}`, ``)
	online(s, 1, "nether", `{"i":"-12","n":"Alex Digs","x":-3.2,"y":40.9,"z":7.9,"r":0},{"i":"-13","n":`+fmt.Sprintf("%q", hostile)+`,"x":1,"y":2,"z":3}`, ``)
	me := session(s, steve)

	got := search(t, s, "alex", me)
	if len(got.Hits) == 0 {
		t.Fatal("alex: nothing found")
	}
	// Ahead of the waypoint that shares the name, and at the block the
	// player is standing in.
	if h := got.Hits[0]; h.Kind != "player" || h.Name != "Alex Digs" || h.ID != "-12" || !h.Live || h.Dimension != "nether" ||
		h.X != -4 || h.Y == nil || *h.Y != 40 || h.Z != 7 || h.Distance != nil {
		t.Errorf("alex = %+v", h)
	}
	if got := search(t, s, "steve", me); len(got.Hits) < 2 || got.Hits[0].Kind != "player" || got.Hits[0].Distance == nil || got.Hits[1].Kind != "waypoint" {
		t.Errorf("steve: %s", describe(got))
	}
	// A gamertag is a player's choice and is passed on as it is, as text.
	if got := search(t, s, "onerror", me); len(got.Hits) == 0 || got.Hits[0].Kind != "player" || got.Hits[0].Name != hostile {
		t.Errorf("a hostile gamertag: %+v", got.Hits)
	}
	// Kept to players, nothing else that shares the name is listed.
	rec := do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&kind=player&q=steve", "", []*http.Cookie{me})
	if only := decodeBody[searchAnswer](t, rec); describe(only) != "player:Steve Builds@overworld" {
		t.Errorf("kind=player: %s", describe(only))
	}
	if rec := do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&kind=seed&q=steve", "", []*http.Cookie{me}); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown kind = %d, want 400", rec.Code)
	}
	// Nobody is told who is online without a session.
	if rec := do(s.Handler(), "GET", "/api/search?dimension=overworld&x=0&z=0&q=alex", "", nil); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "Alex") {
		t.Errorf("without a session = %d %s", rec.Code, rec.Body.String())
	}
}

// A position the live layer no longer vouches for is not where anyone is.
func TestSearchFindsNoPlayerInAStalePicture(t *testing.T) {
	s, _ := withEverything(t)
	s.Live = live.New(time.Nanosecond, 1000, nil)
	online(s, 1, "overworld", `{"i":"-11","n":"Steve Builds","x":1,"y":2,"z":3}`, ``)
	time.Sleep(time.Millisecond)
	if got := search(t, s, "steve builds", session(s, steve)); len(got.Hits) != 0 {
		t.Errorf("found %s in a picture that has aged out", describe(got))
	}
}

// A named mob is the same animal in the snapshot and in the live picture by
// the game's id for it, and by nothing else.
func TestSearchResolvesANamedMobToWhereItIsNow(t *testing.T) {
	s := withLogin(t)
	store := markers.NewStore()
	store.Set(time.Now(), markers.World{chunks.Overworld: {Mobs: []markers.Marker{
		{X: 10, Y: 64, Z: 10, Kind: "cat", Name: "Biscuit", ID: "-21"},
		{X: 20, Y: 64, Z: 20, Kind: "cat", Name: "Biscuit", ID: "-22"},
		{X: 30, Y: 64, Z: 30, Kind: "cat", Name: "Biscuit"},
	}}})
	s.Markers = store
	s.Live = live.New(10*time.Second, 1000, nil)
	// One of the three is loaded and has wandered; a fourth was named
	// since the snapshot; an unnamed cat is nobody's to find.
	online(s, 1, "overworld", ``, `{"i":"-22","t":"cat","n":"Biscuit","x":-40.5,"y":70,"z":41.5},{"i":"-23","t":"cat","n":"Biscuit Two","x":5,"y":64,"z":5},{"i":"-24","t":"cat","x":6,"y":64,"z":6}`)

	got := search(t, s, "biscuit", session(s, steve))
	var said []string
	for _, h := range got.Hits {
		said = append(said, fmt.Sprintf("%s %q %d,%d live=%v", h.ID, h.Name, h.X, h.Z, h.Live))
	}
	want := []string{`-23 "Biscuit Two" 5,5 live=true`, `-21 "Biscuit" 10,10 live=false`, ` "Biscuit" 30,30 live=false`, `-22 "Biscuit" -41,41 live=true`}
	if !reflect.DeepEqual(said, want) {
		t.Errorf("biscuit:\n got %q\nwant %q", said, want)
	}
}
