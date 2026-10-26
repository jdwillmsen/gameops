package insight

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

const (
	StateFiring   = "firing"
	StateResolved = "resolved"

	// keepResolved bounds memory: resolved insights are history, and the
	// event store is the long record.
	keepResolved = 200
)

// Insight is a finding with a life: when it appeared and whether it holds.
type Insight struct {
	ID         int64
	Rule       string
	Unit       string
	Severity   string
	State      string
	FirstSeen  time.Time
	ResolvedAt time.Time
	Evidence   []int64
	Detail     string
}

// Tracker is the firing/resolved state machine, keyed on (rule, unit) so a
// condition that persists is one insight, not one per tick.
type Tracker struct {
	mu     sync.Mutex
	next   int64
	live   map[string]*Insight
	closed []Insight
}

func NewTracker() *Tracker { return &Tracker{live: map[string]*Insight{}} }

// Apply folds one evaluation in and reports whether anything changed.
func (t *Tracker) Apply(now time.Time, fs []Finding) (changed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[string]bool{}
	for _, f := range fs {
		k := f.Key()
		seen[k] = true
		if in, ok := t.live[k]; ok {
			if !slices.Equal(in.Evidence, f.Evidence) || in.Detail != f.Detail || in.Severity != f.Severity {
				in.Evidence, in.Detail, in.Severity = slices.Clone(f.Evidence), f.Detail, f.Severity
				changed = true
			}
			continue
		}
		t.next++
		t.live[k] = &Insight{ID: t.next, Rule: f.Rule, Unit: f.Unit, Severity: f.Severity, State: StateFiring,
			FirstSeen: now, Evidence: slices.Clone(f.Evidence), Detail: f.Detail}
		changed = true
	}
	for k, in := range t.live {
		if seen[k] {
			continue
		}
		in.State, in.ResolvedAt = StateResolved, now
		t.closed = append(t.closed, *in)
		delete(t.live, k)
		changed = true
	}
	if n := len(t.closed); n > keepResolved {
		t.closed = slices.Clone(t.closed[n-keepResolved:])
	}
	return changed
}

// List returns firing insights first, then resolved ones, newest first within
// each.
func (t *Tracker) List() []Insight {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Insight, 0, len(t.live)+len(t.closed))
	for _, in := range t.live {
		out = append(out, *in)
	}
	slices.SortFunc(out, func(a, b Insight) int { return cmp.Compare(b.ID, a.ID) })
	for i := len(t.closed) - 1; i >= 0; i-- {
		out = append(out, t.closed[i])
	}
	return out
}
