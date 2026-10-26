// Package insight turns the unit event stream into a small set of findings
// with deterministic rules: no model, no randomness, no state. Every rule is
// a pure function over event slices, so a replay of recorded history and the
// live tick run identical code, and a finding always names the events it
// rests on.
package insight

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

const (
	RuleZeroSuccess = "R1"
	RuleThroughput  = "R2"
	RuleLongTask    = "R3"
	RuleCommonMode  = "R4"

	SevWarn = "warn"
	SevCrit = "crit"
)

// Config holds every threshold. Each exists for a reason recorded in the
// rule catalogue, so none is a bare magic number.
type Config struct {
	// R1: this many consecutive failures with one reason and no success
	// for R1Quiet.
	R1Failures int
	R1Quiet    time.Duration

	// R2: EWMA chart on per-minute useful completions. The baseline is the
	// BaselineMinutes before the Recent window; with fewer than MinSamples
	// minutes of history there is no verdict.
	Lambda          float64
	K               float64
	BaselineMinutes int
	RecentMinutes   int
	MinSamples      int
	SigmaFloor      float64

	// R3: per-template duration threshold median + R3K*sigma, where sigma
	// is the scaled MAD floored at SigmaFloorFrac of the median and at
	// SigmaFloorMin.
	R3K            float64
	SigmaFloorFrac float64
	SigmaFloorMin  time.Duration

	// R4: this share of working units raising R2 within CommonWindow, with
	// at least CommonErrors session errors in it.
	CommonShare  float64
	CommonWindow time.Duration
	CommonErrors int
}

func DefaultConfig() Config {
	return Config{
		R1Failures: 3, R1Quiet: 5 * time.Minute,
		Lambda: 0.2, K: 3, BaselineMinutes: 20, RecentMinutes: 5, MinSamples: 10, SigmaFloor: 0.5,
		R3K: 3.5, SigmaFloorFrac: 0.05, SigmaFloorMin: time.Second,
		CommonShare: 0.5, CommonWindow: 2 * time.Minute, CommonErrors: 5,
	}
}

// Finding is one rule's verdict. Unit is empty for a fleet finding.
type Finding struct {
	Rule     string
	Unit     string
	Severity string
	Evidence []int64
	// Detail is a short, coordinate-free sentence for the console.
	Detail string
}

// Key is the dedupe identity: one live insight per rule and unit.
func (f Finding) Key() string { return f.Rule + "/" + f.Unit }

// OpenTask is a task that started and has not ended.
type OpenTask struct {
	StartID  int64
	Unit     string
	Template string
	Since    time.Time
}

// Input is everything one evaluation reads.
type Input struct {
	Now    time.Time
	Events []unitlog.Event
	Open   []OpenTask
}

func byUnit(es []unitlog.Event) map[string][]unitlog.Event {
	m := map[string][]unitlog.Event{}
	for _, e := range es {
		m[e.Unit] = append(m[e.Unit], e)
	}
	for _, s := range m {
		sort.SliceStable(s, func(i, j int) bool {
			if !s[i].At.Equal(s[j].At) {
				return s[i].At.Before(s[j].At)
			}
			return s[i].ID < s[j].ID
		})
	}
	return m
}

// ZeroSuccess is R1. It is an absolute rule on purpose: a unit that never
// succeeds has a zero baseline, so no baseline rule can ever call it unusual.
// es must be one unit's events, oldest first.
func ZeroSuccess(cfg Config, now time.Time, unit string, es []unitlog.Event) *Finding {
	var lastDone time.Time
	var first time.Time
	for _, e := range es {
		if e.At.After(now) {
			continue
		}
		if first.IsZero() {
			first = e.At
		}
		if e.Useful() {
			lastDone = e.At
		}
	}
	quietFrom := lastDone
	if quietFrom.IsZero() {
		quietFrom = first
	}
	if quietFrom.IsZero() || now.Sub(quietFrom) < cfg.R1Quiet {
		return nil
	}
	var run []int64
	reason := ""
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if e.At.After(now) || e.Kind != unitlog.KindTaskFailed {
			if e.Kind == unitlog.KindTaskDone && !e.Idle {
				break
			}
			continue
		}
		if reason == "" {
			reason = e.Reason
		}
		if e.Reason != reason {
			break
		}
		run = append(run, e.ID)
	}
	if len(run) < cfg.R1Failures {
		return nil
	}
	slices.Reverse(run)
	return &Finding{Rule: RuleZeroSuccess, Unit: unit, Severity: SevCrit, Evidence: run,
		Detail: "no completed task for " + now.Sub(quietFrom).Round(time.Second).String() + ", last failures all " + quoteReason(reason)}
}

func quoteReason(r string) string {
	if r == "" {
		return "without a reason code"
	}
	return r
}

// perMinute counts useful completions in the n whole minutes ending at end.
func perMinute(es []unitlog.Event, end time.Time, n int) []float64 {
	out := make([]float64, n)
	start := end.Add(-time.Duration(n) * time.Minute)
	for _, e := range es {
		if !e.Useful() || e.At.Before(start) || !e.At.Before(end) {
			continue
		}
		out[int(e.At.Sub(start)/time.Minute)]++
	}
	return out
}

// Throughput is R2: an EWMA control chart on the unit's own completions per
// minute, against its own recent past. It reports only a drop, and gives no
// verdict until the unit has MinSamples minutes of history. es must be one
// unit's events.
func Throughput(cfg Config, now time.Time, unit string, es []unitlog.Event) *Finding {
	end := now.Truncate(time.Minute)
	recentStart := end.Add(-time.Duration(cfg.RecentMinutes) * time.Minute)
	var first time.Time
	for _, e := range es {
		if first.IsZero() || e.At.Before(first) {
			first = e.At
		}
	}
	if first.IsZero() {
		return nil
	}
	base := perMinute(es, recentStart, cfg.BaselineMinutes)
	// Minutes before the unit's first event are not samples of it.
	skip := 0
	if first.After(recentStart.Add(-time.Duration(cfg.BaselineMinutes) * time.Minute)) {
		skip = int(first.Sub(recentStart.Add(-time.Duration(cfg.BaselineMinutes)*time.Minute)) / time.Minute)
	}
	if skip > len(base) {
		skip = len(base)
	}
	base = base[skip:]
	if len(base) < cfg.MinSamples {
		return nil
	}
	med := median(base)
	sigma := math.Max(madScale*mad(base, med), cfg.SigmaFloor)
	z := med
	rec := perMinute(es, end, cfg.RecentMinutes)
	for _, x := range rec {
		z = cfg.Lambda*x + (1-cfg.Lambda)*z
	}
	t := float64(len(rec))
	sigmaZ := sigma * math.Sqrt(cfg.Lambda/(2-cfg.Lambda)*(1-math.Pow(1-cfg.Lambda, 2*t)))
	if z >= med-cfg.K*sigmaZ {
		return nil
	}
	var ev []int64
	for _, e := range es {
		if e.Useful() && !e.At.Before(recentStart) && e.At.Before(end) {
			ev = append(ev, e.ID)
		}
	}
	return &Finding{Rule: RuleThroughput, Unit: unit, Severity: SevWarn, Evidence: ev,
		Detail: "completions per minute fell below its own baseline"}
}

// LongTasks is R3. history maps a template to the durations of its finished
// runs. The sigma floor matters: a template with a 0.2 s median and a zero
// MAD would otherwise flag every slightly slower run.
func LongTasks(cfg Config, now time.Time, open []OpenTask, history map[string][]time.Duration) []Finding {
	var out []Finding
	for _, t := range open {
		h := history[t.Template]
		if len(h) < cfg.MinSamples {
			continue
		}
		secs := make([]float64, len(h))
		for i, d := range h {
			secs[i] = d.Seconds()
		}
		med := median(secs)
		sigma := math.Max(mad(secs, med)/0.6745, math.Max(cfg.SigmaFloorFrac*med, cfg.SigmaFloorMin.Seconds()))
		limit := med + cfg.R3K*sigma
		if now.Sub(t.Since).Seconds() <= limit {
			continue
		}
		out = append(out, Finding{Rule: RuleLongTask, Unit: t.Unit, Severity: SevWarn, Evidence: []int64{t.StartID},
			Detail: t.Template + " has run longer than its usual range"})
	}
	return out
}

// Evaluate runs every rule. R4 replaces the per-unit R2 findings of the units
// it explains, so one outage is one insight, not twenty.
func Evaluate(cfg Config, in Input, history map[string][]time.Duration) []Finding {
	units := byUnit(in.Events)
	names := make([]string, 0, len(units))
	for u := range units {
		names = append(names, u)
	}
	sort.Strings(names)

	var out []Finding
	for _, u := range names {
		if f := ZeroSuccess(cfg, in.Now, u, units[u]); f != nil {
			out = append(out, *f)
		}
	}

	// Working units are those that completed something in the baseline.
	working := map[string]bool{}
	raised := map[string]*Finding{}
	for _, u := range names {
		es := units[u]
		start := in.Now.Add(-time.Duration(cfg.BaselineMinutes+cfg.RecentMinutes) * time.Minute)
		for _, e := range es {
			if e.Useful() && e.At.After(start) {
				working[u] = true
				break
			}
		}
		if !working[u] {
			continue
		}
		if f := Throughput(cfg, in.Now, u, es); f != nil {
			raised[u] = f
		}
	}

	// A unit counts toward the common cause if R2 held at any point in the
	// window, since units cross their limits a minute or two apart.
	inWindow := map[string]bool{}
	for u := range working {
		for off := time.Duration(0); off <= cfg.CommonWindow; off += 30 * time.Second {
			if Throughput(cfg, in.Now.Add(-off), u, units[u]) != nil {
				inWindow[u] = true
				break
			}
		}
	}
	var sessErrs []int64
	for _, e := range in.Events {
		if e.Kind == unitlog.KindSession && !e.At.After(in.Now) && in.Now.Sub(e.At) <= cfg.CommonWindow {
			sessErrs = append(sessErrs, e.ID)
		}
	}
	common := len(working) > 0 && float64(len(inWindow)) >= cfg.CommonShare*float64(len(working)) && len(sessErrs) >= cfg.CommonErrors
	if common {
		slices.Sort(sessErrs)
		out = append(out, Finding{Rule: RuleCommonMode, Severity: SevCrit, Evidence: sessErrs,
			Detail: "most working units slowed together alongside session errors"})
	}
	for _, u := range names {
		if f, ok := raised[u]; ok && !(common && inWindow[u]) {
			out = append(out, *f)
		}
	}
	out = append(out, LongTasks(cfg, in.Now, in.Open, history)...)
	return out
}
