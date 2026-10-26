package invhistory

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

const (
	DefaultDays = 7
	// MaxDays matches how long the event store keeps history.
	MaxDays = 30
)

// Gate is the part of auth.Auth the route needs.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

// API serves a unit's inventory history to signed-in operators. What a unit
// carries is operator-only everywhere else, so this is too.
type API struct {
	store events.Store
	has   func(unit string) bool
	now   func() time.Time
}

func NewAPI(s events.Store, has func(string) bool) *API {
	return &API{store: s, has: has, now: time.Now}
}

func (a *API) Mount(mux *http.ServeMux, g Gate) {
	mux.HandleFunc("GET /v1/units/{id}/inventory/changes", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Operator(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signed_out"})
			return
		}
		a.changes(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) changes(w http.ResponseWriter, r *http.Request) {
	unit := r.PathValue("id")
	if !a.has(unit) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such unit"})
		return
	}
	days := DefaultDays
	if s := r.URL.Query().Get("days"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxDays {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "days must be 1 to " + strconv.Itoa(MaxDays)})
			return
		}
		days = n
	}
	until := a.now()
	rep, err := Read(r.Context(), a.store, unit, until.AddDate(0, 0, -days), until)
	if err != nil {
		if r.Context().Err() == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "event store unavailable"})
		}
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
