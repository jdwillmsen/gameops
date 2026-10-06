package server

import (
	"cmp"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

const (
	defaultSearchHits = 20
	maxSearchHits     = 50
	// maxSearchQuery is the longest text searched for, in characters.
	maxSearchQuery = 64
	// stretchesPerBiome is how many stretches of each matching biome go
	// into a search: the nearest few, so that one common biome does not
	// fill the answer.
	stretchesPerBiome = 3
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
	Detail    string `json:"detail,omitempty"`
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
	structures.Fortress: "Nether Fortress",
	structures.Monument: "Ocean Monument",
	structures.Outpost:  "Pillager Outpost",
	structures.WitchHut: "Witch Hut",
}

var containerNames = map[string]string{"chest": "Chest", "barrel": "Barrel", "shulker": "Shulker Box"}

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

// handleSearch looks one piece of text up in everything the map holds that
// has a name: biomes, recorded structures, the world spawn, beds,
// containers, named mobs, and the waypoints of whoever is asking.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := biomes.Fold(q.Get("q"))
	here, okD := dimensionNamed(q.Get("dimension"))
	x, okX := blockParam(q, "x")
	z, okZ := blockParam(q, "z")
	limit, okL := limitParam(q, defaultSearchHits, maxSearchHits)
	if query == "" || utf8.RuneCountInString(query) > maxSearchQuery || !okD || !okX || !okZ || !okL {
		http.Error(w, "a search needs q, of 1 to 64 characters, and the dimension, x and z to measure from", http.StatusBadRequest)
		return
	}
	matches := func(names ...string) bool {
		return slices.ContainsFunc(names, func(name string) bool { return strings.Contains(biomes.Fold(name), query) })
	}

	var hits []searchHit
	add := func(h searchHit, d chunks.Dimension) {
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

	for _, d := range chunks.Dimensions {
		if s.Biomes != nil {
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
		if s.Structures != nil {
			if survey, ok := s.Structures.Last(); ok {
				for _, st := range survey.Layers[d].Recorded {
					if name := structureNames[st.Kind]; matches(name, string(st.Kind)) {
						add(searchHit{Kind: hitStructure, Name: name, Detail: string(st.Kind),
							X: st.MinX + (st.MaxX-st.MinX)/2, Y: height(st.MinY), Z: st.MinZ + (st.MaxZ-st.MinZ)/2}, d)
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
			if matches("Bed") {
				for _, m := range lists.Beds {
					add(searchHit{Kind: hitBed, Name: "Bed", X: m.X, Y: height(m.Y), Z: m.Z}, d)
				}
			}
			for _, m := range lists.Containers {
				kind := cmp.Or(containerNames[m.Kind], "Container")
				if matches(m.Name, kind, m.Kind) {
					add(searchHit{Kind: hitContainer, Name: cmp.Or(m.Name, kind), Detail: m.Kind, X: m.X, Y: height(m.Y), Z: m.Z}, d)
				}
			}
			for _, m := range lists.Mobs {
				if matches(m.Name, m.Kind) {
					add(searchHit{Kind: hitMob, Name: m.Name, Detail: m.Kind, X: m.X, Y: height(m.Y), Z: m.Z}, d)
				}
			}
		}
	}

	out := searchJSON{Waypoints: waypointsOff}
	// Whose waypoints are searched comes from the session and nowhere
	// else, exactly as when they are listed: nothing in the request can
	// name another player, and without a login there is nobody to be.
	if id, ok := auth.FromContext(r.Context()); ok && s.Waypoints != nil && s.Sessions != nil {
		out.Waypoints = waypointsSearched
		list, _, err := s.Waypoints.Waypoints(r.Context(), id.XUID)
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
		return cmp.Or(cmp.Compare(a.order, b.order), cmp.Compare(a.distance, b.distance), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
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
