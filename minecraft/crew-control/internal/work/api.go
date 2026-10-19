package work

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

const (
	maxWait     = 25 * time.Second
	minWait     = time.Second
	maxEventLen = 1 << 10
)

// API serves the routes a unit calls. They are not public: the token is a
// shared secret and a request that came through the ingress is refused.
type API struct {
	store  Store
	units  roster.Roster
	token  [sha256.Size]byte
	notify func(unit string)

	mu    sync.Mutex
	waits map[string]chan struct{}
}

func Routes(mux *http.ServeMux, s Store, units roster.Roster, token string, notify func(unit string)) *API {
	if notify == nil {
		notify = func(string) {}
	}
	a := &API{store: s, units: units, token: sha256.Sum256([]byte(token)), notify: notify, waits: map[string]chan struct{}{}}
	// With no token configured nothing is accepted, rather than "Bearer ".
	mux.HandleFunc("GET /v1/units/{id}/work", a.guard(token != "", a.poll))
	mux.HandleFunc("POST /v1/units/{id}/events", a.guard(token != "", a.event))
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

func (a *API) guard(tokenSet bool, next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only the cluster-internal path is meant to reach these routes.
		if r.Header.Get("X-Forwarded-For") != "" {
			http.NotFound(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !tokenSet || !ok || subtle.ConstantTimeCompare(sum[:], a.token[:]) != 1 {
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

type wireItem struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Template json.RawMessage `json:"template,omitempty"`
	Command  json.RawMessage `json:"command,omitempty"`
}

func (a *API) poll(w http.ResponseWriter, r *http.Request, unit string) {
	timer := time.NewTimer(parseWait(r.URL.Query().Get("wait")))
	defer timer.Stop()
	for {
		woken := a.waiter(unit)
		it, ok, err := a.store.Next(r.Context(), unit, time.Now())
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
			return
		}
		select {
		case <-woken:
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			// Shutdown or a gone unit: nobody is left to read an answer.
			return
		}
	}
}

type report struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Progress int    `json:"progress"`
	Reason   string `json:"reason"`
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
	if _, err := a.store.Apply(r.Context(), unit, id, rep.State, rep.Progress, rep.Reason); err != nil {
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
