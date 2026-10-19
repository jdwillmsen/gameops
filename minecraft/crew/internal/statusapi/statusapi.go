// Package statusapi serves what crew believes about itself. It is read-only
// and unauthenticated, so it must only be reachable from where crew-control
// and the probes run: config defaults its address to loopback.
package statusapi

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
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

type stateBody struct {
	Version string          `json:"version"`
	State   belief.Snapshot `json:"state"`
	Tasks   tasksView       `json:"tasks"`
}

type tasksView struct {
	Current *task.Task `json:"current"`
	// Recent is newest first and never includes Current.
	Recent []task.Task `json:"recent"`
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
func Handler(src Source, version string, tasks TaskSource) http.Handler {
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
		_ = json.NewEncoder(w).Encode(stateBody{Version: version, State: src.Snapshot(), Tasks: viewTasks(tasks)})
	})
	// The process-wide registry: packages register their own metrics at init,
	// so this stays the one place metrics are exposed.
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
