// Package metrics exports what the poller sees of the fleet as Prometheus
// text: task throughput and outcomes, failure reasons, durations, and how the
// units spend their time.
//
// Every number here comes from polling each unit's state about once a
// second, so it is a sample of what happened, not a ledger. The help text of
// each series says how far it can be trusted. The exact per-task record is
// the units' own, and the event history stores what is worth keeping; this
// package only counts, so it costs a fixed few thousand series at most
// however busy the fleet is.
package metrics

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

// Task outcomes. idle is a finished poll, not work: see events.IsIdlePoll.
const (
	OutcomeDone      = "done"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"
	OutcomeUnknown   = "unknown"
	OutcomeIdle      = "idle"
)

// Outcomes lists every value of the outcome label.
var Outcomes = []string{OutcomeDone, OutcomeFailed, OutcomeCancelled, OutcomeUnknown, OutcomeIdle}

// States lists every value of the state label.
var States = []string{
	telemetry.StateWorking, telemetry.StateIdle, telemetry.StateStuck,
	telemetry.StateDead, telemetry.StateOffline,
}

// maxTickGap caps the time one poll adds to a unit's time-in-state. A poll
// loop that stalled (a long GC, a suspended pod) did not watch the units in
// between, and crediting the gap to the last state would be a guess.
const maxTickGap = 10 * time.Second

// durationBuckets spans a quick gather to a long shaft; idle polls are
// excluded, so nothing needs resolution below a second.
var durationBuckets = []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1800}

// Fleet holds the series. Its registry is its own, not the process default,
// so /metrics carries exactly the catalogue and nothing a dependency registers.
type Fleet struct {
	reg   *prometheus.Registry
	units map[string]bool

	started    *prometheus.CounterVec
	finished   *prometheus.CounterVec
	failures   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	lastDone   *prometheus.GaugeVec
	deaths     *prometheus.CounterVec
	stuck      *prometheus.CounterVec
	fleetUnits *prometheus.GaugeVec
	online     prometheus.Gauge
	unitState  *prometheus.GaugeVec
	inState    *prometheus.CounterVec
	health     *prometheus.GaugeVec
	hunger     *prometheus.GaugeVec

	mu   sync.Mutex
	last time.Time
}

// New returns a Fleet for the given unit ids. Events from any other unit are
// dropped, so the unit label is the roster and cannot outgrow it.
func New(unitIDs []string) *Fleet {
	f := &Fleet{reg: prometheus.NewRegistry(), units: make(map[string]bool, len(unitIDs))}
	for _, id := range unitIDs {
		f.units[id] = true
	}
	f.started = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_tasks_started_total",
		Help: "Tasks the poller saw start, by unit and template. A sample: a task that began and ended between two polls is never seen, and quick idle-eligible tasks (haul) are seen only about one time in five at a one-second poll. Includes starts that turned out to be idle polls. Do not subtract finished from started to get work in flight: a unit that goes offline and returns mid-task starts the same task again, and a task already running when crew-control starts is never seen to start.",
	}, []string{"unit", "template"})
	f.finished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_tasks_finished_total",
		Help: "Tasks the poller saw end, by unit, template and outcome (done, failed, cancelled, unknown when the unit's recent list no longer held the verdict, idle for a quick successful poll of an idle-eligible template such as an empty haul). done excludes idle, so rate(done) is real throughput. A sample, not an exact count: tasks shorter than a poll interval are undercounted; failures and tasks of several seconds or more are counted reliably.",
	}, []string{"unit", "template", "outcome"})
	f.failures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_task_failures_total",
		Help: "Failed tasks the poller saw, by template and a closed failure reason derived from the unit's free-text reason (unrecognised text is 'other'). The same sampling caveat as crew_fleet_tasks_finished_total applies; the reason is a classification of text, not a code the unit emitted.",
	}, []string{"template", "reason"})
	f.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crew_fleet_task_duration_seconds",
		Help:    "How long finished tasks ran, from the unit's own created and updated times, by template and outcome (done or failed). Idle polls are left out. Exact for the tasks counted, but only tasks the poller saw end are in it.",
		Buckets: durationBuckets,
	}, []string{"template", "outcome"})
	f.lastDone = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_last_done_timestamp_seconds",
		Help: "Unix time the poller last saw this unit finish a task as done (not idle). Unset until it has; time() minus this is how long a unit has gone without real work.",
	}, []string{"unit"})
	f.deaths = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_deaths_total",
		Help: "Times a poll found the unit dead after finding it alive. A death shorter than one poll interval is missed.",
	}, []string{"unit"})
	f.stuck = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_stuck_total",
		Help: "Times a running task made no progress for the stuck threshold. Counted once per stall.",
	}, []string{"unit"})
	f.fleetUnits = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_units",
		Help: "Units in each state at the last poll. The states are exclusive and sum to the roster: working, idle, stuck, dead, offline.",
	}, []string{"state"})
	f.online = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "crew_fleet_units_online",
		Help: "Units connected at the last poll: everything except offline, so working, idle, stuck and dead together.",
	})
	f.unitState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_state",
		Help: "1 for the state each unit was in at the last poll, 0 for the rest.",
	}, []string{"unit", "state"})
	f.inState = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_state_seconds_total",
		Help: "Seconds each unit has spent in each state, in poll-interval steps while crew-control ran. Accurate to about one poll per change of state; time crew-control itself was down is not counted.",
	}, []string{"unit", "state"})
	f.health = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_health",
		Help: "Health points (0 to 20) each unit reported at the last poll. Absent while the unit is offline or has not reported one, so a gap is never a zero.",
	}, []string{"unit"})
	f.hunger = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_hunger",
		Help: "Food level (0 to 20) each unit reported at the last poll. Absent while the unit is offline or has not reported one, so a gap is never a zero.",
	}, []string{"unit"})
	f.reg.MustRegister(f.started, f.finished, f.failures, f.duration, f.lastDone,
		f.deaths, f.stuck, f.fleetUnits, f.online, f.unitState, f.inState, f.health, f.hunger)

	for _, s := range States {
		f.fleetUnits.WithLabelValues(s)
	}
	for _, id := range unitIDs {
		f.deaths.WithLabelValues(id)
		f.stuck.WithLabelValues(id)
		for _, s := range States {
			f.unitState.WithLabelValues(id, s)
			f.inState.WithLabelValues(id, s)
		}
	}
	return f
}

// Handler serves the Prometheus text exposition.
func (f *Fleet) Handler() http.Handler {
	return promhttp.HandlerFor(f.reg, promhttp.HandlerOpts{})
}

// Observe counts the events one poll produced.
func (f *Fleet) Observe(batch []telemetry.Born) {
	for _, b := range batch {
		if !f.units[b.Unit] {
			continue
		}
		tpl := Template(b.Template)
		switch b.Kind {
		case "task_started":
			f.started.WithLabelValues(b.Unit, tpl).Inc()
		case "task_done":
			if events.IsIdlePoll(b.Kind, b.Template, b.Took) {
				f.finished.WithLabelValues(b.Unit, tpl, OutcomeIdle).Inc()
				continue
			}
			f.finished.WithLabelValues(b.Unit, tpl, OutcomeDone).Inc()
			f.lastDone.WithLabelValues(b.Unit).Set(float64(b.At.UnixNano()) / 1e9)
			f.observeDuration(tpl, OutcomeDone, b.Took)
		case "task_failed":
			f.finished.WithLabelValues(b.Unit, tpl, OutcomeFailed).Inc()
			f.failures.WithLabelValues(tpl, Reason(b.Reason)).Inc()
			f.observeDuration(tpl, OutcomeFailed, b.Took)
		case "task_cancelled":
			f.finished.WithLabelValues(b.Unit, tpl, OutcomeCancelled).Inc()
		case "task_ended":
			f.finished.WithLabelValues(b.Unit, tpl, OutcomeUnknown).Inc()
		case "died":
			f.deaths.WithLabelValues(b.Unit).Inc()
		case "stuck":
			f.stuck.WithLabelValues(b.Unit).Inc()
		}
	}
}

func (f *Fleet) observeDuration(template, outcome string, took time.Duration) {
	// Zero means the unit sent no usable times, which is not a fast task.
	if took > 0 {
		f.duration.WithLabelValues(template, outcome).Observe(took.Seconds())
	}
}

// ObservePoll records every unit's state at the end of a poll and credits the
// time since the previous poll to the state each unit is in now.
func (f *Fleet) ObservePoll(now time.Time, states []telemetry.UnitState) {
	f.mu.Lock()
	var dt time.Duration
	if !f.last.IsZero() {
		dt = min(max(now.Sub(f.last), 0), maxTickGap)
	}
	f.last = now
	f.mu.Unlock()

	count := make(map[string]int, len(States))
	for _, u := range states {
		if !f.units[u.ID] {
			continue
		}
		count[u.State]++
		setOrClear(f.health, u.ID, u.Health)
		setOrClear(f.hunger, u.ID, u.Hunger)
		for _, s := range States {
			v := 0.0
			if s == u.State {
				v = 1
			}
			f.unitState.WithLabelValues(u.ID, s).Set(v)
		}
		if dt > 0 && validState(u.State) {
			f.inState.WithLabelValues(u.ID, u.State).Add(dt.Seconds())
		}
	}
	for _, s := range States {
		f.fleetUnits.WithLabelValues(s).Set(float64(count[s]))
	}
	f.online.Set(float64(count[telemetry.StateWorking] + count[telemetry.StateIdle] + count[telemetry.StateStuck] + count[telemetry.StateDead]))
}

// setOrClear drops the series for an unknown reading: Prometheus would
// otherwise keep serving the last value as if it were current.
func setOrClear(g *prometheus.GaugeVec, unit string, v *float32) {
	if v == nil {
		g.DeleteLabelValues(unit)
		return
	}
	g.WithLabelValues(unit).Set(float64(*v))
}

func validState(s string) bool {
	for _, v := range States {
		if v == s {
			return true
		}
	}
	return false
}
