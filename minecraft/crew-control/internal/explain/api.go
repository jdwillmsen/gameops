package explain

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

const (
	defaultWindow = time.Hour
	maxWindow     = 24 * time.Hour
	// lookback reaches before the window so a failure at its start still
	// finds its predecessor.
	lookback = kitWindow
	pageSize = 200
	maxPages = 100
)

// API serves the explainer. The payload holds event ids and sentences, never
// a position, so the same answer goes to anonymous and signed-in viewers.
type API struct {
	store events.Store
	now   func() time.Time
}

func NewAPI(s events.Store) *API { return &API{store: s, now: time.Now} }

func (a *API) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/units/{id}/explain", a.explain)
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (a *API) window(r *http.Request) (from, to time.Time, ok bool) {
	to = a.now()
	if s := r.URL.Query().Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, false
		}
		to = t
	}
	from = to.Add(-defaultWindow)
	if s := r.URL.Query().Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, false
		}
		from = t
	}
	return from, to, !from.After(to) && to.Sub(from) <= maxWindow
}

func (a *API) explain(w http.ResponseWriter, r *http.Request) {
	from, to, ok := a.window(r)
	if !ok {
		fail(w, http.StatusBadRequest, "from and to must be RFC 3339 times, in order, at most 24h apart")
		return
	}
	unit := r.PathValue("id")
	q := events.Query{Unit: unit, Since: from.Add(-lookback), Until: to, Limit: pageSize, Asc: true}
	var es []unitlog.Event
	for i := 0; i < maxPages; i++ {
		page, err := a.store.Query(r.Context(), q)
		if err != nil {
			if r.Context().Err() == nil {
				fail(w, http.StatusServiceUnavailable, "event store unavailable")
			}
			return
		}
		es = append(es, unitlog.FromAll(page.Events)...)
		if page.Next == nil {
			break
		}
		q.Cursor = *page.Next
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(Explain(unit, from, to, es))
}
