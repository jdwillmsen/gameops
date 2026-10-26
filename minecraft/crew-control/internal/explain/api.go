package explain

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/anon"
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
//
// Each request scans the event log, so anonymous callers get a one-hour cap
// and a short shared cache; signed-in operators get the full 24 h, uncached.
type API struct {
	store events.Store
	now   func() time.Time
	gate  anon.Gate
	cache *anon.Cache
}

func NewAPI(s events.Store) *API {
	return &API{store: s, now: time.Now, cache: anon.NewCache()}
}

func (a *API) Mount(mux *http.ServeMux, g anon.Gate) {
	a.gate = g
	mux.HandleFunc("GET /v1/units/{id}/explain", a.explain)
}

func span(d time.Duration) string {
	if d >= 24*time.Hour {
		return "24h"
	}
	return "1h"
}

func failReply(status int, msg string) anon.Reply {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return anon.Reply{Status: status, Body: b}
}

// window reads from and to. snap rounds explicit bounds down to whole minutes
// before they are used, so callers varying a bound by seconds share a result.
func (a *API) window(r *http.Request, limit time.Duration, snap bool) (from, to time.Time, ok bool) {
	to = a.now()
	if s := r.URL.Query().Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, false
		}
		to = t
		if snap {
			to = to.Truncate(time.Minute)
		}
	}
	from = to.Add(-defaultWindow)
	if s := r.URL.Query().Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, false
		}
		from = t
		if snap {
			from = from.Truncate(time.Minute)
		}
	}
	return from, to, !from.After(to) && to.Sub(from) <= limit
}

func (a *API) explain(w http.ResponseWriter, r *http.Request) {
	signed := anon.Signed(a.gate, r)
	limit := maxWindow
	if !signed {
		limit = anon.MaxWindow
	}
	from, to, ok := a.window(r, limit, !signed)
	if !ok {
		anon.Write(w, failReply(http.StatusBadRequest, "from and to must be RFC 3339 times, in order, at most "+span(limit)+" apart"))
		return
	}
	unit := r.PathValue("id")
	q := r.URL.Query()
	scan := func() (anon.Reply, bool) { return a.scan(r, unit, from, to) }
	if signed {
		rep, _ := scan()
		anon.Write(w, rep)
		return
	}
	fromKey, toKey := "default", "now"
	if q.Get("from") != "" {
		fromKey = from.Format(time.RFC3339)
	}
	if q.Get("to") != "" {
		toKey = to.Format(time.RFC3339)
	}
	key := unit + "|" + fromKey + "|" + toKey
	anon.Write(w, a.cache.Do(key, scan))
}

// scan reads the unit's events and explains them. The second result says the
// answer may be kept for other callers.
func (a *API) scan(r *http.Request, unit string, from, to time.Time) (anon.Reply, bool) {
	q := events.Query{Unit: unit, Since: from.Add(-lookback), Until: to, Limit: pageSize}
	var es []unitlog.Event
	truncated := false
	for i := 0; ; i++ {
		page, err := a.store.Query(r.Context(), q)
		if err != nil {
			return failReply(http.StatusServiceUnavailable, "event store unavailable"), false
		}
		es = append(es, unitlog.FromAll(page.Events)...)
		if page.Next == nil {
			break
		}
		if i+1 >= maxPages {
			truncated = true
			break
		}
		q.Cursor = *page.Next
	}
	slices.Reverse(es)
	res := Explain(unit, from, to, es)
	res.Truncated = truncated
	b, err := json.Marshal(res)
	if err != nil {
		return failReply(http.StatusInternalServerError, "encode failed"), false
	}
	return anon.Reply{Status: http.StatusOK, Body: b}, true
}
