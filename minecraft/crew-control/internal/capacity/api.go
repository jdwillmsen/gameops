package capacity

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

const (
	defaultWindow = time.Hour
	minWindow     = time.Minute
	maxWindow     = 24 * time.Hour
	pageSize      = 200
	maxPages      = 100
)

// API serves GET /v1/capacity. The report holds counts and durations per
// template name, no position, so it is public like the fleet totals.
type API struct {
	store events.Store
	now   func() time.Time
}

func NewAPI(s events.Store) *API { return &API{store: s, now: time.Now} }

func (a *API) Mount(mux *http.ServeMux) { mux.HandleFunc("GET /v1/capacity", a.get) }

func bad(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	win := defaultWindow
	if s := r.URL.Query().Get("window"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < minWindow || d > maxWindow {
			bad(w, http.StatusBadRequest, "window must be a duration between 1m and 24h")
			return
		}
		win = d
	}
	to := a.now()
	from := to.Add(-win)
	var es []unitlog.Event
	for _, kind := range []string{unitlog.KindTaskDone, unitlog.KindTaskFailed} {
		q := events.Query{Kind: kind, Since: from, Until: to, Limit: pageSize, Asc: true}
		for i := 0; i < maxPages; i++ {
			page, err := a.store.Query(r.Context(), q)
			if err != nil {
				if r.Context().Err() == nil {
					bad(w, http.StatusServiceUnavailable, "event store unavailable")
				}
				return
			}
			es = append(es, unitlog.FromAll(page.Events)...)
			if page.Next == nil {
				break
			}
			q.Cursor = *page.Next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(Compute(from, to, es))
}
