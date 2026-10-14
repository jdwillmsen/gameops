package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/topo"
)

// topographyVersion names a reading of the heights. A tile asked for with
// it as v may be kept by the browser for good: a new reading is a new
// version.
func topographyVersion(h *heights.Map) string {
	return strconv.FormatInt(h.SnapshotAt.Unix(), 36)
}

// topographyStyles are the drawings a tile may be asked for, by the name
// the page asks with.
var topographyStyles = map[string]topo.Styles{"hillshade": topo.Shade, "contours": topo.Contours, "tint": topo.Tint}

type topographyJSON struct {
	// Offered is false in a dimension that has no land to draw: the
	// heightmap of the Nether is the bedrock roof over all of it.
	Offered bool `json:"offered"`
	// Ready is false until the heights have been read once.
	Ready bool `json:"ready"`
	// Version names the reading, as the biomes' does.
	Version string `json:"version,omitempty"`
	Tiles   struct {
		MinZoom int `json:"minZoom"`
		MaxZoom int `json:"maxZoom"`
		Size    int `json:"size"`
	} `json:"tiles"`
	// Intervals is the height between two contour lines in blocks, by how
	// many zooms out from the finest a tile is, and HeavyEvery how many
	// lines there are to a heavy one.
	Intervals  []int `json:"intervals"`
	HeavyEvery int   `json:"heavyEvery"`
	// Bands are the tint's colours and the heights each is, lowest first,
	// which is the legend.
	Bands []topo.Band `json:"bands"`
}

// handleTopography says what the overlay holds for a dimension: whether it
// is drawn there, how far in and out, how far apart its lines are and what
// its colours mean.
func (s *Server) handleTopography(w http.ResponseWriter, r *http.Request) {
	d, ok := dimensionNamed(r.URL.Query().Get("dimension"))
	if !ok {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	out := topographyJSON{Offered: topo.Offered(d), HeavyEvery: topo.HeavyEvery, Bands: []topo.Band{}, Intervals: []int{}}
	out.Tiles.MinZoom, out.Tiles.MaxZoom, out.Tiles.Size = topo.MinZoom, topo.MaxZoom, topo.TileSize
	if out.Offered {
		out.Bands = topo.Bands(d)
		for zoom := 0; zoom >= topo.MinZoom; zoom-- {
			out.Intervals = append(out.Intervals, topo.Interval(zoom))
		}
		if h := s.Biomes.World().Heights(); h != nil {
			out.Ready, out.Version = true, topographyVersion(h)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// topographyPick reads which drawings a tile is asked for, and which bands
// of the tint are left out. A band is named by its place in the dimension's
// list, from 0. It says how the pick is in the tile's tag, the same for the
// same pick however it was written.
func topographyPick(q map[string][]string, bands int) (pick topo.Pick, tag string, ok bool) {
	raw := q["styles"]
	if len(raw) != 1 || raw[0] == "" || len(raw[0]) > 64 {
		return topo.Pick{}, "", false
	}
	for name := range strings.SplitSeq(raw[0], ",") {
		style, known := topographyStyles[name]
		if !known {
			return topo.Pick{}, "", false
		}
		pick.Styles |= style
	}
	tag = strconv.Itoa(int(pick.Styles))
	if hide, given := q["hide"]; given {
		if len(hide) != 1 || len(hide[0]) > 64 {
			return topo.Pick{}, "", false
		}
		if hide[0] != "" {
			for name := range strings.SplitSeq(hide[0], ",") {
				n, err := strconv.Atoi(name)
				if err != nil || n < 0 || n >= bands {
					return topo.Pick{}, "", false
				}
				// What is hidden of a tint that is not drawn changes
				// nothing, and is not a picture of its own.
				if pick.Styles&topo.Tint != 0 {
					pick.Hidden |= 1 << n
				}
			}
		}
	}
	return pick, tag + "." + strconv.FormatUint(uint64(pick.Hidden), 36), true
}

// handleTopographyTile draws one tile of the overlay, in whichever of its
// styles are asked for, as one picture. It is drawn when asked for, as the
// biomes' are: a tile takes a millisecond or a few from what is already in
// memory, which is less than reading one from the volume would save.
func (s *Server) handleTopographyTile(w http.ResponseWriter, r *http.Request) {
	d, ok := dimensionNamed(r.PathValue("dimension"))
	if !ok || !topo.Offered(d) {
		http.NotFound(w, r)
		return
	}
	zoom, errZ := strconv.Atoi(r.PathValue("zoom"))
	x, errX := strconv.Atoi(r.PathValue("x"))
	name, ext, _ := strings.Cut(r.PathValue("y"), ".")
	y, errY := strconv.Atoi(name)
	if errZ != nil || errX != nil || errY != nil || ext != "png" ||
		zoom < topo.MinZoom || zoom > topo.MaxZoom ||
		x < -maxTileCoordinate || x > maxTileCoordinate || y < -maxTileCoordinate || y > maxTileCoordinate {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	pick, picked, ok := topographyPick(r.URL.Query(), len(topo.Bands(d)))
	if !ok {
		http.Error(w, "a tile's drawings are asked for by styles, a list of hillshade, contours and tint, and its bands left out by hide, a list of the numbers of the bands", http.StatusBadRequest)
		return
	}

	h := w.Header()
	surface := s.Biomes.World().Heights()
	if surface == nil {
		h.Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	version := topographyVersion(surface)
	etag := `"` + version + "-" + picked + `"`
	// Private, because a tile is only for the logged-in browser that asked.
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
	data, ok := topo.Tile(surface, d, zoom, x, y, pick)
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
