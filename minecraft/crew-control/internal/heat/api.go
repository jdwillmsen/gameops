package heat

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

// Gate is the part of auth.Auth the route needs.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

// MountOperator registers the density route. Where units stand is
// operator-only data, so the session check comes before anything is parsed,
// and the answer is never cacheable.
func (s *Store) MountOperator(mux *http.ServeMux, g Gate, knownUnit func(string) bool) {
	mux.HandleFunc("GET /v1/heatmap", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Operator(r); !ok {
			reply(w, http.StatusUnauthorized, map[string]string{"error": "signed_out"})
			return
		}
		q := r.URL.Query()
		layer := Layer(q.Get("layer"))
		if layer == "" {
			layer = Position
		}
		if !ValidLayer(layer) {
			reply(w, http.StatusBadRequest, map[string]string{"error": "unknown layer"})
			return
		}
		window := q.Get("window")
		if window == "" {
			window = "1h"
		}
		if _, ok := Windows[window]; !ok {
			reply(w, http.StatusBadRequest, map[string]string{"error": "window must be 15m, 1h or 6h"})
			return
		}
		unit := q.Get("unit")
		if unit != "" && !knownUnit(unit) {
			reply(w, http.StatusNotFound, map[string]string{"error": "unknown unit"})
			return
		}
		reply(w, http.StatusOK, s.Query(unit, layer, window, time.Now()))
	})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
