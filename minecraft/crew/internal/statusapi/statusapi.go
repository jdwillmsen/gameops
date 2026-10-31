// Package statusapi serves what crew believes about itself. It is read-only
// and unauthenticated, so it must only be reachable from where crew-control
// and the probes run: config defaults its address to loopback.
package statusapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

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

type options struct {
	fight     FightSource
	tactics   TacticsSource
	sightings SightingSource
	entities  func(func(belief.Entity))
}

// EntityView is one entity as a harness reads it back. Positions make it
// unfit for a real lab, so WithEntities is wired only for a preview server.
type EntityView struct {
	ID          uint64     `json:"id"`
	UniqueID    int64      `json:"unique_id"`
	Type        string     `json:"type"`
	Name        string     `json:"name,omitempty"`
	CustomName  string     `json:"custom_name,omitempty"`
	Pos         [3]float32 `json:"pos"`
	Tamed       bool       `json:"tamed"`
	Sitting     bool       `json:"sitting"`
	Angry       bool       `json:"angry"`
	Owner       int64      `json:"owner,omitempty"`
	Target      int64      `json:"target,omitempty"`
	Health      float32    `json:"health"`
	HealthKnown bool       `json:"health_known"`
}

// WithEntities lists every entity the unit knows of as "entities" in /v1/state.
func WithEntities(each func(func(belief.Entity))) Option {
	return func(o *options) { o.entities = each }
}

// WithFight reports a warrior's fight counts under the tasks in /v1/state.
func WithFight(f FightSource) Option { return func(o *options) { o.fight = f } }

// SightingSource is the read side of what the unit has seen of other
// entities; ok is false for a unit that keeps none.
type SightingSource interface {
	Sightings() (life.Sightings, bool)
}

// WithSightings reports the first health reading and flag states of each mob
// type as "seen" in /v1/state, which is how a lab check reads them back
// without the unit's log.
func WithSightings(s SightingSource) Option { return func(o *options) { o.sightings = s } }

// Tactics is what a unit is fighting with, so a trainer can confirm a pushed
// profile took hold on the unit and not only on the squad route.
type Tactics struct {
	Profile string `json:"profile"`
	// Hash is the profile's tactics.Hash, the identity the squad route and an
	// approval use; empty only when the profile cannot be hashed.
	Hash string `json:"hash"`
	// AppliedAt is when the unit's profile sync last swapped it in; absent
	// for a unit that has no sync and runs the shipped behaviour.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	// TrainingEnabled is the kill switch as the unit last read it; absent
	// for a unit that does not read one.
	TrainingEnabled *bool `json:"training_enabled,omitempty"`
}

// TacticsSource is the read side of a unit's tuning.
type TacticsSource interface {
	Tactics() Tactics
}

// WithTactics reports the unit's tuning as "tactics" in /v1/state.
func WithTactics(t TacticsSource) Option { return func(o *options) { o.tactics = t } }

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
	// Tactics is absent for a unit that has no tuning to report.
	Tactics *Tactics `json:"tactics,omitempty"`
	// Seen is absent for a unit that keeps no sightings.
	Seen *life.Sightings `json:"seen,omitempty"`
	// Entities is absent unless the unit is a harness unit.
	Entities []EntityView `json:"entities,omitempty"`
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
		body := stateBody{Version: version, Unit: unit, State: src.Snapshot(), Tasks: view}
		if o.tactics != nil {
			t := o.tactics.Tactics()
			body.Tactics = &t
		}
		if o.sightings != nil {
			if s, ok := o.sightings.Sightings(); ok {
				body.Seen = &s
			}
		}
		if o.entities != nil {
			body.Entities = []EntityView{}
			o.entities(func(e belief.Entity) {
				body.Entities = append(body.Entities, EntityView{ID: e.ID, UniqueID: e.UniqueID, Type: e.Type, Name: e.Name, CustomName: e.CustomName,
					Pos: e.Pos, Tamed: e.Tamed, Sitting: e.Sitting, Angry: e.Angry, Owner: e.Owner, Target: e.Target, Health: e.Health, HealthKnown: e.HealthKnown})
			})
			sort.Slice(body.Entities, func(i, j int) bool { return body.Entities[i].ID < body.Entities[j].ID })
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	// The process-wide registry: packages register their own metrics at init,
	// so this stays the one place metrics are exposed.
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
