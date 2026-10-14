package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

// MarkerStore is what the last scan of the world found worth marking, one
// encoded answer per dimension.
type MarkerStore interface {
	Dimension(name string) (body []byte, etag string, ok bool)
}

// WaypointSource reads one player's own waypoints.
type WaypointSource interface {
	Waypoints(ctx context.Context, xuid string) (list []markers.Waypoint, more int, err error)
}

// handleMarkers serves a dimension's beds, containers and named mobs. They
// are the same for every player and change once a snapshot, so the browser
// keeps its copy and asks only whether it is still current.
func (s *Server) handleMarkers(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	body, etag, ok := s.Markers.Dimension(dimension)
	if !ok {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	// With the surface under each marker, the answer changes whenever the
	// reading of the world does, and the tag says so.
	surface, reading := s.surfaceReading()
	if surface != nil {
		etag = etag[:len(etag)-1] + "-" + reading + `"`
	}
	h := w.Header()
	// Private, because it is only for the logged-in browser that asked;
	// no-cache, so that every use of the copy is checked against the tag.
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if d, named := dimensionNamed(dimension); surface != nil && named {
		var err error
		if body, err = s.surfaced.get("markers|"+dimension+"|"+etag, func() ([]byte, error) {
			return markers.WithSurface(body, surfaceOf(surface, d))
		}); err != nil {
			http.Error(w, "the markers could not be written out", http.StatusInternalServerError)
			return
		}
	}
	h.Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

type waypointsJSON struct {
	Waypoints []markers.Waypoint `json:"waypoints"`
	More      int                `json:"more"`
}

// handleWaypoints serves the waypoints of whoever the session says is
// asking. Whose they are comes from the session and nowhere else: nothing
// in the request can name another player.
func (s *Server) handleWaypoints(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	list, more, err := s.Waypoints.Waypoints(r.Context(), id.XUID)
	switch {
	case errors.Is(err, markers.ErrBusy):
		w.Header().Set("Retry-After", "5")
		http.Error(w, "waypoints are busy", http.StatusServiceUnavailable)
		return
	case err != nil:
		// The detail names an internal address, so it stays in the log.
		s.log().Warn("waypoints not read", "xuid", id.XUID, "error", err.Error())
		http.Error(w, "waypoints are unavailable", http.StatusBadGateway)
		return
	}
	if list == nil {
		list = []markers.Waypoint{}
	}
	writeJSON(w, http.StatusOK, waypointsJSON{Waypoints: list, More: more})
}
