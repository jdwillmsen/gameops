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
	Detail string `json:"detail,omitempty"`
	// Colour, Trapped and Baby are a marker's own, passed on so that the
	// page can call and draw a hit as it does the marker.
	Colour    string `json:"colour,omitempty"`
	Trapped   bool   `json:"trapped,omitempty"`
	Baby      bool   `json:"baby,omitempty"`
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
	structures.Village:  "Village",
	structures.WitchHut: "Witch Hut",
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

	names := s.displayNames()
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
					if name := structureNames[st.Kind]; matches(name, string(st.Kind), names.Structure(string(st.Kind))) {
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
				if matches(m.Name, m.Kind, names.Entity(m.Kind)) {
					add(searchHit{Kind: hitMob, Name: m.Name, Detail: m.Kind, Baby: m.Baby, X: m.X, Y: height(m.Y), Z: m.Z}, d)
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
