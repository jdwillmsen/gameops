// Package explain answers "why did this unit fail or stall" with a fixed list
// of rules over the unit's own event history. Every finding names the rule
// that produced it and the event ids it rests on; a failure no rule covers is
// reported as unexplained and is never given a guessed cause.
package explain

import (
	"fmt"
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

// Cause classes. The catalogue in the README lists what each means.
const (
	ClassMissingTool  = "missing_tool"
	ClassSessionLost  = "session_lost"
	ClassGeometryTrap = "geometry_trap"
	ClassLowHunger    = "low_hunger"
	ClassLowHealth    = "low_health"
)

// Reason codes the rules look for on a failed task.
const (
	reasonMissingTool = "missing_tool"
	reasonSessionLost = "session_lost"
	reasonNoPath      = "no_path"
)

const (
	// sessionWindow: a session that ended this soon after a session error
	// is read as caused by it.
	sessionWindow = 60 * time.Second
	// vitalsWindow: how far back low food or health still counts as
	// preceding a stall.
	vitalsWindow = 2 * time.Minute
	// kitWindow: how far back a failed kit pickup can still explain a
	// missing tool.
	kitWindow = 30 * time.Minute
	// trapRepeats: failed paths from one unchanged anchor before the
	// pattern is called a trap.
	trapRepeats = 3

	// maxFailures bounds the evidence list of a finding.
	maxFailures = 20
)

// Finding is a group of failures with one cause class and rule.
type Finding struct {
	Class string `json:"cause_class"`
	Rule  string `json:"rule"`
	// Chain is the cause chain of the latest instance, oldest event first.
	Chain []int64 `json:"chain"`
	Count int     `json:"count"`
	// Failures lists up to maxFailures of the failed-task event ids.
	Failures []int64 `json:"failures"`
	Sentence string  `json:"sentence"`
}

// Result is what the explainer returns for one unit and window.
type Result struct {
	Unit string    `json:"unit"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Total counts failed tasks in the window.
	Total       int       `json:"total"`
	Explained   int       `json:"explained"`
	Findings    []Finding `json:"findings"`
	Unexplained Unexplain `json:"unexplained"`
}

// Unexplain is the bucket for failures no rule covers. It is as prominent as
// any finding: not knowing is an answer.
type Unexplain struct {
	Count    int     `json:"count"`
	Failures []int64 `json:"failures"`
	Sentence string  `json:"sentence"`
}

type verdict struct {
	class, rule string
	chain       []int64
	sentence    string
}

// Explain classifies the failures in [from, to]. es is one unit's events,
// oldest first, and should reach back before from so that predecessors are
// visible.
func Explain(unit string, from, to time.Time, es []unitlog.Event) Result {
	byID := make(map[int64]unitlog.Event, len(es))
	for _, e := range es {
		byID[e.ID] = e
	}
	res := Result{Unit: unit, From: from, To: to, Findings: []Finding{}}
	groups := map[string]*Finding{}
	var order []string
	res.Unexplained.Failures = []int64{}

	noPathRuns := trapRuns(es)

	for i, e := range es {
		if e.Kind != unitlog.KindTaskFailed || e.At.Before(from) || e.At.After(to) {
			continue
		}
		res.Total++
		v, ok := classify(e, es[:i], byID, noPathRuns)
		if !ok {
			res.Unexplained.Count++
			if len(res.Unexplained.Failures) < maxFailures {
				res.Unexplained.Failures = append(res.Unexplained.Failures, e.ID)
			}
			continue
		}
		res.Explained++
		key := v.class + "/" + v.rule
		g, ok := groups[key]
		if !ok {
			g = &Finding{Class: v.class, Rule: v.rule, Failures: []int64{}}
			groups[key] = g
			order = append(order, key)
		}
		g.Count++
		g.Chain, g.Sentence = v.chain, v.sentence
		if len(g.Failures) < maxFailures {
			g.Failures = append(g.Failures, e.ID)
		}
	}
	for _, k := range order {
		res.Findings = append(res.Findings, *groups[k])
	}
	sort.SliceStable(res.Findings, func(i, j int) bool { return res.Findings[i].Count > res.Findings[j].Count })
	res.Unexplained.Sentence = unexplainedSentence(res.Unexplained.Count)
	return res
}

func unexplainedSentence(n int) string {
	if n == 0 {
		return "every failure in this window has a rule-derived cause"
	}
	return fmt.Sprintf("%d failed task(s) have no cause in the window; no cause is guessed", n)
}

func classify(f unitlog.Event, before []unitlog.Event, byID map[int64]unitlog.Event, runs map[int64][]int64) (verdict, bool) {
	switch f.Reason {
	case reasonMissingTool:
		return missingTool(f, before, byID)
	case reasonSessionLost:
		return sessionLost(f, before, byID)
	case reasonNoPath:
		if run, ok := runs[f.ID]; ok {
			return verdict{ClassGeometryTrap, "no_path.same_anchor", run,
				"repeated failed paths from an unchanged position: a geometry trap, cause unknown"}, true
		}
	}
	return vitals(f, before)
}

// explicitCause returns the event f names as its cause when it is one of the
// kinds the rule accepts.
func explicitCause(f unitlog.Event, byID map[int64]unitlog.Event, kinds ...string) (unitlog.Event, bool) {
	if f.Cause == 0 {
		return unitlog.Event{}, false
	}
	c, ok := byID[f.Cause]
	if !ok {
		return c, false
	}
	for _, k := range kinds {
		if c.Kind == k {
			return c, true
		}
	}
	return c, false
}

// lastBefore finds the latest event of the given kinds on or before f, no
// older than window.
func lastBefore(f unitlog.Event, before []unitlog.Event, window time.Duration, kinds ...string) (unitlog.Event, bool) {
	for i := len(before) - 1; i >= 0; i-- {
		e := before[i]
		if f.At.Sub(e.At) > window {
			break
		}
		for _, k := range kinds {
			if e.Kind == k {
				return e, true
			}
		}
	}
	return unitlog.Event{}, false
}

func missingTool(f unitlog.Event, before []unitlog.Event, byID map[int64]unitlog.Event) (verdict, bool) {
	c, ok := explicitCause(f, byID, unitlog.KindKitFailed, unitlog.KindDied)
	if !ok {
		c, ok = lastBefore(f, before, kitWindow, unitlog.KindKitFailed, unitlog.KindDied)
	}
	if !ok {
		return verdict{}, false
	}
	if c.Kind == unitlog.KindDied {
		return verdict{ClassMissingTool, "missing_tool.died", []int64{c.ID, f.ID},
			"a tool was missing because the unit died and its kit was stripped"}, true
	}
	return verdict{ClassMissingTool, "missing_tool.kit_failed", []int64{c.ID, f.ID},
		"a tool was missing because collecting the kit from its chest failed"}, true
}

func sessionLost(f unitlog.Event, before []unitlog.Event, byID map[int64]unitlog.Event) (verdict, bool) {
	c, ok := explicitCause(f, byID, unitlog.KindSession)
	if !ok {
		c, ok = lastBefore(f, before, sessionWindow, unitlog.KindSession)
	}
	if !ok {
		return verdict{}, false
	}
	return verdict{ClassSessionLost, "session_lost.session_error", []int64{c.ID, f.ID},
		"the game session ended right after a session error"}, true
}

func vitals(f unitlog.Event, before []unitlog.Event) (verdict, bool) {
	c, ok := lastBefore(f, before, vitalsWindow, unitlog.KindLowHunger, unitlog.KindLowHealth)
	if !ok {
		return verdict{}, false
	}
	if c.Kind == unitlog.KindLowHealth {
		return verdict{ClassLowHealth, "vitals.low_health", []int64{c.ID, f.ID}, "health was low just before the task failed"}, true
	}
	return verdict{ClassLowHunger, "vitals.low_hunger", []int64{c.ID, f.ID}, "hunger was low just before the task failed"}, true
}

// trapRuns maps each no_path failure that belongs to a run of at least
// trapRepeats failures from one anchor to that run's ids. A run ends when the
// anchor changes or the unit succeeds.
func trapRuns(es []unitlog.Event) map[int64][]int64 {
	out := map[int64][]int64{}
	var run []unitlog.Event
	flush := func() {
		if len(run) >= trapRepeats {
			ids := make([]int64, len(run))
			for i, e := range run {
				ids[i] = e.ID
			}
			for _, e := range run {
				out[e.ID] = ids
			}
		}
		run = nil
	}
	for _, e := range es {
		switch {
		case e.Kind == unitlog.KindTaskDone && !e.Idle:
			flush()
		case e.Kind == unitlog.KindTaskFailed && e.Reason == reasonNoPath && e.Anchor != "":
			if len(run) > 0 && run[0].Anchor != e.Anchor {
				flush()
			}
			run = append(run, e)
		}
	}
	flush()
	return out
}
