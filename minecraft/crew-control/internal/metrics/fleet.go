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
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/dimension"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
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

// PhaseUnknown labels a death at a time the unit could not name.
const PhaseUnknown = "unknown"

// DayPhases lists every day phase a unit may report.
var DayPhases = []string{"dawn", "day", "dusk", "night"}

// phaseLabel keeps the label inside the closed list whatever the event held.
func phaseLabel(v any) string {
	if s, ok := v.(string); ok && slices.Contains(DayPhases, s) {
		return s
	}
	return PhaseUnknown
}

// Position stores that skip a sample taken outside the overworld.
const (
	StoreHeat   = "heat"
	StoreReplay = "replay"
)

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
	reg *prometheus.Registry
	// units maps each rostered unit to its world. A unit has exactly one
	// world, so the world label never multiplies a series that already
	// carries the unit; only the fleet-wide series grow with the world count.
	units  map[string]string
	worlds []string

	started    *prometheus.CounterVec
	finished   *prometheus.CounterVec
	failures   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	lastDone   *prometheus.GaugeVec
	deaths     *prometheus.CounterVec
	stuck      *prometheus.CounterVec
	fleetUnits *prometheus.GaugeVec
	online     *prometheus.GaugeVec
	unitState  *prometheus.GaugeVec
	inState    *prometheus.CounterVec
	health     *prometheus.GaugeVec
	hunger     *prometheus.GaugeVec
	toolPct    *prometheus.GaugeVec
	deathsAt   *prometheus.CounterVec
	swings     *prometheus.CounterVec
	hits       *prometheus.CounterVec
	damageTkn  *prometheus.CounterVec
	unitDim    *prometheus.GaugeVec
	skipped    *prometheus.CounterVec

	mu   sync.Mutex
	last time.Time
	// fights is each unit's fight totals at its last poll, the baseline the
	// next poll's growth is measured from.
	fights map[string]telemetry.FightTotals
}

// Option adjusts a Fleet before its series are created.
type Option func(*Fleet)

// WithWorlds names the world each unit plays in. A unit not listed is in
// the roster's default world.
func WithWorlds(worldOf map[string]string) Option {
	return func(f *Fleet) {
		for id := range f.units {
			if w := worldOf[id]; w != "" {
				f.units[id] = w
			}
		}
	}
}

// ForRoster is the options for a parsed roster: each unit in its own world.
func ForRoster(r roster.Roster) Option {
	worldOf := map[string]string{}
	for _, u := range r.Units() {
		worldOf[u.ID] = u.World
	}
	return WithWorlds(worldOf)
}

// New returns a Fleet for the given unit ids. Events from any other unit are
// dropped, so the unit label is the roster and cannot outgrow it, and the
// world label is the roster's worlds for the same reason.
func New(unitIDs []string, opts ...Option) *Fleet {
	f := &Fleet{reg: prometheus.NewRegistry(), units: make(map[string]string, len(unitIDs)), fights: map[string]telemetry.FightTotals{}}
	for _, id := range unitIDs {
		f.units[id] = roster.DefaultWorld
	}
	for _, o := range opts {
		o(f)
	}
	for _, id := range unitIDs {
		if w := f.units[id]; !slices.Contains(f.worlds, w) {
			f.worlds = append(f.worlds, w)
		}
	}
	f.started = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_tasks_started_total",
		Help: "Tasks the poller saw start, by unit and template. A sample: a task that began and ended between two polls is never seen, and quick idle-eligible tasks (haul) are seen only about one time in five at a one-second poll. Includes starts that turned out to be idle polls. Do not subtract finished from started to get work in flight: a unit that goes offline and returns mid-task starts the same task again, and a task already running when crew-control starts is never seen to start.",
	}, []string{"world", "unit", "template"})
	f.finished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_tasks_finished_total",
		Help: "Tasks the poller saw end, by unit, template and outcome (done, failed, cancelled, unknown when the unit's recent list no longer held the verdict, idle for a quick successful poll of an idle-eligible template such as an empty haul). done excludes idle, so rate(done) is real throughput. A sample, not an exact count: tasks shorter than a poll interval are undercounted; failures and tasks of several seconds or more are counted reliably.",
	}, []string{"world", "unit", "template", "outcome"})
	f.failures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_task_failures_total",
		Help: "Failed tasks the poller saw, by template and a closed failure reason derived from the unit's free-text reason (unrecognised text is 'other'). The same sampling caveat as crew_fleet_tasks_finished_total applies; the reason is a classification of text, not a code the unit emitted.",
	}, []string{"world", "template", "reason"})
	f.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "crew_fleet_task_duration_seconds",
		Help:    "How long finished tasks ran, from the unit's own created and updated times, by template and outcome (done or failed). Idle polls are left out. Exact for the tasks counted, but only tasks the poller saw end are in it.",
		Buckets: durationBuckets,
	}, []string{"world", "template", "outcome"})
	f.lastDone = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_last_done_timestamp_seconds",
		Help: "Unix time the poller last saw this unit finish a task as done (not idle). Unset until it has; time() minus this is how long a unit has gone without real work.",
	}, []string{"world", "unit"})
	f.deaths = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_deaths_total",
		Help: "Times a poll found the unit dead after finding it alive. A death shorter than one poll interval is missed.",
	}, []string{"world", "unit"})
	f.stuck = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_stuck_total",
		Help: "Times a running task made no progress for the stuck threshold. Counted once per stall.",
	}, []string{"world", "unit"})
	f.fleetUnits = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_units",
		Help: "Units in each state at the last poll, per world. The states are exclusive and sum to the world's units: working, idle, stuck, dead, offline.",
	}, []string{"world", "state"})
	f.online = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_units_online",
		Help: "Units connected at the last poll, per world: everything except offline, so working, idle, stuck and dead together.",
	}, []string{"world"})
	f.unitState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_state",
		Help: "1 for the state each unit was in at the last poll, 0 for the rest.",
	}, []string{"world", "unit", "state"})
	f.inState = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_state_seconds_total",
		Help: "Seconds each unit has spent in each state, in poll-interval steps while crew-control ran. Accurate to about one poll per change of state; time crew-control itself was down is not counted.",
	}, []string{"world", "unit", "state"})
	f.health = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_health",
		Help: "Health points (0 to 20) each unit reported at the last poll. Absent while the unit is offline or has not reported one, so a gap is never a zero.",
	}, []string{"world", "unit"})
	f.hunger = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_hunger",
		Help: "Food level (0 to 20) each unit reported at the last poll. Absent while the unit is offline or has not reported one, so a gap is never a zero.",
	}, []string{"world", "unit"})
	f.toolPct = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_tool_durability_pct",
		Help: "Durability left (0 to 100) on the item each unit held at the last poll. Absent while the unit is offline, holds nothing, holds an item that cannot wear out, or has not reported it, so a gap is never a zero. Armour wear is in the tool_worn events, not here.",
	}, []string{"world", "unit"})
	f.deathsAt = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_deaths_by_day_phase_total",
		Help: "Deaths the poller saw, by the unit's own day phase at the poll that found it dead (dawn, day, dusk, night, or unknown when the unit had no clock). The same sampling caveat as crew_fleet_unit_deaths_total applies.",
	}, []string{"world", "phase"})
	f.swings = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_swings_total",
		Help: "Melee swings and bow releases a warrior sent, summed from the growth of the totals in its state between polls. A unit that restarts, or goes offline, starts a new baseline, so growth in a poll gap that spans either is lost and the series undercounts; it never counts a unit's history from before crew-control watched. Absent units (not warriors, older units) stay at zero.",
	}, []string{"world", "unit"})
	f.hits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_hits_landed_total",
		Help: "Swings and shots a warrior's target answered with a hurt animation or its death, on the same terms as crew_fleet_unit_swings_total. An upper bound when several warriors fight one target.",
	}, []string{"world", "unit"})
	f.damageTkn = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_unit_damage_taken_total",
		Help: "Health points a warrior lost, on the same terms as crew_fleet_unit_swings_total.",
	}, []string{"world", "unit"})
	f.unitDim = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "crew_fleet_unit_dimension",
		Help: "The dimension each unit stood in at the last poll: 0 overworld, 1 nether, 2 end, 3 any other id. Absent while the unit is offline or has not reported where it is, so a gap is never the overworld.",
	}, []string{"world", "unit"})
	f.skipped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_fleet_position_samples_dropped_total",
		Help: "Position samples the heat grid or the replay recorder skipped because the unit stood outside the overworld, by world, the dimension (nether, end, other) and the store (heat, replay). Both stores keep overworld rows only, so this is what they leave out. One sample per store per sampling tick.",
	}, []string{"world", "dim", "store"})
	f.reg.MustRegister(f.unitDim, f.skipped, f.swings, f.hits, f.damageTkn, f.deathsAt, f.started, f.finished, f.failures, f.duration, f.lastDone,
		f.deaths, f.stuck, f.fleetUnits, f.online, f.unitState, f.inState, f.health, f.hunger, f.toolPct)

	for _, w := range f.worlds {
		f.online.WithLabelValues(w)
		for _, p := range append(DayPhases, PhaseUnknown) {
			f.deathsAt.WithLabelValues(w, p)
		}
		for _, s := range States {
			f.fleetUnits.WithLabelValues(w, s)
		}
	}
	for _, id := range unitIDs {
		w := f.units[id]
		f.deaths.WithLabelValues(w, id)
		f.stuck.WithLabelValues(w, id)
		f.swings.WithLabelValues(w, id)
		f.hits.WithLabelValues(w, id)
		f.damageTkn.WithLabelValues(w, id)
		for _, s := range States {
			f.unitState.WithLabelValues(w, id, s)
			f.inState.WithLabelValues(w, id, s)
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
		w, ok := f.units[b.Unit]
		if !ok {
			continue
		}
		tpl := Template(b.Template)
		switch b.Kind {
		case "task_started":
			f.started.WithLabelValues(w, b.Unit, tpl).Inc()
		case "task_done":
			if events.IsIdlePoll(b.Kind, b.Template, b.Took) {
				f.finished.WithLabelValues(w, b.Unit, tpl, OutcomeIdle).Inc()
				continue
			}
			f.finished.WithLabelValues(w, b.Unit, tpl, OutcomeDone).Inc()
			f.lastDone.WithLabelValues(w, b.Unit).Set(float64(b.At.UnixNano()) / 1e9)
			f.observeDuration(w, tpl, OutcomeDone, b.Took)
		case "task_failed":
			f.finished.WithLabelValues(w, b.Unit, tpl, OutcomeFailed).Inc()
			f.failures.WithLabelValues(w, tpl, Reason(b.Reason)).Inc()
			f.observeDuration(w, tpl, OutcomeFailed, b.Took)
		case "task_cancelled":
			f.finished.WithLabelValues(w, b.Unit, tpl, OutcomeCancelled).Inc()
		case "task_ended":
			f.finished.WithLabelValues(w, b.Unit, tpl, OutcomeUnknown).Inc()
		case "died":
			f.deaths.WithLabelValues(w, b.Unit).Inc()
			f.deathsAt.WithLabelValues(w, phaseLabel(b.Attrs["day_phase"])).Inc()
		case "stuck":
			f.stuck.WithLabelValues(w, b.Unit).Inc()
		}
	}
}

func (f *Fleet) observeDuration(world, template, outcome string, took time.Duration) {
	// Zero means the unit sent no usable times, which is not a fast task.
	if took > 0 {
		f.duration.WithLabelValues(world, template, outcome).Observe(took.Seconds())
	}
}

// PositionDropped counts one position sample that a store left out because
// the unit stood outside the overworld. The unit picks the world and the
// dimension must be one of the closed non-overworld names, so the sample's
// own words cannot add a series.
func (f *Fleet) PositionDropped(store string, p telemetry.Position) {
	w, ok := f.units[p.ID]
	if !ok || (store != StoreHeat && store != StoreReplay) {
		return
	}
	dim := dimension.Name(p.Dim)
	if dim == dimension.Overworld {
		return
	}
	f.skipped.WithLabelValues(w, dim, store).Inc()
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

	count := make(map[string]map[string]int, len(f.worlds))
	for _, w := range f.worlds {
		count[w] = make(map[string]int, len(States))
	}
	for _, u := range states {
		w, ok := f.units[u.ID]
		if !ok {
			continue
		}
		count[w][u.State]++
		setOrClear(f.health, w, u.ID, u.Health)
		setOrClear(f.hunger, w, u.ID, u.Hunger)
		if u.ToolDurability == nil {
			f.toolPct.DeleteLabelValues(w, u.ID)
		} else {
			f.toolPct.WithLabelValues(w, u.ID).Set(float64(*u.ToolDurability))
		}
		if u.Dim == "" {
			f.unitDim.DeleteLabelValues(w, u.ID)
		} else {
			f.unitDim.WithLabelValues(w, u.ID).Set(dimension.Gauge(u.Dim))
		}
		for _, s := range States {
			v := 0.0
			if s == u.State {
				v = 1
			}
			f.unitState.WithLabelValues(w, u.ID, s).Set(v)
		}
		f.observeFight(w, u.ID, u.Fight)
		if dt > 0 && validState(u.State) {
			f.inState.WithLabelValues(w, u.ID, u.State).Add(dt.Seconds())
		}
	}
	for _, w := range f.worlds {
		c := count[w]
		for _, s := range States {
			f.fleetUnits.WithLabelValues(w, s).Set(float64(c[s]))
		}
		f.online.WithLabelValues(w).Set(float64(c[telemetry.StateWorking] + c[telemetry.StateIdle] + c[telemetry.StateStuck] + c[telemetry.StateDead]))
	}
}

// observeFight adds the growth of one unit's fight totals since the last
// poll. The first reading, one after a gap, and one after a restart (a total
// that went down) only set the baseline.
func (f *Fleet) observeFight(world, unit string, cur *telemetry.FightTotals) {
	f.mu.Lock()
	prev, had := f.fights[unit]
	if cur == nil {
		delete(f.fights, unit)
		f.mu.Unlock()
		return
	}
	f.fights[unit] = *cur
	f.mu.Unlock()
	if !had {
		return
	}
	d, ok := cur.Since(prev)
	if !ok {
		return
	}
	f.swings.WithLabelValues(world, unit).Add(float64(d.Swings))
	f.hits.WithLabelValues(world, unit).Add(float64(d.Hits))
	f.damageTkn.WithLabelValues(world, unit).Add(d.Damage)
}

// setOrClear drops the series for an unknown reading: Prometheus would
// otherwise keep serving the last value as if it were current.
func setOrClear(g *prometheus.GaugeVec, world, unit string, v *float32) {
	if v == nil {
		g.DeleteLabelValues(world, unit)
		return
	}
	g.WithLabelValues(world, unit).Set(float64(*v))
}

func validState(s string) bool {
	for _, v := range States {
		if v == s {
			return true
		}
	}
	return false
}
