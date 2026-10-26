package jobs

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrCycle is returned for steps whose dependencies loop; a loop has no
// longest path.
var ErrCycle = errors.New("jobs: steps depend on each other in a cycle")

// PathStep is one step as the critical-path function sees it. Known false
// means no duration is available, which is different from a duration of zero.
type PathStep struct {
	Key       string
	DependsOn []string
	Duration  time.Duration
	Known     bool
}

// StepTiming is the verdict for one step.
type StepTiming struct {
	// Critical: the step is on the reported path.
	Critical bool
	// Slack is how long the step could slip without delaying the job. A
	// step off the path can also have zero slack when another branch is
	// exactly as long.
	Slack time.Duration
	// Unknown: no duration, so the step is never critical and its slack is
	// not computed. It counts as zero length in its neighbours' timing.
	Unknown bool
}

// PathResult is a job's longest chain and every step's slack.
type PathResult struct {
	// Path lists the critical steps in dependency order.
	Path   []string
	Length time.Duration
	Steps  map[string]StepTiming
}

// CriticalPath finds the longest chain by duration over DependsOn using a
// longest-path pass in topological order, so it is linear in steps plus
// edges. Ties between equally long chains go to the smaller step key, which
// keeps the answer stable between calls.
func CriticalPath(steps []PathStep) (PathResult, error) {
	idx := make(map[string]int, len(steps))
	for i, s := range steps {
		if _, dup := idx[s.Key]; dup {
			return PathResult{}, fmt.Errorf("%w: duplicate step %q", ErrInvalid, s.Key)
		}
		idx[s.Key] = i
	}
	n := len(steps)
	succ := make([][]int, n)
	indeg := make([]int, n)
	deps := make([][]int, n)
	for i, s := range steps {
		seen := map[string]bool{}
		for _, d := range s.DependsOn {
			j, ok := idx[d]
			if !ok {
				return PathResult{}, fmt.Errorf("%w: step %q depends on unknown step %q", ErrInvalid, s.Key, d)
			}
			if seen[d] {
				continue
			}
			seen[d] = true
			succ[j] = append(succ[j], i)
			deps[i] = append(deps[i], j)
			indeg[i]++
		}
	}
	dur := func(i int) time.Duration {
		if steps[i].Known {
			return steps[i].Duration
		}
		return 0
	}

	// Kahn's algorithm, taking the smallest key first so the order is fixed.
	var order []int
	ready := []int{}
	for i := range steps {
		if indeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	byKey := func(a, b int) int { return compareKeys(steps[a].Key, steps[b].Key) }
	for len(ready) > 0 {
		slices.SortFunc(ready, byKey)
		i := ready[0]
		ready = ready[1:]
		order = append(order, i)
		for _, j := range succ[i] {
			if indeg[j]--; indeg[j] == 0 {
				ready = append(ready, j)
			}
		}
	}
	if len(order) != n {
		return PathResult{}, ErrCycle
	}

	es := make([]time.Duration, n)
	ef := make([]time.Duration, n)
	var length time.Duration
	for _, i := range order {
		for _, d := range deps[i] {
			es[i] = max(es[i], ef[d])
		}
		ef[i] = es[i] + dur(i)
		length = max(length, ef[i])
	}
	lf := make([]time.Duration, n)
	for k := n - 1; k >= 0; k-- {
		i := order[k]
		lf[i] = length
		for _, j := range succ[i] {
			lf[i] = min(lf[i], lf[j]-dur(j))
		}
	}

	res := PathResult{Length: length, Steps: make(map[string]StepTiming, n)}
	for i, s := range steps {
		res.Steps[s.Key] = StepTiming{Slack: lf[i] - dur(i) - es[i], Unknown: !s.Known}
		if !s.Known {
			res.Steps[s.Key] = StepTiming{Unknown: true}
		}
	}

	// Walk back from the known step that finishes last, always through the
	// dependency that finishes exactly when the step starts.
	end := -1
	for i := range steps {
		if !steps[i].Known {
			continue
		}
		if end < 0 || ef[i] > ef[end] || (ef[i] == ef[end] && byKey(i, end) < 0) {
			end = i
		}
	}
	for i := end; i >= 0; {
		res.Path = append(res.Path, steps[i].Key)
		t := res.Steps[steps[i].Key]
		t.Critical = true
		res.Steps[steps[i].Key] = t
		next := -1
		for _, d := range deps[i] {
			if !steps[d].Known || ef[d] != es[i] {
				continue
			}
			if next < 0 || byKey(d, next) < 0 {
				next = d
			}
		}
		i = next
	}
	slices.Reverse(res.Path)
	return res, nil
}

func compareKeys(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

