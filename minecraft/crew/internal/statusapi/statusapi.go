// Package statusapi serves what crew believes about itself. It is read-only
// and unauthenticated, so it must only be reachable from where crew-control
// and the probes run: config defaults its address to loopback.
package statusapi

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
)

// recentTasks is how much history the state body carries.
const recentTasks = 5

type Source interface {
	Snapshot() belief.Snapshot
}

// TaskSource is the read side of the task runner. It has no way to submit or
// cancel, so the unauthenticated routes cannot be made to.
type TaskSource interface {
	List() []task.Task
}

// FightSource is the read side of a warrior's fight counts; ok is false for a
// unit that keeps none.
type FightSource interface {
	Stats() (life.FightSummary, bool)
}

// Option adds an optional part to the state body.
type Option func(*options)

type options struct{ fight FightSource }

// WithFight reports a warrior's fight counts under the tasks in /v1/state.
func WithFight(f FightSource) Option { return func(o *options) { o.fight = f } }

// Unit is who the state belongs to: a fixed id and the in-game name. Role is
// the unit's CREW_ROLE, empty when it has none. It rides here, not on the
// squad hello, because builder roles never join a squad.
type Unit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type stateBody struct {
	Version string          `json:"version"`
	Unit    Unit            `json:"unit"`
	State   belief.Snapshot `json:"state"`
	Tasks   tasksView       `json:"tasks"`
}

type tasksView struct {
	Current *task.Task `json:"current"`
	// Recent is newest first and never includes Current.
	Recent []task.Task `json:"recent"`
	// Fight is the warrior's swings, hits, damage and deaths; absent for a
	// unit that keeps none.
	Fight *life.FightSummary `json:"fight,omitempty"`
}

func viewTasks(src TaskSource) tasksView {
	v := tasksView{Recent: []task.Task{}}
	if src == nil {
		return v
	}
	all := src.List()
	for i := len(all) - 1; i >= 0; i-- {
		switch {
		case all[i].State == task.Running && v.Current == nil:
			cur := all[i]
			v.Current = &cur
		case len(v.Recent) < recentTasks:
			v.Recent = append(v.Recent, all[i])
		}
	}
	return v
}

// Handler serves the status routes. tasks may be nil, which reports no tasks.
func Handler(src Source, version string, unit Unit, tasks TaskSource, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		s := src.Snapshot()
		if !s.Spawned || s.Dead {
			http.Error(w, "not in the world", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		view := viewTasks(tasks)
		if o.fight != nil {
			if s, ok := o.fight.Stats(); ok {
				view.Fight = &s
			}
		}
		_ = json.NewEncoder(w).Encode(stateBody{Version: version, Unit: unit, State: src.Snapshot(), Tasks: view})
	})
	// The process-wide registry: packages register their own metrics at init,
	// so this stays the one place metrics are exposed.
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
