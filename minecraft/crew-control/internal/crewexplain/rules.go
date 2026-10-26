package crewexplain

import (
	"fmt"
	"sort"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/explain"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/insight"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

// Causes a draft may name. The rule classes come from the explainer; the two
// extra values say there was nothing to explain or no rule covers it.
const (
	CauseNone        = "none"
	CauseUnexplained = "unexplained"
)

// Causes is the closed set, in the order the schema lists it.
var Causes = []string{
	explain.ClassMissingTool, explain.ClassSessionLost, explain.ClassGeometryTrap,
	explain.ClassLowHunger, explain.ClassLowHealth, CauseUnexplained, CauseNone,
}

// RuleFinding is what the deterministic engines say about a window.
type RuleFinding struct {
	// Dominant is the cause class with the most failed tasks.
	Dominant    string
	Failures    int
	Explained   int
	Unexplained int
	Lines       []string
	// Evidence is every event id a finding rests on.
	Evidence []int64
}

// Rules runs the per-unit explainer and the fleet insight rules over a window.
func Rules(w Window) RuleFinding {
	by := map[string][]unitlog.Event{}
	var all []unitlog.Event
	for _, e := range w.Events {
		u := e.unitlog()
		by[e.Unit] = append(by[e.Unit], u)
		all = append(all, u)
	}
	units := make([]string, 0, len(by))
	for u := range by {
		units = append(units, u)
	}
	sort.Strings(units)

	rf := RuleFinding{Dominant: CauseNone}
	counts := map[string]int{}
	evidence := map[int64]bool{}
	for _, u := range units {
		r := explain.Explain(u, w.From, w.To, by[u])
		rf.Failures += r.Total
		rf.Explained += r.Explained
		rf.Unexplained += r.Unexplained.Count
		for _, f := range r.Findings {
			counts[f.Class] += f.Count
			rf.Lines = append(rf.Lines, fmt.Sprintf("%s: %s (%d failed task(s), rule %s, events %v)", u, f.Sentence, f.Count, f.Rule, f.Chain))
			for _, id := range f.Chain {
				evidence[id] = true
			}
			for _, id := range f.Failures {
				evidence[id] = true
			}
		}
		if r.Unexplained.Count > 0 {
			rf.Lines = append(rf.Lines, fmt.Sprintf("%s: %s (events %v)", u, r.Unexplained.Sentence, r.Unexplained.Failures))
			for _, id := range r.Unexplained.Failures {
				evidence[id] = true
			}
		}
	}

	best, bestN := "", 0
	for _, c := range Causes {
		if counts[c] > bestN {
			best, bestN = c, counts[c]
		}
	}
	switch {
	case rf.Failures == 0:
		rf.Dominant = CauseNone
	case rf.Unexplained > bestN:
		rf.Dominant = CauseUnexplained
	default:
		rf.Dominant = best
	}

	for _, f := range insight.Evaluate(insight.DefaultConfig(), insight.Input{Now: w.To, Events: all}, nil) {
		who := f.Unit
		if who == "" {
			who = "fleet"
		}
		rf.Lines = append(rf.Lines, fmt.Sprintf("%s %s %s: %s (events %v)", f.Rule, f.Severity, who, f.Detail, f.Evidence))
		for _, id := range f.Evidence {
			evidence[id] = true
		}
	}
	for id := range evidence {
		rf.Evidence = append(rf.Evidence, id)
	}
	sort.Slice(rf.Evidence, func(i, j int) bool { return rf.Evidence[i] < rf.Evidence[j] })
	return rf
}
