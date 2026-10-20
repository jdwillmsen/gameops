package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

const (
	listLen     = 50
	feedLen     = 20
	maxBody     = 4 << 10
	cancelTries = 5

	// A stock count this fresh is trusted when planning, so a chest that was
	// just looked at is not inspected again.
	planSince = 10 * time.Minute

	planDemo = "parallel-demo"
)

// Gate is the part of auth.Auth the operator routes need.
type Gate interface {
	Require(h http.Handler) http.Handler
	Operator(r *http.Request) (auth.Operator, bool)
}

// API serves jobs to signed-in operators. The ledger and scheduler exist only
// once the database is up, so get is asked per request and a nil answer is 503.
type API struct {
	get     func() (ledger.Ledger, *Scheduler)
	changed func()
	now     func() time.Time
	// retryEvery spaces the attempts to cancel a job a tick is holding.
	retryEvery time.Duration
}

// NewAPI: changed is called after a job is created, so streams hear of it
// before the scheduler's first tick; the scheduler reports its own changes.
func NewAPI(get func() (ledger.Ledger, *Scheduler), changed func()) *API {
	if changed == nil {
		changed = func() {}
	}
	return &API{get: get, changed: changed, now: time.Now, retryEvery: 200 * time.Millisecond}
}

type stepView struct {
	ID        uuid.UUID       `json:"id"`
	Key       string          `json:"key"`
	Unit      string          `json:"unit"`
	Template  json.RawMessage `json:"template"`
	DependsOn []string        `json:"depends_on"`
	State     string          `json:"state"`
	Attempts  int             `json:"attempts"`
	WorkID    *uuid.UUID      `json:"work_id"`
	Reason    string          `json:"reason"`
}

// jobView carries task templates, which hold coordinates, so it goes only to
// signed-in responses and is never logged.
type jobView struct {
	ID       uuid.UUID       `json:"id"`
	Kind     string          `json:"kind"`
	Spec     json.RawMessage `json:"spec"`
	State    string          `json:"state"`
	Reason   string          `json:"reason"`
	Operator string          `json:"operator"`
	Round    int             `json:"round"`
	Deadline time.Time       `json:"deadline"`
	Created  time.Time       `json:"created"`
	Updated  time.Time       `json:"updated"`
	Steps    []stepView      `json:"steps"`
}

func viewOf(j Job) jobView {
	v := jobView{j.ID, j.Kind, j.Spec, j.State, j.Reason, j.Operator, j.Round, j.Deadline, j.Created, j.Updated, make([]stepView, len(j.Steps))}
	for i, s := range j.Steps {
		deps := s.DependsOn
		if deps == nil {
			deps = []string{}
		}
		v.Steps[i] = stepView{s.ID, s.Key, s.Unit, s.Template, deps, s.State, s.Attempts, s.WorkID, s.Reason}
	}
	return v
}

func viewsOf(js []Job) []jobView {
	out := make([]jobView, len(js))
	for i, j := range js {
		out[i] = viewOf(j)
	}
	return out
}

// Feed renders the jobs event: the newest jobs, newest first.
func (a *API) Feed(ctx context.Context) ([]byte, error) {
	_, s := a.get()
	if s == nil {
		return nil, work.ErrUnavailable
	}
	js, err := s.Store().List(ctx, feedLen)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Jobs []jobView `json:"jobs"`
	}{viewsOf(js)})
}

// MountOperator registers the job routes. Without sign-in configured they
// still exist and answer 401.
func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	get := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, ok := g.Operator(r); !ok {
				writeError(w, http.StatusUnauthorized, "signed_out")
				return
			}
			h(w, r)
		}
	}
	mux.Handle("POST /v1/jobs", g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, _ := g.Operator(r)
		a.create(w, r, op.ID)
	})))
	mux.Handle("POST /v1/jobs/{id}/cancel", g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, _ := g.Operator(r)
		a.cancel(w, r, op.ID)
	})))
	mux.HandleFunc("GET /v1/jobs", get(a.list))
	mux.HandleFunc("GET /v1/jobs/{id}", get(a.one))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps an error to a status. Planning messages name the rule or the
// request's own place, never a stored value, so they are safe to return.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, ErrUnplannable):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "no such job")
	case errors.Is(err, work.ErrUnavailable), errors.Is(err, ledger.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "jobs unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "job error")
	}
}

func (a *API) stores(w http.ResponseWriter) (ledger.Ledger, *Scheduler, bool) {
	l, s := a.get()
	if l == nil || s == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs unavailable")
		return nil, nil, false
	}
	return l, s, true
}

func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

type createReq struct {
	Kind     string         `json:"kind"`
	Template string         `json:"template"`
	Place    string         `json:"place"`
	Want     map[string]int `json:"want"`
}

func (a *API) create(w http.ResponseWriter, r *http.Request, op string) {
	l, s, ok := a.stores(w)
	if !ok {
		return
	}
	var req createReq
	if err := decodeStrict(http.MaxBytesReader(w, r.Body, maxBody), &req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "job too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad job")
		return
	}
	now := a.now()
	j := Job{Kind: req.Kind, Operator: op, Deadline: now.Add(DefaultTimeout)}
	var err error
	switch req.Kind {
	case KindPlan:
		if req.Template != planDemo || req.Place != "" || req.Want != nil {
			writeError(w, http.StatusBadRequest, "only the parallel-demo plan exists")
			return
		}
		j.Spec = mustJSON(map[string]string{"template": planDemo})
		j.Steps, err = ParallelDemo(r.Context(), l)
	case KindStock:
		if req.Template != "" || req.Place == "" {
			writeError(w, http.StatusBadRequest, "a stock job takes a place and a want")
			return
		}
		spec := StockSpec{Place: req.Place, Want: req.Want}
		j.Spec = mustJSON(spec)
		j.Steps, err = PlanStock(r.Context(), l, spec, s.FreeUnits(r.Context()), now.Add(-planSince))
	default:
		writeError(w, http.StatusBadRequest, "kind must be plan or stock")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	j, err = s.Store().Create(r.Context(), j)
	if err != nil {
		fail(w, r, err)
		return
	}
	s.Wake()
	a.changed()
	writeJSON(w, http.StatusAccepted, viewOf(j))
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.stores(w)
	if !ok {
		return
	}
	js, err := s.Store().List(r.Context(), listLen)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewsOf(js))
}

func (a *API) one(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.stores(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	j, err := s.Store().Get(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(j))
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request, op string) {
	_, s, ok := a.stores(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	// A tick holds the job only for the length of one pass, so a short wait
	// is enough to get a turn.
	for try := 1; ; try++ {
		var j Job
		j, err = s.Cancel(r.Context(), id, op)
		if !errors.Is(err, ErrBusy) {
			if err != nil {
				fail(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, viewOf(j))
			return
		}
		if try == cancelTries {
			writeError(w, http.StatusConflict, "job busy, try again")
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(a.retryEvery):
		}
	}
}
