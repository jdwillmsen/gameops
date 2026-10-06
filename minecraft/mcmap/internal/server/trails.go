package server

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/trails"
)

// TrailSource is where players have recently been.
type TrailSource interface {
	Trails(dimension, player string, since, now time.Time) trails.Reply
}

// maxGamertag is longer than any gamertag, and bounds what is compared.
const maxGamertag = 64

// handleTrails serves the trails of the players seen in a dimension. Every
// logged-in player already sees where every other player is, live; a trail
// is the same positions a little later, and is offered on the same terms.
func (s *Server) handleTrails(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dimension, player := q.Get("dimension"), q.Get("player")
	if !slices.Contains(render.Dimensions, dimension) {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	var since time.Time
	if raw := q.Get("since"); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds < 0 || seconds > 1<<40 {
			http.Error(w, "since is a time in Unix seconds", http.StatusBadRequest)
			return
		}
		since = time.Unix(seconds, 0)
	}
	if len(player) > maxGamertag {
		http.Error(w, "no such player", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, s.Trails.Trails(dimension, player, since, time.Now()))
}
