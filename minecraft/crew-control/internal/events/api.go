package events

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

const (
	// The dry-run report lists a sample, not everything: a first prune after
	// a long-lived table could otherwise answer with a month of rows.
	pruneSample = 100

	// MinRetention is how long history is kept before the prune job drops
	// it: at least a month, so a console can replay more than the last few
	// days, and not much more, so an append-only table stays small.
	MinRetention = 30 * 24 * time.Hour

	maxBody = 1 << 10
)

// Gate is the part of auth.Auth the operator routes need.
type Gate interface {
	Require(h http.Handler) http.Handler
	Operator(r *http.Request) (auth.Operator, bool)
}

// API serves the history query and the retention route to signed-in
// operators. Nothing here accepts events: units do not post any today, and
// the table is filled from the poller.
type API struct {
	store Store
	now   func() time.Time
}

func NewAPI(s Store) *API { return &API{store: s, now: time.Now} }

// wireCursor is the cursor on the wire, so a client cannot be talked into
// sending anything but the pair the store handed it.
type wireCursor struct {
	At time.Time `json:"a"`
	ID int64     `json:"i"`
}

func encodeCursor(c Cursor) string {
	b, _ := json.Marshal(wireCursor(c))
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (Cursor, error) {
	var c Cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	var w wireCursor
	if json.Unmarshal(b, &w) != nil || w.At.IsZero() || w.ID == 0 {
		return c, errors.New("not a cursor")
	}
	return Cursor(w), nil
}

// view is an event as an operator sees it. It carries coordinates, so it
// goes only to signed-in responses and is never logged.
type view struct {
	ID       int64           `json:"id"`
	Unit     string          `json:"unit"`
	At       time.Time       `json:"at"`
	Kind     string          `json:"kind"`
	Severity string          `json:"severity"`
	Task     *uuid.UUID      `json:"task,omitempty"`
	Job      *uuid.UUID      `json:"job,omitempty"`
	Pos      *Pos            `json:"pos,omitempty"`
	Payload  json.RawMessage `json:"payload"`

	ReasonCode  string `json:"reason_code,omitempty"`
	DurationMS  *int   `json:"duration_ms,omitempty"`
	Attempt     *int   `json:"attempt,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	CrewVersion string `json:"crew_version,omitempty"`
	CauseRef    *int64 `json:"cause_ref,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
}

func viewOf(e Event) view {
	return view{ID: e.ID, Unit: e.Unit, At: e.At, Kind: e.Kind, Severity: e.Severity, Task: e.TaskID, Job: e.JobID,
		Pos: e.Pos, Payload: e.Payload, ReasonCode: e.ReasonCode, DurationMS: e.DurationMS, Attempt: e.Attempt,
		SessionID: e.SessionID, CrewVersion: e.CrewVersion, CauseRef: e.CauseRef, Outcome: e.Outcome}
}

// parseQuery reads the console's filter, sort and page parameters. Every
// value is optional; anything present but malformed is a 400, because a
// silently ignored filter would look like empty history.
func (a *API) parseQuery(r *http.Request) (Query, error) {
	q := Query{Limit: defaultLimit}
	get := func(name string) string { return r.URL.Query().Get(name) }
	if s := get("unit"); s != "" {
		q.Unit = s
	}
	if s := get("kind"); s != "" {
		q.Kind = s
	}
	if s := get("severity"); s != "" {
		if !validSeverity(s) {
			return q, errors.New("unknown severity")
		}
		q.Severity = s
	}
	if err := parseAnalyticFilters(get, &q); err != nil {
		return q, err
	}
	for name, into := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		if s := get(name); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return q, errors.New("bad time")
			}
			*into = t
		}
	}
	if s := get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return q, errors.New("bad limit")
		}
		q.Limit = n
	}
	switch get("sort") {
	case "", "desc":
		q.Asc = false
	case "asc":
		q.Asc = true
	default:
		return q, errors.New("sort must be asc or desc")
	}
	if s := get("cursor"); s != "" {
		c, err := decodeCursor(s)
		if err != nil {
			return q, errors.New("bad cursor")
		}
		q.Cursor = c
	}
	return q, nil
}

// MountOperator registers the history routes. Without sign-in configured
// they still exist and answer 401. Positions are in every answer, so the
// session check is the whole of the difference between an operator and a
// stranger.
func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Operator(r); !ok {
			opError(w, http.StatusUnauthorized, "signed_out")
			return
		}
		a.history(w, r)
	})
	mux.Handle("POST /v1/events/prune", g.Require(http.HandlerFunc(a.prune)))
}

func opJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func opError(w http.ResponseWriter, status int, msg string) {
	opJSON(w, status, map[string]string{"error": msg})
}

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	q, err := a.parseQuery(r)
	if err != nil {
		opError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := a.store.Query(r.Context(), q)
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	out := struct {
		Events []view  `json:"events"`
		Next   *string `json:"next,omitempty"`
	}{Events: make([]view, len(page.Events))}
	for i, e := range page.Events {
		out.Events[i] = viewOf(e)
	}
	if page.Next != nil {
		next := encodeCursor(*page.Next)
		out.Next = &next
	}
	opJSON(w, http.StatusOK, out)
}

func (a *API) prune(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DryRun bool `json:"dry_run"`
	}
	// An empty body is an ordinary prune; anything else must be the one
	// field this route takes.
	if b, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody)); len(b) > 0 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil {
			opError(w, http.StatusBadRequest, "bad prune request")
			return
		}
	}
	rep, err := a.store.Prune(r.Context(), a.now().Add(-MinRetention), pruneSample, body.DryRun)
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	out := struct {
		Pruned int    `json:"pruned"`
		Total  int    `json:"total"`
		Would  []view `json:"would"`
	}{Pruned: rep.Pruned, Total: rep.Total, Would: make([]view, len(rep.Would))}
	for i, e := range rep.Would {
		out.Would[i] = viewOf(e)
	}
	opJSON(w, http.StatusOK, out)
}

func opStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, ErrUnavailable):
		opError(w, http.StatusServiceUnavailable, "event store unavailable")
	case errors.Is(err, ErrInvalid):
		opError(w, http.StatusBadRequest, "filter not supported by the event store")
	default:
		opError(w, http.StatusInternalServerError, "event store error")
	}
}
