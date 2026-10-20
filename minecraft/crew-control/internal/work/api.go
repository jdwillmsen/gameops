package work

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

const (
	maxWait = 25 * time.Second
	minWait = time.Second
	// The cap was 1 KiB for a bare state report. A report can now carry what
	// a unit saw in a chest, up to 27 item kinds at about 40 bytes each plus
	// the position and time, which would not fit. Four times the old cap is
	// room for that and still too small to be a place to park data.
	maxEventLen = 4 << 10

	// A chest holds 27 stacks, so more kinds than that is not an observation.
	maxSeenItems = 27
)

var seenItemName = regexp.MustCompile(`^[a-z_]{1,32}$`)

// API serves the routes a unit calls. They are not public: the token is a
// shared secret and a request that came through the ingress is refused.
type API struct {
	store  Store
	units  roster.Roster
	token  [sha256.Size]byte
	notify func(unit string)
	now    func() time.Time

	// OnSeen receives what a unit saw, before its report is applied.
	// OnTerminal receives an item that carries a job once a report leaves it
	// finished, including a task cancelled because its cancel_task was
	// reported done. It is a wake hint only: a repeated report fires it
	// again, it is not fired for cancels done in place or by CancelJob, and
	// it is lost if the process dies right after the store write. A consumer
	// must re-read the store rather than trust what it was handed. Both
	// hooks are set before serving and run on the request, so must not block.
	OnSeen     func(unit string, s Seen)
	OnTerminal func(it Item)

	mu    sync.Mutex
	waits map[string]chan struct{}
}

func Routes(mux *http.ServeMux, s Store, units roster.Roster, token string, notify func(unit string)) *API {
	if notify == nil {
		notify = func(string) {}
	}
	a := &API{store: s, units: units, token: sha256.Sum256([]byte(token)), notify: notify, now: time.Now, waits: map[string]chan struct{}{}}
	// With no token configured the routes do not exist, rather than existing
	// to refuse; a deployment that lost its secret looks like one without
	// the feature.
	if token == "" {
		return a
	}
	mux.HandleFunc("GET /v1/units/{id}/work", a.guard(a.poll))
	mux.HandleFunc("POST /v1/units/{id}/events", a.guard(a.event))
	return a
}

// Wake ends a waiting poll for unit so it can pick up new work.
func (a *API) Wake(unit string) {
	a.mu.Lock()
	if ch, ok := a.waits[unit]; ok {
		close(ch)
		delete(a.waits, unit)
	}
	a.mu.Unlock()
}

// waiter returns a channel closed by the next Wake. It is taken before the
// store is read, so work enqueued in between is not missed.
func (a *API) waiter(unit string) <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, ok := a.waits[unit]
	if !ok {
		ch = make(chan struct{})
		a.waits[unit] = ch
	}
	return ch
}

func (a *API) guard(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only the cluster-internal path is meant to reach these routes.
		if r.Header.Get("X-Forwarded-For") != "" {
			http.NotFound(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], a.token[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if _, ok := a.units.Get(id); !ok {
			http.NotFound(w, r)
			return
		}
		next(w, r, id)
	}
}

func parseWait(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return maxWait
	}
	return max(minWait, min(d, maxWait))
}

func pollKinds(r *http.Request) (string, bool) {
	values, ok := r.URL.Query()["kinds"]
	if !ok {
		return "", true
	}
	if len(values) != 1 || values[0] != "command" {
		return "", false
	}
	return "command", true
}

type wireItem struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Template json.RawMessage `json:"template,omitempty"`
	Command  json.RawMessage `json:"command,omitempty"`
}

func (a *API) poll(w http.ResponseWriter, r *http.Request, unit string) {
	kinds, ok := pollKinds(r)
	if !ok {
		http.Error(w, "bad kinds", http.StatusBadRequest)
		return
	}
	timer := time.NewTimer(parseWait(r.URL.Query().Get("wait")))
	defer timer.Stop()
	for {
		woken := a.waiter(unit)
		it, ok, due, err := a.store.Next(r.Context(), unit, kinds, a.now())
		if err != nil {
			storeError(w, r, err)
			return
		}
		if ok {
			wi := wireItem{ID: it.ID.String(), Kind: it.Kind}
			if it.Kind == KindCommand {
				wi.Command = it.Body
			} else {
				wi.Template = it.Body
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(wi)
			a.notify(unit)
			return
		}
		// An item delivered earlier may fall due again before the wait ends.
		var (
			redeliver <-chan time.Time
			rt        *time.Timer
		)
		if due > 0 {
			rt = time.NewTimer(due)
			redeliver = rt.C
		}
		select {
		case <-woken:
		case <-redeliver:
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			// Shutdown or a gone unit: nobody is left to read an answer.
			return
		}
		if rt != nil {
			rt.Stop()
		}
	}
}

type report struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Progress int    `json:"progress"`
	Reason   string `json:"reason"`
	// Raw so a malformed observation can be dropped without failing the
	// state report that carries it.
	Seen json.RawMessage `json:"seen"`
}

// parseSeen returns false for anything that is not a well-formed observation.
func parseSeen(raw json.RawMessage) (Seen, bool) {
	var s Seen
	if len(raw) == 0 || string(raw) == "null" {
		return s, false
	}
	// Pointers so a missing coordinate is told apart from zero: a chest at
	// the origin is a place, an absent one is not an observation.
	var w struct {
		Chest *struct {
			X, Y, Z *int
		} `json:"chest"`
		Items map[string]int `json:"items"`
		At    time.Time      `json:"at"`
	}
	if decodeStrict(bytes.NewReader(raw), &w) != nil || w.At.IsZero() || len(w.Items) > maxSeenItems ||
		w.Chest == nil || w.Chest.X == nil || w.Chest.Y == nil || w.Chest.Z == nil {
		return Seen{}, false
	}
	for name, n := range w.Items {
		if !seenItemName.MatchString(name) || n < 0 {
			return Seen{}, false
		}
	}
	s.Chest.X, s.Chest.Y, s.Chest.Z = *w.Chest.X, *w.Chest.Y, *w.Chest.Z
	s.Items, s.At = w.Items, w.At
	if s.Items == nil {
		s.Items = map[string]int{}
	}
	return s, true
}

func (a *API) event(w http.ResponseWriter, r *http.Request, unit string) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEventLen))
	dec.DisallowUnknownFields()
	var rep report
	err := dec.Decode(&rep)
	if err == nil {
		// A second value, or garbage after the first, is not our format.
		if _, e := dec.Token(); !errors.Is(e, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "event too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(rep.ID)
	if err != nil {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	// Before Apply, so a tick that sees the final inspect done also sees the
	// stock it reported. Only the unit's own item counts, and a failed lookup
	// just skips the hook: Apply reports the real error below.
	if seen, ok := parseSeen(rep.Seen); ok && a.OnSeen != nil {
		if own, err := a.store.Get(r.Context(), id); err == nil && own.Unit == unit {
			a.OnSeen(unit, seen)
		}
	}
	it, err := a.store.Apply(r.Context(), unit, id, rep.State, rep.Progress, rep.Reason)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrWrongUnit):
			http.NotFound(w, r)
		case errors.Is(err, ErrInvalid):
			http.Error(w, "bad event", http.StatusBadRequest)
		default:
			storeError(w, r, err)
		}
		return
	}
	if a.OnTerminal != nil {
		if it.JobID != nil && terminal(it.State) {
			a.OnTerminal(it)
		}
		if target, ok := cancelTarget(it); ok && it.State == Done {
			// Best effort: the wake is only a hint, and the cancel is stored.
			if t, err := a.store.Get(r.Context(), target); err == nil && t.JobID != nil && t.State == Cancelled {
				a.OnTerminal(t)
			}
		}
	}
	a.notify(unit)
	w.WriteHeader(http.StatusNoContent)
}

func storeError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil || errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, ErrUnavailable) {
		http.Error(w, "work store unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "work store error", http.StatusInternalServerError)
}
