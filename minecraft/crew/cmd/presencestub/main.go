// Command presencestub stands in for the server agent's presence API on the
// crew lab, which has no agent. An idle-mode unit polls it exactly as an FWB
// AFK bot polls the agent, and a person parks or unparks the unit with one
// HTTP call, so park and unpark can be proven before FWB.
//
// It is for the lab only: it checks no token, keeps everything in memory and
// knows nothing of overrides, expiry or wake-on-join.
//
// usage: presencestub [-addr :8090] [-default present|parked]
//
//	PUT /stub/actors/{id}?state=parked   park (or state=present to unpark)
//	GET /stub/actors/{id}                the state and the unit's last report
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/internal/presenceapi"
)

// A status report is a few hundred bytes; the cap only stops a runaway body.
const maxBody = 64 << 10

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("presencestub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8090", "listen address")
	def := fs.String("default", string(presenceapi.StatePresent), "state of an actor nobody has set: present or parked")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !presenceapi.State(*def).Valid() {
		fmt.Fprintf(stderr, "presencestub: -default must be %q or %q\n", presenceapi.StatePresent, presenceapi.StateParked)
		return 2
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	srv := &http.Server{Addr: *addr, Handler: newStub(presenceapi.State(*def), log).routes(), ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("listening", "addr", *addr, "default", *def)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve_failed", "error", err.Error())
		return 1
	}
	return 0
}

type actor struct {
	state presenceapi.State
	// version moves on every change, and the ETag is built from it, so a
	// unit's conditional poll sees a change on its next request.
	version int64
	last    *presenceapi.Status
	lastAt  time.Time
}

type stub struct {
	def presenceapi.State
	log *slog.Logger
	now func() time.Time

	mu     sync.Mutex
	actors map[string]*actor
}

func newStub(def presenceapi.State, log *slog.Logger) *stub {
	return &stub{def: def, log: log, now: time.Now, actors: map[string]*actor{}}
}

// get must be called with mu held.
func (s *stub) get(id string) *actor {
	a, ok := s.actors[id]
	if !ok {
		a = &actor{state: s.def, version: 1}
		s.actors[id] = a
	}
	return a
}

func (s *stub) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/actors/{id}/presence", s.presence)
	mux.HandleFunc("POST /v1/actors/{id}/status", s.status)
	mux.HandleFunc("PUT /stub/actors/{id}", s.set)
	mux.HandleFunc("GET /stub/actors/{id}", s.show)
	return mux
}

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

func (s *stub) presence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	a := s.get(id)
	p := presenceapi.Presence{ActorID: id, Effective: a.state, Default: s.def}
	tag := etag(a.version)
	s.mu.Unlock()

	w.Header().Set("ETag", tag)
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *stub) status(w http.ResponseWriter, r *http.Request) {
	var st presenceapi.Status
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, presenceapi.Error{Code: "bad_request", Message: err.Error()})
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	a := s.get(id)
	changed := a.last == nil || a.last.Connected != st.Connected || a.last.ObservedState != st.ObservedState
	a.last, a.lastAt = &st, s.now().UTC()
	s.mu.Unlock()
	// Only a change is logged: the unit reports on every poll, and the lab
	// proof reads this log for the moment it obeyed a park.
	if changed {
		s.log.Info("status_changed", "actor", id, "connected", st.Connected, "observed_state", string(st.ObservedState), "version", st.ProcessVersion)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *stub) set(w http.ResponseWriter, r *http.Request) {
	state := presenceapi.State(r.URL.Query().Get("state"))
	if !state.Valid() {
		writeJSON(w, http.StatusBadRequest, presenceapi.Error{Code: "bad_request", Message: fmt.Sprintf("state must be %q or %q", presenceapi.StatePresent, presenceapi.StateParked)})
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	a := s.get(id)
	from := a.state
	if from != state {
		a.state = state
		a.version++
	}
	s.mu.Unlock()
	s.log.Info("state_set", "actor", id, "from", string(from), "to", string(state))
	s.show(w, r)
}

type view struct {
	ID      string              `json:"id"`
	State   presenceapi.State   `json:"state"`
	Version int64               `json:"version"`
	Last    *presenceapi.Status `json:"last_status,omitempty"`
	LastAt  *time.Time          `json:"last_status_at,omitempty"`
}

func (s *stub) show(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	a := s.get(id)
	v := view{ID: id, State: a.state, Version: a.version, Last: a.last}
	if a.last != nil {
		at := a.lastAt
		v.LastAt = &at
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
