package export

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

const (
	// SchemaVersion changes when the shape of the answer does.
	SchemaVersion = 1

	// MaxLines and MaxSpan keep a window small enough to read whole, and to
	// fit a local model's context. A request over either is refused, never
	// cut short: a silently shortened window reads as the whole incident.
	MaxLines = 200
	MaxSpan  = time.Hour

	// maxScanned bounds the events read to build one window, so a busy unit
	// cannot make an export walk the table.
	maxScanned = 5000
	pageSize   = 200
)

// Gate is the part of auth.Auth the export needs.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

// Audit is the record kept of one export: who took which window and when.
type Audit struct {
	Operator string
	WindowID string
	Unit     string
	Job      string
	From     time.Time
	To       time.Time
	Lines    int
	At       time.Time
}

// API serves the export to signed-in operators only.
type API struct {
	store events.Store
	now   func() time.Time
	audit func(Audit)
}

// NewAPI builds the export over the same event store the explorer reads.
// audit is called once per successful export, before the answer is written.
func NewAPI(s events.Store, audit func(Audit)) *API {
	if audit == nil {
		audit = func(Audit) {}
	}
	return &API{store: s, now: time.Now, audit: audit}
}

func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	mux.HandleFunc("GET /v1/export/window", func(w http.ResponseWriter, r *http.Request) {
		op, ok := g.Operator(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "signed_out", "sign in as an operator")
			return
		}
		a.window(w, r, op)
	})
}

// Window is the answer. Its digest and id cover the bounds and the lines, so
// a draft that cites the id names exactly what the reader saw.
type Window struct {
	SchemaVersion    int       `json:"schema_version"`
	RedactionVersion int       `json:"redaction_version"`
	WindowID         string    `json:"window_id"`
	SHA256           string    `json:"sha256"`
	Unit             string    `json:"unit,omitempty"`
	Job              string    `json:"job,omitempty"`
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	Truncated        bool      `json:"truncated"`
	Omitted          int       `json:"omitted"`
	Lines            []Line    `json:"lines"`
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	body := map[string]any{"error": code, "message": msg, "max_lines": MaxLines, "max_span": MaxSpan.String()}
	write(w, status, body)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) window(w http.ResponseWriter, r *http.Request, op auth.Operator) {
	qs := r.URL.Query()
	unit, job := qs.Get("unit"), qs.Get("job")
	if (unit == "") == (job == "") {
		fail(w, http.StatusBadRequest, "bad_scope", "give exactly one of unit or job")
		return
	}
	q := events.Query{Asc: true, Limit: pageSize}
	if unit != "" {
		if !unitShape.MatchString(unit) {
			fail(w, http.StatusBadRequest, "bad_unit", "unit is not a unit id")
			return
		}
		q.Unit = unit
	} else {
		id, err := uuid.Parse(job)
		if err != nil {
			fail(w, http.StatusBadRequest, "bad_job", "job is not a job id")
			return
		}
		q.JobID = &id
	}
	to := a.now().UTC()
	if s := qs.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			fail(w, http.StatusBadRequest, "bad_time", "to must be an RFC 3339 time")
			return
		}
		to = t.UTC()
	}
	from := to.Add(-MaxSpan)
	if s := qs.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			fail(w, http.StatusBadRequest, "bad_time", "from must be an RFC 3339 time")
			return
		}
		from = t.UTC()
	}
	if from.After(to) {
		fail(w, http.StatusBadRequest, "bad_window", "from is after to")
		return
	}
	if to.Sub(from) > MaxSpan {
		fail(w, http.StatusUnprocessableEntity, "window_too_long", "the window spans more than "+MaxSpan.String())
		return
	}
	q.Since, q.Until = from, to

	raw, omitted, ok := a.read(w, r, q)
	if !ok {
		return
	}
	lines := merge(raw)
	if len(lines) > MaxLines {
		fail(w, http.StatusUnprocessableEntity, "window_too_large",
			fmt.Sprintf("the window holds %d distinct lines after merging repeats; narrow from and to", len(lines)))
		return
	}

	body, _ := json.Marshal(lines)
	if found := Scan(body); len(found) > 0 {
		// The allow-list should make this unreachable; if it fires, the
		// shapes above have a hole, and the safe answer is no answer.
		fail(w, http.StatusInternalServerError, "redaction_failed", "the window failed its safety scan and was withheld: "+found[0].Rule)
		return
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	scope := unit
	if scope == "" {
		scope = job
	}
	idSum := sha256.Sum256([]byte(fmt.Sprintf("%d|%d|%s|%s|%s|%s", SchemaVersion, RedactionVersion, scope,
		from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano), digest)))
	win := Window{
		SchemaVersion: SchemaVersion, RedactionVersion: RedactionVersion,
		WindowID: "win_" + hex.EncodeToString(idSum[:8]), SHA256: digest,
		Unit: unit, Job: job, From: from, To: to,
		Truncated: len(raw) > MaxLines && len(lines) <= MaxLines, Omitted: omitted, Lines: lines,
	}
	if win.Lines == nil {
		win.Lines = []Line{}
	}
	a.audit(Audit{Operator: op.ID, WindowID: win.WindowID, Unit: unit, Job: job, From: from, To: to, Lines: len(lines), At: a.now().UTC()})
	write(w, http.StatusOK, win)
}

// read walks the window oldest-first and redacts it. omitted counts events
// whose unit or kind was not of an exportable shape. Reading more than
// maxScanned events is an error for the caller, not a cut: the answer would
// otherwise start or stop mid-incident without saying so.
func (a *API) read(w http.ResponseWriter, r *http.Request, q events.Query) (lines []Line, omitted int, ok bool) {
	scanned := 0
	for {
		page, err := a.store.Query(r.Context(), q)
		if err != nil {
			if r.Context().Err() == nil {
				fail(w, http.StatusServiceUnavailable, "store_unavailable", "event store unavailable")
			}
			return nil, 0, false
		}
		for _, e := range page.Events {
			scanned++
			if l, keep := Redact(e); keep {
				lines = append(lines, l)
			} else {
				omitted++
			}
		}
		if page.Next == nil {
			return lines, omitted, true
		}
		if scanned >= maxScanned {
			fail(w, http.StatusUnprocessableEntity, "window_too_large",
				fmt.Sprintf("the window holds more than %d events; narrow from and to", maxScanned))
			return nil, 0, false
		}
		q.Cursor = *page.Next
	}
}
