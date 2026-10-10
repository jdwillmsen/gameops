package server

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

const (
	defaultSearchHits = 20
	maxSearchHits     = 50
	// maxSearchQuery is the longest text searched for, in characters.
	maxSearchQuery = 64
	// A player's waypoints are held for search this long, for at most
	// searchWaypointPlayers players: typing a query is a search a keystroke,
	// and each one would otherwise take one of the few agent calls that
	// /api/waypoints shares.
	searchWaypointTTL     = 30 * time.Second
	searchWaypointPlayers = 64
	// stretchesPerBiome is how many stretches of each matching biome go
	// into a search: the nearest few, so that one common biome does not
	// fill the answer.
	stretchesPerBiome = 3
	// predictedPerKind is how many predicted sites of each kind of
	// structure go into a search from one dimension: the nearest few. A
	// kind has hundreds, most of them places the generator will only try,
	// and would otherwise push what the world has recorded off the list.
	predictedPerKind = 5
)

// What a search hit is.
const (
	hitBiome     = "biome"
	hitStructure = "structure"
	hitSpawn     = "spawn"
	hitBed       = "bed"
	hitContainer = "container"
	hitMob       = "mob"
	hitWaypoint  = "waypoint"
	hitPlayer    = "player"
)

// searchKinds is every kind a search may be kept to.
var searchKinds = []string{hitBiome, hitStructure, hitSpawn, hitBed, hitContainer, hitMob, hitWaypoint, hitPlayer}

// How sure a structure hit is, where it is not one the world recorded.
const (
	// certaintyPredicted: the seed puts one here.
	certaintyPredicted = "predicted"
	// certaintyCandidate: the seed puts a site here, in terrain that is
	// not generated, and the biome will decide.
	certaintyCandidate = "candidate"
)

// Whether the player's waypoints were part of a search.
const (
	waypointsSearched    = "searched"
	waypointsUnavailable = "unavailable"
	waypointsOff         = "off"
)

type searchHit struct {
	Kind string `json:"kind"`
	// Name is what matched and what the page lists. A container's, a
	// mob's and a waypoint's are text a player chose: never markup.
	Name string `json:"name"`
	// Detail says more where the name does not: a biome's identifier, a
	// structure's kind, a container's kind, a mob's type.
	Detail string `json:"detail,omitempty"`
	// Certainty is left out for everything the world holds. A structure
	// worked out from the seed carries predicted or candidate, so that
	// the page never lists a calculation as a fact.
	Certainty string `json:"certainty,omitempty"`
	// Colour, Trapped and Baby are a marker's own, passed on so that the
	// page can call and draw a hit as it does the marker.
	Colour  string `json:"colour,omitempty"`
	Trapped bool   `json:"trapped,omitempty"`
	Baby    bool   `json:"baby,omitempty"`
	// ID is the game's own id for a player or a mob, which is what the
	// live layer tracks one by. A named mob read from a world that does
	// not say has none.
	ID string `json:"id,omitempty"`
	// Live is set where the position is from the live layer, and so is
	// where the player or mob is now. Without it a mob's position is where
	// the last snapshot found it.
	Live      bool   `json:"live,omitempty"`
	Dimension string `json:"dimension"`
	X         int32  `json:"x"`
	// Y is left out for what has no height: a biome, the spawn of a world
	// that has not resolved one.
	Y *int32 `json:"y,omitempty"`
	Z int32  `json:"z"`
	// Distance is from the point asked about, in blocks, and is left out
	// for a hit in another dimension, which no walk leads to.
	Distance *float64 `json:"distance,omitempty"`

	distance float64
	order    int
	// first puts a hit ahead of the places: someone who is online is
	// looked for by name far more often than anything else that shares it.
	first bool
}

type searchJSON struct {
	Hits []searchHit `json:"hits"`
	// More is how many further matches there were.
	More int `json:"more"`
	// Waypoints is whether the player's own waypoints were searched:
	// searched, unavailable (the agent could not be read) or off.
	Waypoints string `json:"waypoints"`
}

var structureNames = map[structures.Kind]string{
	structures.Fortress:     "Nether Fortress",
	structures.Monument:     "Ocean Monument",
	structures.Outpost:      "Pillager Outpost",
	structures.Village:      "Village",
	structures.WitchHut:     "Witch Hut",
	structures.Stronghold:   "Stronghold",
	structures.TrialChamber: "Trial Chamber",

	structures.DesertPyramid: "Desert Pyramid",
	structures.JungleTemple:  "Jungle Temple",
	structures.Igloo:         "Igloo",
	structures.TrailRuins:    "Trail Ruins",
	structures.AbandonedCamp: "Abandoned Camp",
	structures.RuinedPortal:  "Ruined Portal",
	structures.Bastion:       "Bastion Remnant",
	structures.EndCity:       "End City",
	structures.EndGateway:    "End Gateway",
	structures.ExitPortal:    "Exit Portal",
}

// displayNames is the names the page shows, so that a search finds a thing
// by what the page calls it. With no names to ask it is nil, which tidies
// every id.
func (s *Server) displayNames() *icons.Names {
	if s.Art == nil {
		return nil
	}
	return s.Art.Names()
}

// containerName is what a container marker is called: by its colour if it
// is a shulker box, as trapped if it is that sort of chest.
func containerName(names *icons.Names, m markers.Marker) string {
	switch {
	case m.Kind == "":
		return "Container"
	case m.Kind == "shulker":
		return names.Shulker(m.Colour)
	case m.Kind == "chest" && m.Trapped:
		return names.Container("trapped_chest")
	}
	return names.Container(m.Kind)
}

// markerLists is one dimension's markers as the marker store serves them.
type markerLists struct {
	Beds       []markers.Marker `json:"beds"`
	Containers []markers.Marker `json:"containers"`
	Mobs       []markers.Marker `json:"mobs"`
}

// searchCache keeps each dimension's markers decoded, for as long as the
// store goes on serving the same ones: they change once a snapshot, and a
// search would otherwise decode up to 2 MB a dimension every time.
type searchCache struct {
	mu   sync.Mutex
	held map[string]cachedMarkers
	// waypoints is keyed by the session's XUID and nothing else, so one
	// player's list is never an answer for another.
	waypoints map[string]*cachedWaypoints
}

// cachedWaypoints is one player's list. fetch is held for the whole agent
// call, so searches by the same player wait for it instead of each making
// their own; list, stamp and fresh are guarded by searchCache.mu.
type cachedWaypoints struct {
	fetch sync.Mutex
	list  []markers.Waypoint
	// stamp is when the list was fetched, or when the entry was made while
	// it has none yet.
	stamp time.Time
	fresh bool
}

type cachedMarkers struct {
	etag  string
	lists markerLists
}

func (c *searchCache) markers(store MarkerStore, dimension string) markerLists {
	body, etag, ok := store.Dimension(dimension)
	if !ok {
		return markerLists{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if hit, ok := c.held[dimension]; ok && hit.etag == etag {
		return hit.lists
	}
	var lists markerLists
	if err := json.Unmarshal(body, &lists); err != nil {
		return markerLists{}
	}
	if c.held == nil {
		c.held = map[string]cachedMarkers{}
	}
	c.held[dimension] = cachedMarkers{etag, lists}
	return lists
}

// playerWaypoints returns the waypoints of the player with this XUID, from
// the agent at most once in searchWaypointTTL. A failure is returned and
// not kept, so the next search asks again.
func (c *searchCache) playerWaypoints(ctx context.Context, src WaypointSource, xuid string, now func() time.Time) ([]markers.Waypoint, error) {
	c.mu.Lock()
	e := c.waypoints[xuid]
	if e == nil {
		if c.waypoints == nil {
			c.waypoints = map[string]*cachedWaypoints{}
		}
		if len(c.waypoints) >= searchWaypointPlayers {
			c.evictWaypoints(now())
		}
		e = &cachedWaypoints{stamp: now()}
		c.waypoints[xuid] = e
	}
	c.mu.Unlock()

	// The cache-wide lock is not held across the agent call.
	e.fetch.Lock()
	defer e.fetch.Unlock()
	c.mu.Lock()
	if e.fresh && now().Sub(e.stamp) < searchWaypointTTL {
		list := e.list
		c.mu.Unlock()
		return list, nil
	}
	c.mu.Unlock()
	list, _, err := src.Waypoints(ctx, xuid)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	e.list, e.stamp, e.fresh = list, now(), true
	c.mu.Unlock()
	return list, nil
}

// evictWaypoints makes room for one more player: the expired go first, and
// if every entry is still current, the oldest. The caller holds c.mu.
func (c *searchCache) evictWaypoints(now time.Time) {
	for k, e := range c.waypoints {
		if e.fresh && now.Sub(e.stamp) >= searchWaypointTTL {
			delete(c.waypoints, k)
		}
	}
	if len(c.waypoints) < searchWaypointPlayers {
		return
	}
	oldest, at := "", now.Add(time.Hour)
	for k, e := range c.waypoints {
		if e.stamp.Before(at) {
			oldest, at = k, e.stamp
		}
	}
	delete(c.waypoints, oldest)
}

// nearestPredictions is the predictions of each kind nearest block x, z,
// at most perKind of a kind, in the order they came.
func nearestPredictions(all []structures.Prediction, x, z int32, perKind int) []structures.Prediction {
	away := func(p structures.Prediction) float64 {
		return math.Hypot(float64(p.X)-float64(x), float64(p.Z)-float64(z))
	}
	byKind := map[structures.Kind][]structures.Prediction{}
	for _, p := range all {
		byKind[p.Kind] = append(byKind[p.Kind], p)
	}
	var out []structures.Prediction
	for _, kind := range structures.Kinds {
		list := byKind[kind]
		slices.SortStableFunc(list, func(a, b structures.Prediction) int { return cmp.Compare(away(a), away(b)) })
		out = append(out, list[:min(len(list), perKind)]...)
	}
	return out
}

// handleSearch looks one piece of text up in everything the map holds that
// has a name: the players online now, biomes, recorded and predicted
// structures, the world spawn, beds, containers, named mobs, and the
// waypoints of whoever is asking.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := biomes.Fold(q.Get("q"))
	here, okD := dimensionNamed(q.Get("dimension"))
	x, okX := blockParam(q, "x")
	z, okZ := blockParam(q, "z")
	limit, okL := limitParam(q, defaultSearchHits, maxSearchHits)
	only := q.Get("kind")
	// The kinds that are off until asked for, which this viewer has on. A
	// page from before there was more than one such kind says so of the
	// stronghold alone.
	asked := map[structures.Kind]bool{structures.Stronghold: q.Get("strongholds") == "1"}
	for i, kind := range strings.Split(q.Get("asked"), ",") {
		if i >= len(structures.Catalog) {
			break
		}
		asked[structures.Kind(kind)] = true
	}
	// A kind that gives away a goal is listed only for a viewer who has
	// turned its row on, whatever is typed.
	held := func(kind structures.Kind) bool {
		k, known := structures.InfoOf(kind)
		return known && k.Asked && !asked[kind]
	}
	if query == "" || utf8.RuneCountInString(query) > maxSearchQuery || !okD || !okX || !okZ || !okL ||
		(only != "" && !slices.Contains(searchKinds, only)) {
		http.Error(w, "a search needs q, of 1 to 64 characters, and the dimension, x and z to measure from; kind, if given, is one kind of hit", http.StatusBadRequest)
		return
	}
	wants := func(kind string) bool { return only == "" || only == kind }
	matches := func(names ...string) bool {
		return slices.ContainsFunc(names, func(name string) bool { return strings.Contains(biomes.Fold(name), query) })
	}

	names := s.displayNames()
	var hits []searchHit
	add := func(h searchHit, d chunks.Dimension) {
		if !wants(h.Kind) {
			return
		}
		h.Dimension = d.Name()
		h.distance = math.Round(math.Hypot(float64(h.X)-float64(x), float64(h.Z)-float64(z)))
		// The dimension asked from comes first, then the others in their
		// usual order.
		h.order = int(d) + 1
		if d == here {
			h.order = 0
			h.Distance = &h.distance
		}
		hits = append(hits, h)
	}
	height := func(y int32) *int32 { return &y }
	block := func(v float64) int32 { return int32(math.Floor(v)) }

	// Who is online and which named mobs are loaded, from the same pictures
	// the page is streamed: nothing here that a session cannot already see,
	// and no more of it than the live store's own cap. Every dimension's is
	// read before any marker is looked at, since a mob led through a portal
	// since the snapshot is loaded in a dimension its marker is not in.
	type loadedMob struct {
		live.Entity
		in chunks.Dimension
	}
	loaded := map[string]loadedMob{}
	if s.Live != nil && (wants(hitPlayer) || wants(hitMob)) {
		now := time.Now()
		for _, d := range chunks.Dimensions {
			frame := s.Live.Store.Snapshot(d.Name(), now)
			for _, p := range frame.Players {
				if matches(p.Name) {
					add(searchHit{Kind: hitPlayer, Name: p.Name, ID: p.ID, Live: true, first: true,
						X: block(p.X), Y: height(block(p.Y)), Z: block(p.Z)}, d)
				}
			}
			for _, m := range frame.Mobs {
				if m.Name != "" {
					loaded[m.ID] = loadedMob{m, d}
				}
			}
		}
	}
	for _, d := range chunks.Dimensions {
		if s.Biomes != nil && wants(hitBiome) {
			world := s.Biomes.World()
			for _, p := range world.Present(d) {
				if !p.Matches(query) {
					continue
				}
				near, _ := world.Nearest(d, p.ID, x, z, stretchesPerBiome)
				for _, n := range near {
					add(searchHit{Kind: hitBiome, Name: p.Label, Detail: p.Name, X: n.X, Z: n.Z}, d)
				}
			}
		}
		if s.Structures != nil && (wants(hitStructure) || wants(hitSpawn)) {
			if survey, ok := s.Structures.Last(); ok {
				for _, st := range survey.Layers[d].Recorded {
					if held(st.Kind) {
						continue
					}
					if name := structureNames[st.Kind]; matches(name, string(st.Kind), names.Structure(string(st.Kind))) {
						add(searchHit{Kind: hitStructure, Name: name, Detail: string(st.Kind),
							X: st.MinX + (st.MaxX-st.MinX)/2, Y: height(st.MinY), Z: st.MinZ + (st.MaxZ-st.MinZ)/2}, d)
					}
				}
				for _, p := range nearestPredictions(survey.Layers[d].Predicted, x, z, predictedPerKind) {
					if held(p.Kind) {
						continue
					}
					if name := structureNames[p.Kind]; matches(name, string(p.Kind), names.Structure(string(p.Kind))) {
						certainty := certaintyPredicted
						if p.Candidate {
							certainty = certaintyCandidate
						}
						add(searchHit{Kind: hitStructure, Name: name, Detail: string(p.Kind), Certainty: certainty, X: p.X, Z: p.Z}, d)
					}
				}
				if d == chunks.Overworld && survey.HasLevel && matches("World Spawn") {
					spawn := searchHit{Kind: hitSpawn, Name: "World Spawn", X: survey.Level.SpawnX, Z: survey.Level.SpawnZ}
					if survey.Level.SpawnYKnown {
						spawn.Y = height(survey.Level.SpawnY)
					}
					add(spawn, d)
				}
			}
		}
		if s.Markers != nil {
			lists := s.search.markers(s.Markers, d.Name())
			for _, m := range lists.Beds {
				if name := names.Bed(m.Colour); matches(name) {
					add(searchHit{Kind: hitBed, Name: name, Colour: m.Colour, X: m.X, Y: height(m.Y), Z: m.Z}, d)
				}
			}
			for _, m := range lists.Containers {
				kind := containerName(names, m)
				if matches(m.Name, kind, m.Kind) {
					add(searchHit{Kind: hitContainer, Name: cmp.Or(m.Name, kind), Detail: m.Kind, Colour: m.Colour, Trapped: m.Trapped,
						X: m.X, Y: height(m.Y), Z: m.Z}, d)
				}
			}
			for _, m := range lists.Mobs {
				// The same animal by the game's id for it, never by its
				// name or where it was. While it is loaded it is found by
				// the name it has now and listed once, where it is now,
				// in whichever dimension that is: one renamed since the
				// snapshot does not answer to its old name with its new
				// position, and one that has changed dimension is not also
				// listed where it was.
				at, isLoaded := loaded[m.ID]
				isLoaded = isLoaded && m.ID != ""
				name := m.Name
				if isLoaded {
					name = at.Name
				}
				if !matches(name, m.Kind, names.Entity(m.Kind)) {
					continue
				}
				h := searchHit{Kind: hitMob, Name: name, Detail: m.Kind, Baby: m.Baby, ID: m.ID, X: m.X, Y: height(m.Y), Z: m.Z}
				in := d
				if isLoaded {
					h.Live, h.X, h.Y, h.Z, in = true, block(at.X), height(block(at.Y)), block(at.Z), at.in
					delete(loaded, m.ID)
				}
				add(h, in)
			}
		}
	}
	// A mob named since the last snapshot is in no marker yet.
	for _, m := range loaded {
		if matches(m.Name, m.Type, names.Entity(m.Type)) {
			add(searchHit{Kind: hitMob, Name: m.Name, Detail: m.Type, ID: m.ID, Live: true,
				X: block(m.X), Y: height(block(m.Y)), Z: block(m.Z)}, m.in)
		}
	}

	out := searchJSON{Waypoints: waypointsOff}
	// Whose waypoints are searched comes from the session and nowhere
	// else, exactly as when they are listed: nothing in the request can
	// name another player, and without a login there is nobody to be.
	if id, ok := auth.FromContext(r.Context()); ok && s.Waypoints != nil && s.Sessions != nil && wants(hitWaypoint) {
		out.Waypoints = waypointsSearched
		list, err := s.search.playerWaypoints(r.Context(), s.Waypoints, id.XUID, s.Sessions.Now)
		if err != nil {
			// A search is still worth its other answers.
			out.Waypoints = waypointsUnavailable
			s.log().Warn("waypoints not searched", "xuid", id.XUID, "error", err.Error())
		}
		for _, wp := range list {
			if d, ok := dimensionNamed(wp.Dimension); ok && matches(wp.Name) {
				add(searchHit{Kind: hitWaypoint, Name: wp.Name, X: wp.X, Y: height(wp.Y), Z: wp.Z}, d)
			}
		}
	}

	slices.SortStableFunc(hits, func(a, b searchHit) int {
		rank := func(h searchHit) int {
			if h.first {
				return 0
			}
			return 1
		}
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.order, b.order), cmp.Compare(a.distance, b.distance),
			cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	if len(hits) > limit {
		out.More = len(hits) - limit
		hits = hits[:limit]
	}
	out.Hits = hits
	if out.Hits == nil {
		out.Hits = []searchHit{}
	}
	writeJSON(w, http.StatusOK, out)
}
