package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// MissingSource is the chunks the world is known to have held and does not
// hold now: those the chunk count has seen before and not found in its last
// count. It is what tells a lost chunk from ground that was never
// generated, which look the same on the terrain.
type MissingSource interface {
	Missing() chunks.Set
}

const (
	// lostMinZoom is the widest tile drawn: a pixel is 32 blocks, where a
	// chunk is half of one and is drawn as a whole pixel so that it is not
	// lost to the eye. lostMaxZoom is the finest.
	lostMinZoom = -5
	lostMaxZoom = 4
)

// lostSet is the missing chunks of a count, indexed for a tile to look up,
// kept until a newer count replaces it.
type lostSet struct {
	at time.Time
	// missing is how many were missing at that count: an acknowledgement
	// forgets some without the count's time changing.
	missing int
	chunk   map[chunks.Pos]struct{}
	count   map[chunks.Dimension]int
}

// lost is the missing chunks as of the last count, indexed once per count
// and not once per tile.
func (s *Server) lost() *lostSet {
	src, ok := s.Chunks.(MissingSource)
	if !ok {
		return nil
	}
	report, counted := s.Chunks.Last()
	if !counted {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	missing := report.TotalMissing()
	if s.lostHeld != nil && s.lostHeld.at.Equal(report.At) && s.lostHeld.missing == missing {
		return s.lostHeld
	}
	set := src.Missing()
	held := &lostSet{at: report.At, missing: missing, chunk: set, count: map[chunks.Dimension]int{}}
	for p := range set {
		held.count[p.Dim]++
	}
	s.lostHeld = held
	return held
}

func lostVersion(l *lostSet) string {
	return strconv.FormatInt(l.at.Unix(), 36) + "-" + strconv.Itoa(l.missing)
}

type lostJSON struct {
	// Offered is false where the service keeps no record of what the world
	// has held, and before the first count.
	Offered bool `json:"offered"`
	// Version names the count. A tile asked for with it as v may be kept
	// by the browser for good.
	Version string `json:"version,omitempty"`
	// Missing is how many chunks of the dimension are missing now.
	Missing int `json:"missing"`
	Tiles   struct {
		MinZoom int `json:"minZoom"`
		MaxZoom int `json:"maxZoom"`
		Size    int `json:"size"`
	} `json:"tiles"`
}

// handleLost says whether chunks the world has lost can be marked, and how
// many there are in a dimension.
func (s *Server) handleLost(w http.ResponseWriter, r *http.Request) {
	d, ok := dimensionNamed(r.URL.Query().Get("dimension"))
	if !ok {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	var out lostJSON
	out.Tiles.MinZoom, out.Tiles.MaxZoom, out.Tiles.Size = lostMinZoom, lostMaxZoom, 256
	if l := s.lost(); l != nil {
		out.Offered, out.Version, out.Missing = true, lostVersion(l), l.count[d]
	}
	writeJSON(w, http.StatusOK, out)
}

var (
	// A lost chunk is a translucent red with a heavier diagonal through
	// it, which stays a mark and not a colour of the terrain when the
	// ground under it is red already.
	lostFill  = color.NRGBA{R: 214, G: 40, B: 40, A: 110}
	lostHatch = color.NRGBA{R: 140, G: 10, B: 10, A: 230}
)

// lostTile draws the missing chunks of one tile, or says there are none.
func lostTile(l *lostSet, d chunks.Dimension, zoom, tx, ty int) ([]byte, bool) {
	if zoom < lostMinZoom || zoom > lostMaxZoom || l.count[d] == 0 {
		return nil, false
	}
	// A tile is 256 pixels: that many blocks across at zoom 0, more below
	// it and fewer above.
	span := 256
	if zoom < 0 {
		span <<= -zoom
	} else {
		span >>= zoom
	}
	x0, z0 := tx*span, ty*span
	// A chunk's width in pixels, which is under one where a pixel is wider
	// than a chunk.
	chunkPx := 16 * 256 / span
	img := image.NewPaletted(image.Rect(0, 0, 256, 256), color.Palette{color.NRGBA{}, lostFill, lostHatch})
	drawn := false
	mark := func(cx, cz int32) {
		px, pz := (int(cx)*16-x0)*256/span, (int(cz)*16-z0)*256/span
		size := max(chunkPx, 1)
		for dz := range size {
			for dx := range size {
				x, z := px+dx, pz+dz
				if x < 0 || z < 0 || x >= 256 || z >= 256 {
					continue
				}
				c := uint8(1)
				// A diagonal across a chunk that is drawn big enough to have one.
				if size >= 8 && (dx+dz)%size < max(size/8, 1) {
					c = 2
				}
				img.SetColorIndex(x, z, c)
				drawn = true
			}
		}
	}
	cx0, cz0 := floorDiv(x0, 16), floorDiv(z0, 16)
	across := span / 16
	if len(l.chunk) < across*across {
		for p := range l.chunk {
			if p.Dim == d && int(p.X) >= cx0 && int(p.X) < cx0+across && int(p.Z) >= cz0 && int(p.Z) < cz0+across {
				mark(p.X, p.Z)
			}
		}
	} else {
		for cz := cz0; cz < cz0+across; cz++ {
			for cx := cx0; cx < cx0+across; cx++ {
				if _, gone := l.chunk[chunks.Pos{Dim: d, X: int32(cx), Z: int32(cz)}]; gone {
					mark(int32(cx), int32(cz))
				}
			}
		}
	}
	if !drawn {
		return nil, false
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, img); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// handleLostTile draws one tile of the lost chunks, addressed as every
// tile is. It is drawn when asked for, from a set held in memory.
func (s *Server) handleLostTile(w http.ResponseWriter, r *http.Request) {
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
		zoom < lostMinZoom || zoom > lostMaxZoom ||
		x < -maxTileCoordinate || x > maxTileCoordinate || y < -maxTileCoordinate || y > maxTileCoordinate {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	l := s.lost()
	h := w.Header()
	if l == nil {
		h.Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	version := lostVersion(l)
	etag := `"` + version + `"`
	if r.URL.Query().Get("v") == version {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, ok := lostTile(l, d, zoom, x, y)
	if !ok {
		h.Del("ETag")
		h.Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	h.Set("Content-Type", "image/png")
	_, _ = w.Write(data)
}
