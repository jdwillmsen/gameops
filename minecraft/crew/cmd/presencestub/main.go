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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/internal/presenceapi"
)

// A status report is a few hundred bytes; the cap only stops a runaway body.
const maxBody = 64 << 10

// The contract's actor id shape, the same one crew checks PRESENCE_ACTOR_ID
// against, so a mistyped id in a PUT is refused rather than made an actor.
var actorIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// logSafe drops line breaks from a value a request supplied. The JSON
// handler escapes them anyway; this keeps a log line one line whatever
// handler a later change picks.
func logSafe(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "\n", ""), "\r", "")
}

// actorID is the request's actor, or false after answering 404 for an id
// that cannot be one.
func actorID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !actorIDPattern.MatchString(id) {
		writeJSON(w, http.StatusNotFound, presenceapi.Error{Code: "not_found", Message: "actor id must match " + actorIDPattern.String()})
		return "", false
	}
	return id, true
}

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
	// polledAt is zero until the unit first asks, which is how a park sent
	// to a mistyped id shows as reaching nobody.
	polledAt time.Time
}

type stub struct {
	def presenceapi.State
	log *slog.Logger
	now func() time.Time
	// boot is in every ETag. Versions restart at 1 with the process, and a
	// unit holding "1" from before a restart would otherwise get 304 for a
	// state it never saw.
	boot string

	mu     sync.Mutex
	actors map[string]*actor
}

func newStub(def presenceapi.State, log *slog.Logger) *stub {
	return &stub{def: def, log: log, now: time.Now, boot: strconv.FormatInt(time.Now().UnixNano(), 36), actors: map[string]*actor{}}
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

// etag must be called with mu held.
func (s *stub) etag(a *actor) string {
	return fmt.Sprintf(`"%s-%s-%d"`, s.boot, a.state, a.version)
}

func (s *stub) presence(w http.ResponseWriter, r *http.Request) {
	id, ok := actorID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	a := s.get(id)
	a.polledAt = s.now().UTC()
	p := presenceapi.Presence{ActorID: id, Effective: a.state, Default: s.def}
	tag := s.etag(a)
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
	id, ok := actorID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	a := s.get(id)
	changed := a.last == nil || a.last.Connected != st.Connected || a.last.ObservedState != st.ObservedState
	a.last, a.lastAt = &st, s.now().UTC()
	s.mu.Unlock()
	// Only a change is logged: the unit reports on every poll, and the lab
	// proof reads this log for the moment it obeyed a park.
	if changed {
		s.log.Info("status_changed", "actor", logSafe(id), "connected", st.Connected, "observed_state", logSafe(string(st.ObservedState)), "version", logSafe(st.ProcessVersion))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *stub) set(w http.ResponseWriter, r *http.Request) {
	state := presenceapi.State(r.URL.Query().Get("state"))
	if !state.Valid() {
		writeJSON(w, http.StatusBadRequest, presenceapi.Error{Code: "bad_request", Message: fmt.Sprintf("state must be %q or %q", presenceapi.StatePresent, presenceapi.StateParked)})
		return
	}
	id, ok := actorID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	a := s.get(id)
	from := a.state
	if from != state {
		a.state = state
		a.version++
	}
	v := a.view(id)
	s.mu.Unlock()
	if v.PolledAt == nil {
		s.log.Warn("state_set_unpolled", "actor", logSafe(id), "to", logSafe(string(state)), "note", "no unit has polled this id; check it matches PRESENCE_ACTOR_ID")
	} else {
		s.log.Info("state_set", "actor", logSafe(id), "from", logSafe(string(from)), "to", logSafe(string(state)))
	}
	writeJSON(w, http.StatusOK, v)
}

type view struct {
	ID       string              `json:"id"`
	State    presenceapi.State   `json:"state"`
	Version  int64               `json:"version"`
	Last     *presenceapi.Status `json:"last_status,omitempty"`
	LastAt   *time.Time          `json:"last_status_at,omitempty"`
	PolledAt *time.Time          `json:"polled_at,omitempty"`
}

// view must be called with mu held.
func (a *actor) view(id string) view {
	v := view{ID: id, State: a.state, Version: a.version, Last: a.last}
	if a.last != nil {
		at := a.lastAt
		v.LastAt = &at
	}
	if !a.polledAt.IsZero() {
		at := a.polledAt
		v.PolledAt = &at
	}
	return v
}

func (s *stub) show(w http.ResponseWriter, r *http.Request) {
	id, ok := actorID(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	v := s.get(id).view(id)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
