package server

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// HistorySource is the map as it was on the days that were kept.
type HistorySource interface {
	Days() []history.Day
	Tile(day, dimension string, tile render.Tile) (path, format string, ok bool)
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
