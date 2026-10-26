// Package invhistory records what each crew unit gains and loses and answers
// what changed over a window. Changes ride the event store as one kind, so
// they get its retention, its database fallback and its operator gate; the
// table is not touched and no migration is needed.
package invhistory

import (
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

// Kind is the event kind a recorded change is stored under.
const Kind = "inventory_changed"

// MinGap is the least time between two recorded changes of one unit. A unit
// mining or hauling changes its inventory every poll; recording each step
// would bury the event stream, so a gap's worth of steps is one net change.
const MinGap = 30 * time.Second

// Change is one item's total count across every slot, before and after.
type Change struct {
	Item string `json:"item"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

type payload struct {
	Changes []Change `json:"changes"`
}

type unitState struct {
	// base is the totals as of the last recorded change (or first sight).
	base map[string]int
	last time.Time
}

// Tracker diffs each unit's inventory against the last one it recorded.
type Tracker struct {
	mu    sync.Mutex
	units map[string]*unitState
}

func NewTracker() *Tracker { return &Tracker{units: map[string]*unitState{}} }

func totals(items []telemetry.LiveItem) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		if it.Count > 0 && it.Name != "" {
			out[it.Name] += it.Count
		}
	}
	return out
}

// Observe compares a unit's inventory with the last recorded one and returns
// the change event, if any. The first sight of a unit is its baseline, not a
// change: after a restart the gap is unknown, and inventing a gain from
// nothing would make every restart look like a haul. An offline unit reports
// no inventory, so its baseline is kept and what it did meanwhile shows as
// one change when it returns.
func (t *Tracker) Observe(now time.Time, unit string, online bool, items []telemetry.LiveItem) (events.Event, bool) {
	if !online {
		return events.Event{}, false
	}
	cur := totals(items)
	t.mu.Lock()
	defer t.mu.Unlock()
	st, seen := t.units[unit]
	if !seen {
		t.units[unit] = &unitState{base: cur}
		return events.Event{}, false
	}
	if !st.last.IsZero() && now.Sub(st.last) < MinGap {
		return events.Event{}, false
	}
	changes := diff(st.base, cur)
	if len(changes) == 0 {
		return events.Event{}, false
	}
	st.base, st.last = cur, now
	b, _ := json.Marshal(payload{Changes: changes})
	return events.Event{Unit: unit, At: now, Kind: Kind, Severity: events.SevInfo, Payload: b}, true
}

// diff lists the items whose total differs, in name order so the payload is
// stable.
func diff(from, to map[string]int) []Change {
	names := map[string]struct{}{}
	for n := range from {
		names[n] = struct{}{}
	}
	for n := range to {
		names[n] = struct{}{}
	}
	var out []Change
	for n := range names {
		if from[n] != to[n] {
			out = append(out, Change{Item: n, From: from[n], To: to[n]})
		}
	}
	slices.SortFunc(out, func(a, b Change) int {
		switch {
		case a.Item < b.Item:
			return -1
		case a.Item > b.Item:
			return 1
		}
		return 0
	})
	return out
}
