package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

const (
	// A tail polls this often, so a new event reaches a viewer within about
	// a second of being written, inside the two-second target.
	tailEvery = time.Second
	keepAlive = 15 * time.Second
	maxTails  = 16
	// A burst larger than this is cut into the next push.
	tailBatch = 200
	maxBody   = 4 << 10
)

// API serves the explorer to signed-in operators. Events carry unit
// coordinates, so nothing here is reachable signed out.
type API struct {
	src   Source
	views ViewStore
	gate  events.Gate
	now   func() time.Time
	every time.Duration
	tails atomic.Int32
}

func NewAPI(src Source, views ViewStore, gate events.Gate) *API {
	return &API{src: src, views: views, gate: gate, now: time.Now, every: tailEvery}
}

// Mount registers the explorer routes.
func (a *API) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/explorer/events", a.operator(a.query))
	mux.HandleFunc("GET /v1/explorer/tail", a.operator(a.tail))
	mux.HandleFunc("GET /v1/explorer/views", a.operator(a.listViews))
	mux.Handle("POST /v1/explorer/views", a.gate.Require(http.HandlerFunc(a.saveView)))
	mux.Handle("POST /v1/explorer/views/delete", a.gate.Require(http.HandlerFunc(a.deleteView)))
}

func (a *API) operator(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.gate.Operator(r); !ok {
			fail(w, http.StatusUnauthorized, "signed_out")
			return
		}
		h(w, r)
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}

type view struct {
	ID       int64           `json:"id"`
	Unit     string          `json:"unit"`
	At       time.Time       `json:"at"`
	Kind     string          `json:"kind"`
	Severity string          `json:"severity"`
	Task     string          `json:"task,omitempty"`
	Job      string          `json:"job,omitempty"`
	Pos      *events.Pos     `json:"pos,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

func viewOf(e events.Event) view {
	v := view{ID: e.ID, Unit: e.Unit, At: e.At, Kind: e.Kind, Severity: e.Severity, Pos: e.Pos, Payload: e.Payload}
	if e.TaskID != nil {
		v.Task = e.TaskID.String()
	}
	if e.JobID != nil {
		v.Job = e.JobID.String()
	}
	if len(v.Payload) == 0 {
		v.Payload = json.RawMessage("{}")
	}
	return v
}

func views(es []events.Event) []view {
	out := make([]view, len(es))
	for i, e := range es {
		out[i] = viewOf(e)
	}
	return out
}

func storeFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, events.ErrUnavailable):
		fail(w, http.StatusServiceUnavailable, "event store unavailable")
	default:
		fail(w, http.StatusInternalServerError, "event store error")
	}
}

func headID(es []events.Event) int64 {
	var m int64
	for _, e := range es {
		m = max(m, e.ID)
	}
	return m
}

func (a *API) query(w http.ResponseWriter, r *http.Request) {
	p, err := ParseParams(r.URL.Query())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	now := a.now()
	win := Window{Since: p.Filter.Since, Until: p.Filter.Until}
	if win.Since.IsZero() && p.Filter.Range > 0 {
		win.Since = now.Add(-p.Filter.Range)
	}
	loaded, err := a.src.Load(r.Context(), win)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	res, err := Run(loaded.Events, p.Filter, p.Sort, p.Cursor, p.Limit, now)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	out := struct {
		Events    []view `json:"events"`
		Total     int    `json:"total"`
		Next      string `json:"next,omitempty"`
		Head      int64  `json:"head"`
		Truncated bool   `json:"truncated,omitempty"`
	}{views(res.Events), res.Total, res.Next, headID(loaded.Events), loaded.Truncated}
	reply(w, http.StatusOK, out)
}

// tail streams events stored after the head the client last saw, narrowed by
// the same filter. It polls the source rather than hooking the writer, so it
// works unchanged over any store.
func (a *API) tail(w http.ResponseWriter, r *http.Request) {
	p, err := ParseParams(r.URL.Query())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var after int64
	if s := r.URL.Query().Get("after"); s != "" {
		if after, err = strconv.ParseInt(s, 10, 64); err != nil || after < 0 {
			fail(w, http.StatusBadRequest, "bad after")
			return
		}
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if a.tails.Add(1) > maxTails {
		a.tails.Add(-1)
		fail(w, http.StatusServiceUnavailable, "too many tails")
		return
	}
	defer a.tails.Add(-1)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(": tail\n\n"))
	fl.Flush()

	tick := time.NewTicker(a.every)
	defer tick.Stop()
	quiet := time.NewTimer(keepAlive)
	defer quiet.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-quiet.C:
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			fl.Flush()
			quiet.Reset(keepAlive)
		case <-tick.C:
			batch, head, err := a.fresh(r.Context(), p.Filter, after)
			if err != nil {
				// A store hiccup is retried on the next tick; the stream
				// stays open so the viewer does not flap.
				continue
			}
			if len(batch) == 0 {
				after = max(after, head)
				continue
			}
			if len(batch) > tailBatch {
				batch = batch[:tailBatch]
				head = batch[len(batch)-1].ID
			}
			b, _ := json.Marshal(struct {
				Events []view `json:"events"`
				Head   int64  `json:"head"`
			}{views(batch), head})
			_, _ = w.Write(append(append([]byte("event: events\ndata: "), b...), '\n', '\n'))
			fl.Flush()
			after = head
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(keepAlive)
		}
	}
}

// fresh returns the matching events stored after the id, oldest first, and
// the newest id seen whether or not it matched, so a quiet filter does not
// rescan the same rows forever.
func (a *API) fresh(ctx context.Context, f Filter, after int64) ([]events.Event, int64, error) {
	now := a.now()
	// A store that cannot snapshot is read over a short recent window; the
	// ID check is what makes the result exact.
	loaded, err := a.src.Load(ctx, Window{Since: now.Add(-time.Minute), AfterID: after})
	if err != nil {
		return nil, 0, err
	}
	c := f.Compile(now)
	var out []events.Event
	head := after
	for _, e := range loaded.Events {
		head = max(head, e.ID)
		if e.ID > after && c.Match(e) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, Sort{By: ByAt}.compare)
	return out, head, nil
}

func (a *API) listViews(w http.ResponseWriter, r *http.Request) {
	op, _ := a.gate.Operator(r)
	l, err := a.views.List(op.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "view store error")
		return
	}
	if l == nil {
		l = []View{}
	}
	reply(w, http.StatusOK, map[string]any{"views": l})
}

func readBody(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if dec.Decode(into) != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

func (a *API) saveView(w http.ResponseWriter, r *http.Request) {
	op, ok := a.gate.Operator(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "signed_out")
		return
	}
	var body struct{ Name, Query string }
	if !readBody(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > maxViewName {
		fail(w, http.StatusBadRequest, "name must be 1 to 60 characters")
		return
	}
	if len(body.Query) > maxViewQuery {
		fail(w, http.StatusBadRequest, "query too long")
		return
	}
	vals, err := url.ParseQuery(strings.TrimPrefix(body.Query, "?"))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad query")
		return
	}
	p, err := ParseParams(vals)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := a.views.Save(op.ID, name, p.Values().Encode())
	switch {
	case errors.Is(err, ErrViewLimit):
		fail(w, http.StatusConflict, "too many saved views")
	case err != nil:
		fail(w, http.StatusInternalServerError, "view store error")
	default:
		reply(w, http.StatusOK, v)
	}
}

func (a *API) deleteView(w http.ResponseWriter, r *http.Request) {
	op, ok := a.gate.Operator(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "signed_out")
		return
	}
	var body struct{ ID string }
	if !readBody(w, r, &body) {
		return
	}
	switch err := a.views.Delete(op.ID, body.ID); {
	case errors.Is(err, ErrViewNotFound):
		fail(w, http.StatusNotFound, "no such view")
	case err != nil:
		fail(w, http.StatusInternalServerError, "view store error")
	default:
		reply(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
