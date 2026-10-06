package server

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// BiomeSource is the last reading of the world's biomes, nil before the
// first.
type BiomeSource interface {
	World() *biomes.World
}

const (
	// maxBlock is past the edge of any Bedrock world.
	maxBlock = 32_000_000

	defaultNearest = 10
	maxNearest     = 50
	// maxRegionRects is the most rows of chunks one region is sent as. A
	// whole ocean is a few thousand; past this the page has the region's
	// box and the tiles to go by.
	maxRegionRects = 4096
)

func dimensionNamed(name string) (chunks.Dimension, bool) {
	for _, d := range chunks.Dimensions {
		if d.Name() == name {
			return d, true
		}
	}
	return 0, false
}

// blockParam reads a block coordinate. The page holds positions as
// fractions of a block, so one is taken and rounded down.
func blockParam(q url.Values, name string) (int32, bool) {
	v, err := strconv.ParseFloat(q.Get(name), 64)
	if err != nil || math.IsNaN(v) || math.Abs(v) > maxBlock {
		return 0, false
	}
	return int32(math.Floor(v)), true
}

// limitParam reads how many results are wanted, between 1 and most.
func limitParam(q url.Values, fallback, most int) (int, bool) {
	raw := q.Get("limit")
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	return min(n, most), true
}

func biomeVersion(w *biomes.World) string {
	return strconv.FormatInt(w.SnapshotAt.Unix(), 36)
}

type biomeTilesJSON struct {
	MinZoom int `json:"minZoom"`
	MaxZoom int `json:"maxZoom"`
	Size    int `json:"size"`
}

type biomesJSON struct {
	// Extracted is false until the world's biomes have been read once.
	Extracted bool       `json:"extracted"`
	At        *time.Time `json:"at,omitempty"`
	// Version names the reading. A tile asked for with it as v may be kept
	// by the browser for good: a new reading is a new version.
	Version string            `json:"version,omitempty"`
	Biomes  []biomes.Presence `json:"biomes"`
	Tiles   biomeTilesJSON    `json:"tiles"`
}

// handleBiomes lists the biomes a dimension holds, largest first, with the
// colour each is drawn in: the overlay's legend.
func (s *Server) handleBiomes(w http.ResponseWriter, r *http.Request) {
	d, ok := dimensionNamed(r.URL.Query().Get("dimension"))
	if !ok {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	out := biomesJSON{Biomes: []biomes.Presence{}, Tiles: biomeTilesJSON{MinZoom: minTileZoom, MaxZoom: maxTileZoom, Size: biomes.TileSize}}
	if world := s.Biomes.World(); world != nil {
		out.Extracted, out.At, out.Version = true, &world.SnapshotAt, biomeVersion(world)
		if list := world.Present(d); list != nil {
			out.Biomes = list
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleBiomeTile draws one tile of the overlay. It is drawn when asked
// for: a tile takes a millisecond or two from what is already in memory,
// which is less than reading one from the volume would save.
func (s *Server) handleBiomeTile(w http.ResponseWriter, r *http.Request) {
	d, ok := dimensionNamed(r.PathValue("dimension"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	zoom, errZ := strconv.Atoi(r.PathValue("zoom"))
	x, errX := strconv.Atoi(r.PathValue("x"))
	name, ext, _ := strings.Cut(r.PathValue("y"), ".")
	y, errY := strconv.Atoi(name)
	if errZ != nil || errX != nil || errY != nil || ext != "png" ||
		zoom < minTileZoom || zoom > maxTileZoom ||
		x < -maxTileCoordinate || x > maxTileCoordinate || y < -maxTileCoordinate || y > maxTileCoordinate {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	var only *uint32
	picked := "all"
	if name := q.Get("biome"); name != "" {
		id, ok := biomes.Resolve(name)
		if !ok {
			http.Error(w, "unknown biome", http.StatusBadRequest)
			return
		}
		only, picked = &id, strconv.FormatUint(uint64(id), 10)
	}

	h := w.Header()
	world := s.Biomes.World()
	if world == nil {
		h.Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	version := biomeVersion(world)
	etag := `"` + version + "-" + picked + `"`
	// Private, because a tile is only for the logged-in browser that asked.
	// Asked for by its version it can never change; asked for without, it
	// is checked against the tag every time.
	if q.Get("v") == version {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, ok := world.Tile(d, zoom, x, y, only)
	if !ok {
		// Most of a sparse world's grid has no tile; that is not an error.
		// It may have one after the next reading, so this is not kept long.
		h.Del("ETag")
		h.Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	h.Set("Content-Type", "image/png")
	_, _ = w.Write(data)
}

type biomeAtJSON struct {
	// Generated is false where the world has no chunk, or before the
	// first reading: there is no biome to name.
	Generated bool          `json:"generated"`
	Biome     *biomes.Biome `json:"biome,omitempty"`
}

// handleBiomeAt names the biome at one block.
func (s *Server) handleBiomeAt(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, okD := dimensionNamed(q.Get("dimension"))
	x, okX := blockParam(q, "x")
	z, okZ := blockParam(q, "z")
	if !okD || !okX || !okZ {
		http.Error(w, "a biome is asked for by dimension, x and z", http.StatusBadRequest)
		return
	}
	var out biomeAtJSON
	if b, ok := s.Biomes.World().At(d, x, z); ok {
		out.Generated, out.Biome = true, &b
	}
	writeJSON(w, http.StatusOK, out)
}

type biomeNearestJSON struct {
	Biome biomes.Biome `json:"biome"`
	Hits  []biomes.Hit `json:"hits"`
	// More is how many further stretches of the biome there are.
	More int `json:"more"`
}

// handleBiomeNearest finds the stretches of one biome nearest a block.
func (s *Server) handleBiomeNearest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, okD := dimensionNamed(q.Get("dimension"))
	x, okX := blockParam(q, "x")
	z, okZ := blockParam(q, "z")
	limit, okL := limitParam(q, defaultNearest, maxNearest)
	if !okD || !okX || !okZ || !okL {
		http.Error(w, "the nearest biome is asked for by dimension, biome, x and z, with a limit of 1 or more", http.StatusBadRequest)
		return
	}
	id, ok := biomes.Resolve(q.Get("biome"))
	if !ok {
		http.Error(w, "unknown biome", http.StatusBadRequest)
		return
	}
	out := biomeNearestJSON{Biome: biomes.Lookup(id), Hits: []biomes.Hit{}}
	if hits, more := s.Biomes.World().Nearest(d, id, x, z, limit); hits != nil {
		out.Hits, out.More = hits, more
	}
	writeJSON(w, http.StatusOK, out)
}

type biomeRegionJSON struct {
	// Found is false where there is no biome to belong to.
	Found  bool           `json:"found"`
	Biome  *biomes.Biome  `json:"biome,omitempty"`
	Region *biomes.Extent `json:"region,omitempty"`
	// Rects is the region's chunks as rows: each minX, minZ, maxX, maxZ in
	// blocks, both corners inclusive.
	Rects []biomes.Rect `json:"rects"`
	// RectsMore is how many rows were left out at the limit.
	RectsMore int `json:"rectsMore"`
}

// handleBiomeRegion gives the extent of the stretch of biome a block is in,
// for the page to outline.
func (s *Server) handleBiomeRegion(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, okD := dimensionNamed(q.Get("dimension"))
	x, okX := blockParam(q, "x")
	z, okZ := blockParam(q, "z")
	if !okD || !okX || !okZ {
		http.Error(w, "a region is asked for by dimension, x and z", http.StatusBadRequest)
		return
	}
	out := biomeRegionJSON{Rects: []biomes.Rect{}}
	if b, extent, rects, more, ok := s.Biomes.World().Region(d, x, z, maxRegionRects); ok {
		out.Found, out.Biome, out.Region, out.Rects, out.RectsMore = true, &b, &extent, rects, more
	}
	writeJSON(w, http.StatusOK, out)
}
