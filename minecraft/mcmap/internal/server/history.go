package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// HistorySource is the map as it was on the days that were kept.
type HistorySource interface {
	Days() []history.Day
	Tile(day, dimension string, tile render.Tile) (path, format string, ok bool)
	MarkersOn(day, dimension string) (path string, ok bool)
	Changes(day, dimension string) (history.Changes, bool)
}

func (s *Server) handleHistory(w http.ResponseWriter, _ *http.Request) {
	days := s.History.Days()
	if days == nil {
		days = []history.Day{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Days []history.Day `json:"days"`
	}{days})
}

func (s *Server) handleHistoryTile(w http.ResponseWriter, r *http.Request) {
	dimension := r.PathValue("dimension")
	if !slices.Contains(render.Dimensions, dimension) {
		http.NotFound(w, r)
		return
	}
	tile, ext, ok := tileAddress(r)
	if !ok {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	path, format, ok := s.History.Tile(r.PathValue("day"), dimension, tile)
	if !ok {
		// Short, as for the map's own gaps: the day may be one that is
		// about to be kept.
		w.Header().Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	if ext != format {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/"+format)
	// A day's tile is the same bytes for as long as the day is held.
	// Private, because a tile is only for the logged-in browser that asked.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", stat.ModTime(), f)
}

// handleHistoryMarkers serves a dimension's beds, containers and named mobs
// as they were on a day, in the form handleMarkers serves today's.
func (s *Server) handleHistoryMarkers(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	if !slices.Contains(render.Dimensions, dimension) {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	path, ok := s.History.MarkersOn(r.PathValue("day"), dimension)
	if !ok {
		// Short, as for a tile: a day held without its markers may yet be
		// given them.
		w.Header().Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	// With surfaces to put on them a day's markers are as the world's
	// surface is now, which is not fixed: the tag is the file's, which is
	// named by its content and so may be another dimension's too, the
	// dimension's, and the reading's.
	if s.Biomes != nil {
		s.serveMarkersWithSurface(w, r, path, dimension)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// A day's markers are the same bytes for as long as the day is held,
	// and are as private as a tile.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", stat.ModTime(), f)
}

func (s *Server) serveMarkersWithSurface(w http.ResponseWriter, r *http.Request, path, dimension string) {
	surface, reading := s.surfaceReading()
	etag := `"` + filepath.Base(path) + "-" + dimension + "-" + reading + `"`
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, err := s.surfaced.get("day|"+etag+"|"+dimension, func() ([]byte, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if d, named := dimensionNamed(dimension); surface != nil && named {
			return markers.WithSurface(raw, surfaceOf(surface, d))
		}
		return raw, nil
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// handleHistoryChanges says which tiles of a dimension differ between a day
// and the day held before it, for the page to outline.
func (s *Server) handleHistoryChanges(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	if !slices.Contains(render.Dimensions, dimension) {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	changes, ok := s.History.Changes(r.PathValue("day"), dimension)
	if !ok {
		// Short, as for a tile: the day may be one that is about to be kept.
		w.Header().Set("Cache-Control", "private, max-age=60")
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// The answer is of two days, and only the later is named in the address.
	// Neither day's tiles ever change, but which day is the one before can:
	// a backfill puts in days that are earlier than days already held. It
	// can only do that where a calendar day is free between the two, so the
	// answer is kept for good only where none is, the earlier day being the
	// day before; nothing is ever deleted that could part them again. Any
	// other answer is not kept at all, the first day's among them, which
	// any earlier day would change. That a day is the newest decides
	// nothing: a day kept after it is never the day before it.
	if changes.Settled {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	_ = json.NewEncoder(w).Encode(changes)
}
