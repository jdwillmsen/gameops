package metrics

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

var roster3 = []string{"crew-01", "crew-02", "crew-03"}

// families gathers the registry the way a scrape would.
func families(t *testing.T, f *Fleet) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := f.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

func labelsOf(m *dto.Metric) map[string]string {
	out := map[string]string{}
	for _, l := range m.GetLabel() {
		out[l.GetName()] = l.GetValue()
	}
	return out
}

// find returns the series whose labels equal want exactly, or nil.
func find(mf *dto.MetricFamily, want map[string]string) *dto.Metric {
	for _, m := range mf.GetMetric() {
		got := labelsOf(m)
		if len(got) != len(want) {
			continue
		}
		ok := true
		for k, v := range want {
			if got[k] != v {
				ok = false
			}
		}
		if ok {
			return m
		}
	}
	return nil
}

// value is the counter or gauge for the labels, 0 when the series is absent.
func value(t *testing.T, f *Fleet, name string, labels map[string]string) float64 {
	t.Helper()
	mf := families(t, f)[name]
	if mf == nil {
		// A vector with no child yet has nothing to gather.
		return 0
	}
	m := find(mf, labels)
	if m == nil {
		return 0
	}
	switch {
	case m.Counter != nil:
		return m.Counter.GetValue()
	case m.Gauge != nil:
		return m.Gauge.GetValue()
	}
	t.Fatalf("%s is not a counter or gauge", name)
	return 0
}

func born(unit, kind, template string, took time.Duration, reason string) telemetry.Born {
	return telemetry.Born{Event: telemetry.Event{
		Unit: unit, Kind: kind, Template: template, Took: took, Reason: reason,
		At: time.Unix(1_700_000_000, 0),
	}}
}

func TestTasksAreCountedByTemplateAndOutcome(t *testing.T) {
	f := New(roster3)
	f.Observe([]telemetry.Born{
		born("crew-01", "task_started", "gather_and_deposit", 0, ""),
		born("crew-01", "task_done", "gather_and_deposit", 12*time.Second, ""),
		born("crew-01", "task_started", "mine_shaft", 0, ""),
		born("crew-01", "task_failed", "mine_shaft", 30*time.Second, "no pickaxe in inventory"),
		born("crew-02", "task_cancelled", "goto", 0, ""),
		born("crew-02", "task_ended", "goto", 0, ""),
	})
	for _, c := range []struct {
		name string
		l    map[string]string
		want float64
	}{
		{"crew_fleet_tasks_started_total", map[string]string{"unit": "crew-01", "template": "gather_and_deposit"}, 1},
		{"crew_fleet_tasks_started_total", map[string]string{"unit": "crew-01", "template": "mine_shaft"}, 1},
		{"crew_fleet_tasks_finished_total", map[string]string{"unit": "crew-01", "template": "gather_and_deposit", "outcome": "done"}, 1},
		{"crew_fleet_tasks_finished_total", map[string]string{"unit": "crew-01", "template": "mine_shaft", "outcome": "failed"}, 1},
		{"crew_fleet_tasks_finished_total", map[string]string{"unit": "crew-02", "template": "goto", "outcome": "cancelled"}, 1},
		{"crew_fleet_tasks_finished_total", map[string]string{"unit": "crew-02", "template": "goto", "outcome": "unknown"}, 1},
		{"crew_fleet_task_failures_total", map[string]string{"template": "mine_shaft", "reason": "missing_item"}, 1},
		{"crew_fleet_unit_last_done_timestamp_seconds", map[string]string{"unit": "crew-01"}, 1_700_000_000},
	} {
		if got := value(t, f, c.name, c.l); got != c.want {
			t.Errorf("%s%v = %v, want %v", c.name, c.l, got, c.want)
		}
	}
}

func TestIdlePollsAreCountedNotCountedAsWork(t *testing.T) {
	f := New(roster3)
	f.Observe([]telemetry.Born{
		born("crew-03", "task_done", "haul", 400*time.Millisecond, ""),
		born("crew-03", "task_done", "haul", 2*time.Second, ""),
		born("crew-03", "task_done", "haul", 9*time.Second, ""),
		// A quick goto did something; only idle-eligible templates fold.
		born("crew-03", "task_done", "goto", 500*time.Millisecond, ""),
		// An unknown duration is never idle.
		born("crew-03", "task_done", "haul", 0, ""),
	})
	idle := map[string]string{"unit": "crew-03", "template": "haul", "outcome": "idle"}
	done := map[string]string{"unit": "crew-03", "template": "haul", "outcome": "done"}
	if got := value(t, f, "crew_fleet_tasks_finished_total", idle); got != 2 {
		t.Errorf("idle haul polls = %v, want 2", got)
	}
	if got := value(t, f, "crew_fleet_tasks_finished_total", done); got != 2 {
		t.Errorf("done hauls = %v, want 2 (the 9 s haul and the one of unknown length)", got)
	}
	if got := value(t, f, "crew_fleet_tasks_finished_total", map[string]string{"unit": "crew-03", "template": "goto", "outcome": "done"}); got != 1 {
		t.Errorf("a quick goto must count as done, got %v", got)
	}
	// Idle polls stay out of the duration histogram, so it is not flooded
	// with sub-two-second no-ops; the unknown-length haul has no duration.
	h := find(families(t, f)["crew_fleet_task_duration_seconds"], map[string]string{"template": "haul", "outcome": "done"})
	if h == nil || h.Histogram.GetSampleCount() != 1 {
		t.Errorf("haul duration histogram = %v, want one sample", h)
	}
}

func TestFailureReasonsAreClassified(t *testing.T) {
	f := New(roster3)
	f.Observe([]telemetry.Born{
		born("crew-04", "task_failed", "gather_and_deposit", time.Second, "no axe in inventory"), // not in the roster
		born("crew-01", "task_failed", "gather_and_deposit", time.Second, "no axe in inventory"),
		born("crew-01", "task_failed", "gather_and_deposit", time.Second, "no pickaxe in inventory"),
		born("crew-01", "task_failed", "gather_and_deposit", time.Second, "visit took too long"),
		born("crew-01", "task_failed", "gather_and_deposit", time.Second, "something nobody has seen before"),
	})
	for reason, want := range map[string]float64{"missing_item": 2, "timeout": 1, "other": 1, "no_path": 0} {
		got := value(t, f, "crew_fleet_task_failures_total", map[string]string{"template": "gather_and_deposit", "reason": reason})
		if got != want {
			t.Errorf("reason %s = %v, want %v", reason, got, want)
		}
	}
}

func TestDeathsAndStallsAreCountedPerUnit(t *testing.T) {
	f := New(roster3)
	f.Observe([]telemetry.Born{
		born("crew-02", "died", "", 0, ""),
		born("crew-02", "died", "", 0, ""),
		born("crew-01", "stuck", "haul", 0, ""),
	})
	if got := value(t, f, "crew_fleet_unit_deaths_total", map[string]string{"unit": "crew-02"}); got != 2 {
		t.Errorf("deaths = %v", got)
	}
	if got := value(t, f, "crew_fleet_unit_stuck_total", map[string]string{"unit": "crew-01"}); got != 1 {
		t.Errorf("stuck = %v", got)
	}
}

func TestUnitsExistBeforeAnythingHappens(t *testing.T) {
	f := New(roster3)
	mfs := families(t, f)
	for _, name := range []string{"crew_fleet_unit_deaths_total", "crew_fleet_unit_stuck_total"} {
		if n := len(mfs[name].GetMetric()); n != len(roster3) {
			t.Errorf("%s has %d series, want one per unit", name, n)
		}
	}
	for _, name := range []string{"crew_fleet_unit_state", "crew_fleet_unit_state_seconds_total"} {
		if n := len(mfs[name].GetMetric()); n != len(roster3)*len(States) {
			t.Errorf("%s has %d series, want %d", name, n, len(roster3)*len(States))
		}
	}
}

func poll(units ...string) []telemetry.UnitState {
	out := make([]telemetry.UnitState, len(units))
	for i, s := range units {
		out[i] = telemetry.UnitState{ID: roster3[i], State: s}
	}
	return out
}

func TestStatesPartitionTheFleetAndTimeAccrues(t *testing.T) {
	f := New(roster3)
	t0 := time.Unix(1_700_000_000, 0)
	f.ObservePoll(t0, poll("working", "idle", "offline"))
	f.ObservePoll(t0.Add(time.Second), poll("working", "stuck", "offline"))
	f.ObservePoll(t0.Add(3*time.Second), poll("dead", "stuck", "idle"))

	want := map[string]float64{"working": 0, "idle": 1, "stuck": 1, "dead": 1, "offline": 0}
	var sum float64
	for s, n := range want {
		got := value(t, f, "crew_fleet_units", map[string]string{"state": s})
		sum += got
		if got != n {
			t.Errorf("units{%s} = %v, want %v", s, got, n)
		}
	}
	if sum != float64(len(roster3)) {
		t.Errorf("states sum to %v, want the roster size", sum)
	}
	if got := value(t, f, "crew_fleet_units_online", nil); got != 3 {
		t.Errorf("online = %v, want 3 (dead counts as connected)", got)
	}
	if got := value(t, f, "crew_fleet_unit_state", map[string]string{"unit": "crew-01", "state": "dead"}); got != 1 {
		t.Errorf("crew-01 dead gauge = %v", got)
	}
	if got := value(t, f, "crew_fleet_unit_state", map[string]string{"unit": "crew-01", "state": "working"}); got != 0 {
		t.Errorf("crew-01 working gauge = %v, want cleared", got)
	}
	// The first poll has nothing before it to measure from; each later poll
	// credits the elapsed time to the state the unit is in now.
	for l, w := range map[[2]string]float64{
		{"crew-01", "working"}: 1, {"crew-01", "dead"}: 2,
		{"crew-02", "idle"}: 0, {"crew-02", "stuck"}: 3,
		{"crew-03", "offline"}: 1, {"crew-03", "idle"}: 2,
	} {
		if got := value(t, f, "crew_fleet_unit_state_seconds_total", map[string]string{"unit": l[0], "state": l[1]}); got != w {
			t.Errorf("seconds %v = %v, want %v", l, got, w)
		}
	}
}

func TestAStalledPollLoopIsNotCreditedInFull(t *testing.T) {
	f := New(roster3)
	t0 := time.Unix(1_700_000_000, 0)
	f.ObservePoll(t0, poll("idle", "idle", "idle"))
	f.ObservePoll(t0.Add(time.Hour), poll("idle", "idle", "idle"))
	got := value(t, f, "crew_fleet_unit_state_seconds_total", map[string]string{"unit": "crew-01", "state": "idle"})
	if got != maxTickGap.Seconds() {
		t.Fatalf("idle seconds = %v, want the gap capped at %v", got, maxTickGap.Seconds())
	}
	// A clock that stepped back adds nothing rather than going negative.
	f.ObservePoll(t0, poll("idle", "idle", "idle"))
	if got := value(t, f, "crew_fleet_unit_state_seconds_total", map[string]string{"unit": "crew-01", "state": "idle"}); got != maxTickGap.Seconds() {
		t.Fatalf("idle seconds after a backwards clock = %v", got)
	}
}

func TestLabelSetIsClosed(t *testing.T) {
	f := New(roster3)
	hostile := []string{"", "x", strings.Repeat("y", 500), "crew-99", "a\"b\nc", "💥"}
	var batch []telemetry.Born
	for i, h := range hostile {
		for _, kind := range []string{"task_started", "task_done", "task_failed", "task_cancelled", "task_ended", "died", "stuck", "online", "low_health"} {
			batch = append(batch,
				born(roster3[i%len(roster3)], kind, h, time.Duration(i+1)*time.Second, h),
				born(h, kind, h, time.Duration(i+1)*time.Second, h))
		}
	}
	f.Observe(batch)
	f.ObservePoll(time.Unix(1, 0), []telemetry.UnitState{{ID: "crew-01", State: "weird"}, {ID: "crew-99", State: "idle"}})
	f.ObservePoll(time.Unix(2, 0), []telemetry.UnitState{{ID: "crew-01", State: "weird"}, {ID: "crew-99", State: "idle"}})

	allowed := map[string][]string{
		"unit":     roster3,
		"template": append(slices.Clone(Templates), TemplateOther),
		"reason":   Reasons,
		"outcome":  Outcomes,
		"state":    States,
	}
	series := 0
	for name, mf := range families(t, f) {
		if !strings.HasPrefix(name, "crew_fleet_") {
			t.Errorf("%s is outside the crew_fleet_ namespace", name)
		}
		for _, m := range mf.GetMetric() {
			series++
			for k, v := range labelsOf(m) {
				if name == "crew_fleet_task_duration_seconds" && k == "outcome" && v != OutcomeDone && v != OutcomeFailed {
					t.Errorf("%s has outcome %q", name, v)
				}
				set, ok := allowed[k]
				if !ok {
					t.Errorf("%s has unexpected label %q", name, k)
					continue
				}
				if !slices.Contains(set, v) {
					t.Errorf("%s{%s=%q} is outside the closed set", name, k, v)
				}
			}
		}
	}
	if series > maxSeries(len(roster3)) {
		t.Errorf("%d series for %d units, over the bound %d", series, len(roster3), maxSeries(len(roster3)))
	}
}

// maxSeries is the worst case the label sets allow for n units, counting each
// histogram series as its buckets plus sum and count. It is what the
// "bounded cardinality" claim comes to.
func maxSeries(n int) int {
	t, o, r, s := len(Templates)+1, len(Outcomes), len(Reasons), len(States)
	hist := t * 2 * (len(durationBuckets) + 3)
	return n*t + n*t*o + t*r + hist + n + 2*n + s + 1 + n*s*2
}

func TestEveryNameInTheCatalogueIsExported(t *testing.T) {
	f := New(roster3)
	f.Observe([]telemetry.Born{
		born("crew-01", "task_started", "haul", 0, ""),
		born("crew-01", "task_done", "haul", 5*time.Second, ""),
	})
	f.ObservePoll(time.Unix(1, 0), poll("idle", "idle", "idle"))
	mfs := families(t, f)
	for _, name := range []string{
		"crew_fleet_tasks_started_total", "crew_fleet_tasks_finished_total",
		"crew_fleet_task_duration_seconds", "crew_fleet_unit_last_done_timestamp_seconds",
		"crew_fleet_unit_deaths_total", "crew_fleet_unit_stuck_total", "crew_fleet_units",
		"crew_fleet_units_online", "crew_fleet_unit_state", "crew_fleet_unit_state_seconds_total",
	} {
		if mfs[name] == nil {
			t.Errorf("%s is not exported", name)
		}
	}
	for name, mf := range mfs {
		if mf.GetHelp() == "" {
			t.Errorf("%s has no help text", name)
		}
	}
}

func TestHandlerServesPrometheusText(t *testing.T) {
	f := New(roster3)
	f.Observe([]telemetry.Born{born("crew-01", "task_done", "haul", 5*time.Second, "")})
	rec := httptest.NewRecorder()
	f.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{
		"# HELP crew_fleet_tasks_finished_total",
		`crew_fleet_tasks_finished_total{outcome="done",template="haul",unit="crew-01"} 1`,
		"# TYPE crew_fleet_task_duration_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	// Only the fleet's own series: nothing a dependency registers by itself.
	if strings.Contains(body, "go_goroutines") {
		t.Error("the process default registry leaked into /metrics")
	}
}

// fakeUnit stands in for a crew unit's /v1/state, like the telemetry tests'.
type fakeUnit struct {
	mu   sync.Mutex
	body string
}

func (f *fakeUnit) set(s string) { f.mu.Lock(); f.body = s; f.mu.Unlock() }

func (f *fakeUnit) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = w.Write([]byte(f.body))
}

func stateBody(current, recent string) string {
	return fmt.Sprintf(`{"state":{"dead":false,"position":[0,64,0],"position_known":true},"tasks":{"current":%s,"recent":%s}}`, current, recent)
}

func TestFromAPollerEndToEnd(t *testing.T) {
	unit := &fakeUnit{}
	srv := httptest.NewServer(unit)
	defer srv.Close()
	r, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Ada","url":%q}]`, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	f := New([]string{"crew-01"})
	p := telemetry.NewPoller(r, &http.Client{}, 20*time.Millisecond)
	p.OnEvents = f.Observe
	p.OnPoll = f.ObservePoll

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	wait := func(what string, cond func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	state := func(s string) bool {
		return value(t, f, "crew_fleet_unit_state", map[string]string{"unit": "crew-01", "state": s}) == 1
	}

	unit.set(stateBody("null", "[]"))
	wait("idle", func() bool { return state("idle") })

	unit.set(stateBody(`{"id":"t1","template":"gather_and_deposit","state":"running","progress":1}`, "[]"))
	wait("working", func() bool { return state("working") })
	wait("the start", func() bool {
		return value(t, f, "crew_fleet_tasks_started_total", map[string]string{"unit": "crew-01", "template": "gather_and_deposit"}) == 1
	})

	unit.set(stateBody("null", `[{"id":"t1","state":"failed","reason":"no axe in inventory","created":"2026-10-24T10:00:00Z","updated":"2026-10-24T10:00:12Z"}]`))
	wait("the failure", func() bool {
		return value(t, f, "crew_fleet_task_failures_total", map[string]string{"template": "gather_and_deposit", "reason": "missing_item"}) == 1
	})
	h := find(families(t, f)["crew_fleet_task_duration_seconds"], map[string]string{"template": "gather_and_deposit", "outcome": "failed"})
	if h == nil || math.Abs(h.Histogram.GetSampleSum()-12) > 1e-9 {
		t.Fatalf("failed duration = %v, want one 12 s sample", h)
	}

	srv.Close()
	wait("offline", func() bool { return state("offline") })
}

func TestVitalsGaugesFollowReadingsAndVanishWhenUnknown(t *testing.T) {
	f := New(roster3)
	hp, food := float32(14), float32(0)
	at := time.Unix(1_700_000_000, 0)
	f.ObservePoll(at, []telemetry.UnitState{
		{ID: roster3[0], State: "working", Health: &hp, Hunger: &food},
		{ID: roster3[1], State: "offline"},
		{ID: "crew-99", State: "idle", Health: &hp},
	})
	fam := families(t, f)
	if got := value(t, f, "crew_fleet_unit_health", map[string]string{"unit": roster3[0]}); got != 14 {
		t.Fatalf("health = %v, want 14", got)
	}
	if find(fam["crew_fleet_unit_hunger"], map[string]string{"unit": roster3[0]}) == nil {
		t.Fatal("a hunger of zero is a reading and must stay exported")
	}
	for _, name := range []string{"crew_fleet_unit_health", "crew_fleet_unit_hunger"} {
		for _, u := range []string{roster3[1], "crew-99"} {
			if mf := fam[name]; mf != nil && find(mf, map[string]string{"unit": u}) != nil {
				t.Fatalf("%s has a series for %s, which reported nothing or is off the roster", name, u)
			}
		}
	}

	f.ObservePoll(at.Add(time.Second), []telemetry.UnitState{{ID: roster3[0], State: "offline"}})
	if mf := families(t, f)["crew_fleet_unit_health"]; mf != nil && find(mf, map[string]string{"unit": roster3[0]}) != nil {
		t.Fatal("health still exported after the unit stopped reporting it")
	}
}
