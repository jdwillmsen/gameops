package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// LayerSource draws the cave and level views: a cut through a dimension,
// in the colours of its blocks, a tile at a time.
type LayerSource interface {
	// Version names the reading of the world tiles are drawn from; empty
	// before the first.
	Version() string
	Tile(ctx context.Context, spec layers.Spec, zoom, x, y int) (layers.Result, error)
}

// layerTileTimeout is how long a request for a tile waits for a place to
// be drawn in and for the drawing, which is under the server's own write
// timeout so that a busy server answers and does not drop the connection.
const layerTileTimeout = 25 * time.Second

// The styles a view is drawn in, by the name the page asks for them with.
var layerStyles = []struct {
	ID    string
	Label string
	Style layers.Style
	// Says is what the style is for, which the page shows beside it.
	Says string
}{
	{"floor", "Spaces", layers.Floor, "The ground of caves, tunnels and rooms, with the rock between them dimmed."},
	{"cut", "Cut", layers.Cut, "The world with everything above the cut taken away."},
}

func layerStyle(id string) (layers.Style, bool) {
	for _, s := range layerStyles {
		if s.ID == id {
			return s.Style, true
		}
	}
	return 0, false
}

// netherCuts are the Nether's levels as configured, the default cut first.
func (s *Server) netherCuts() []layers.NetherCut {
	cuts := []layers.NetherCut{{Y: s.NetherTop, Label: render.DefaultNetherLabel}}
	for _, l := range s.NetherLevels {
		cuts = append(cuts, layers.NetherCut{Y: l.TopY, Label: l.Label})
	}
	return cuts
}

type layerLevelJSON struct {
	ID    string `json:"id"`
	Y     int    `json:"y"`
	Label string `json:"label"`
	// Band is how many blocks under the cut a tile looks at, and 0 for all
	// of what is under it.
	Band int `json:"band"`
}

type layerStyleJSON struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Says  string `json:"says"`
}

type layersJSON struct {
	// Offered is false for a dimension with nothing to cut.
	Offered bool `json:"offered"`
	// Ready is false until the world has been opened for drawing.
	Ready bool `json:"ready"`
	// Version names the reading of the world. A tile asked for with it as
	// v may be kept by the browser for good: a new reading is a new
	// version.
	Version string `json:"version,omitempty"`
	Tiles   struct {
		MinZoom int `json:"minZoom"`
		MaxZoom int `json:"maxZoom"`
		Size    int `json:"size"`
	} `json:"tiles"`
	Styles []layerStyleJSON `json:"styles"`
	Levels []layerLevelJSON `json:"levels"`
	// Bands are the thicknesses a request may name besides a level's own.
	Bands []int `json:"bands"`
}

// handleLayers says what the cave and level views hold for a dimension:
// the cuts it is offered at, the styles, and how far in and out a tile is
// drawn.
func (s *Server) handleLayers(w http.ResponseWriter, r *http.Request) {
	d, ok := dimensionNamed(r.URL.Query().Get("dimension"))
	if !ok {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	out := layersJSON{Styles: []layerStyleJSON{}, Levels: []layerLevelJSON{}, Bands: []int{}}
	out.Tiles.MinZoom, out.Tiles.MaxZoom, out.Tiles.Size = layers.MinZoom, layers.MaxZoom, layers.TileSize
	levels := layers.LevelsOf(d, s.netherCuts())
	out.Offered = len(levels) > 0
	if out.Offered {
		for _, st := range layerStyles {
			out.Styles = append(out.Styles, layerStyleJSON{ID: st.ID, Label: st.Label, Says: st.Says})
		}
		for _, l := range levels {
			out.Levels = append(out.Levels, layerLevelJSON{ID: l.ID, Y: l.Y, Label: l.Label, Band: l.Band})
		}
		if d == chunks.Overworld {
			out.Bands = slices.Clone(layers.Bands)
		}
		if v := s.Layers.Version(); v != "" {
			out.Ready, out.Version = true, v
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// layerBand reads the band a request names: nothing, which is the level's
// own and is 0, "all", or one of the few thicknesses there are.
func layerBand(raw string, l layers.Level) (int, bool) {
	switch {
	case raw == "":
		return 0, true
	case raw == "all":
		return layers.AllBelow, true
	}
	n, err := strconv.Atoi(raw)
	// A level that looks at all of what is under it has no other thickness.
	if err != nil || l.Band == 0 || !slices.Contains(layers.Bands, n) {
		return 0, false
	}
	return n, true
}

// handleLayerTile draws one tile of a cut when asked for. A tile takes tens
// of milliseconds to a few hundred from the world's own files, so it is
// kept for as long as the reading it was drawn from, and only so many are
// drawn at once.
func (s *Server) handleLayerTile(w http.ResponseWriter, r *http.Request) {
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
		zoom < layers.MinZoom || zoom > layers.MaxZoom ||
		x < -maxTileCoordinate || x > maxTileCoordinate || y < -maxTileCoordinate || y > maxTileCoordinate {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	// What may be asked for is the levels the dimension is offered at, and
	// nothing the request writes reaches a number the renderer uses.
	var level layers.Level
	found := false
	for _, l := range layers.LevelsOf(d, s.netherCuts()) {
		if l.ID == q.Get("level") {
			level, found = l, true
		}
	}
	style, styled := layerStyle(q.Get("style"))
	band, banded := layerBand(q.Get("band"), level)
	if !found || !styled || !banded {
		http.Error(w, "a tile is asked for by level, one the dimension has, style, floor or cut, and optionally band, 16, 32, 64 or all", http.StatusBadRequest)
		return
	}
	spec := layers.SpecOf(d, level, style, band)
	// A tile past every chunk the world has held has nothing in it, and is
	// not worth a place in the queue to draw: a sweep of the map's far
	// reaches would otherwise hold both places and push the cache's tiles out.
	if s.tileOutside(d, zoom, x, y) {
		w.Header().Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}

	version := s.Layers.Version()
	if version == "" {
		w.Header().Set("Cache-Control", "private, max-age=30")
		w.Header().Set("Retry-After", "30")
		http.Error(w, "the world has not been opened for drawing yet", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	etag := fmt.Sprintf(`"%s-%s-%s-%d"`, version, level.ID, q.Get("style"), spec.Thickness)
	// Private, because a tile is only for the logged-in browser that asked.
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
	ctx, cancel := context.WithTimeout(r.Context(), layerTileTimeout)
	defer cancel()
	res, err := s.Layers.Tile(ctx, spec, zoom, x, y)
	switch {
	case errors.Is(err, layers.ErrNotReady), errors.Is(err, context.DeadlineExceeded):
		h.Del("ETag")
		h.Set("Cache-Control", "no-store")
		h.Set("Retry-After", "5")
		http.Error(w, "the tile is not ready, ask again", http.StatusServiceUnavailable)
		return
	case errors.Is(err, context.Canceled):
		// The viewer moved on.
		return
	case err != nil:
		h.Del("ETag")
		h.Set("Cache-Control", "no-store")
		s.log().Error("layer tile not drawn", "error", err.Error())
		http.Error(w, "the tile could not be drawn", http.StatusInternalServerError)
		return
	}
	if !res.Drawn {
		// Most of a sparse world's grid has nothing in the band; that is
		// not an error, and it may have something after the next reading.
		h.Del("ETag")
		h.Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	if !res.Cached {
		h.Set("Server-Timing", fmt.Sprintf("read;dur=%.1f, paint;dur=%.1f, encode;dur=%.1f",
			ms(res.Read), ms(res.Paint), ms(res.Encode)))
	}
	h.Set("Content-Type", "image/png")
	_, _ = w.Write(res.Data)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// BoundsSource is the box of chunks of a dimension that the world has been
// seen to hold, from the record of every chunk it has had.
type BoundsSource interface {
	Bounds(d chunks.Dimension) (chunks.Box, bool)
}

// tileOutside says the world has been counted and no chunk it has held is
// in or beside the tile. Before the first count nothing is known of where
// the world is.
func (s *Server) tileOutside(d chunks.Dimension, zoom, x, y int) bool {
	src, ok := s.Chunks.(BoundsSource)
	if !ok {
		return false
	}
	if _, counted := s.Chunks.Last(); !counted {
		return false
	}
	box, held := src.Bounds(d)
	return !held || !tileTouches(box, zoom, x, y)
}

// tileTouches says a tile is within a chunk of the box, the margin being the
// row of chunks its lighting reads beyond its edge. Without any chunk seen
// in a dimension nothing is touched.
func tileTouches(b chunks.Box, zoom, x, y int) bool {
	span := 256 << -zoom
	cx0, cz0 := floorDiv(x*span, 16)-1, floorDiv(y*span, 16)-1
	cx1, cz1 := floorDiv((x+1)*span-1, 16)+1, floorDiv((y+1)*span-1, 16)+1
	return cx1 >= int(b.MinX) && cx0 <= int(b.MaxX) && cz1 >= int(b.MinZ) && cz0 <= int(b.MaxZ)
}
