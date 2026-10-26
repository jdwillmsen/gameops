package capacity

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/anon"
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
// template name, no position, so it is public like the fleet totals. Each
// request scans the event log, so anonymous callers get a one-hour cap and a
// short shared cache; signed-in operators get the full 24 h, uncached.
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
	mux.HandleFunc("GET /v1/capacity", a.get)
}

func failReply(status int, msg string) anon.Reply {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return anon.Reply{Status: status, Body: b}
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	signed := anon.Signed(a.gate, r)
	limit := maxWindow
	if !signed {
		limit = anon.MaxWindow
	}
	win := defaultWindow
	if s := r.URL.Query().Get("window"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < minWindow || d > limit {
			anon.Write(w, failReply(http.StatusBadRequest, "window must be a duration between 1m and "+limit.String()))
			return
		}
		win = d
	}
	scan := func() (anon.Reply, bool) { return a.scan(r, win) }
	if signed {
		rep, _ := scan()
		anon.Write(w, rep)
		return
	}
	anon.Write(w, a.cache.Do(win.String(), scan))
}

// scan reads the window's completions and failures. The second result says
// the answer may be kept for other callers.
func (a *API) scan(r *http.Request, win time.Duration) (anon.Reply, bool) {
	to := a.now()
	from := to.Add(-win)
	var es []unitlog.Event
	truncated := false
	for _, kind := range []string{unitlog.KindTaskDone, unitlog.KindTaskFailed} {
		q := events.Query{Kind: kind, Since: from, Until: to, Limit: pageSize, Asc: true}
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
	}
	rep := Compute(from, to, es)
	rep.Truncated = truncated
	b, err := json.Marshal(rep)
	if err != nil {
		return failReply(http.StatusInternalServerError, "encode failed"), false
	}
	return anon.Reply{Status: http.StatusOK, Body: b}, true
}
