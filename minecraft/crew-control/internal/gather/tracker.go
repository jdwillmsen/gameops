package gather

import (
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

// maxPerItem caps what one poll can credit to one item. A full stack arrives
// in a single poll only when it moved from a container, which is exactly what
// this package must not count, so a cap turns a misread withdrawal into a
// bounded blip instead of a spike.
const maxPerItem = 64

// gathering are the task templates whose inventory growth is a pickup. A
// haul or withdraw also fills the inventory, from a chest, and a craft fills
// it from the grid; none of those is yield.
var gathering = map[string]bool{"gather_and_deposit": true, "mine_shaft": true}

// Gathering reports whether growth under this task template counts.
func Gathering(template string) bool { return gathering[template] }

// Gain is what one poll added to a unit's inventory while it gathered.
type Gain struct {
	// ByClass sums the items gained per class; only classes that grew are present.
	ByClass map[string]int
	// Items is the same growth per item name. It is operator-only: names are
	// the unit's own words, so nothing public may print them.
	Items map[string]int
}

// Total is every item gained.
func (g Gain) Total() (n int) {
	for _, v := range g.ByClass {
		n += v
	}
	return
}

// Tracker diffs each unit's inventory from one poll to the next.
type Tracker struct {
	mu    sync.Mutex
	units map[string]map[string]int
}

func NewTracker() *Tracker { return &Tracker{units: map[string]map[string]int{}} }

func totals(items []telemetry.LiveItem) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		if it.Count > 0 && it.Name != "" {
			out[it.Name] += it.Count
		}
	}
	return out
}

// Observe reads one unit's inventory at a poll and returns what it gained.
//
// The baseline moves on every poll, gathering or not, so items that arrive
// from a chest or a craft are absorbed into it and never counted later. A
// unit that is offline or dead has no baseline: what it did across that gap
// is unknown, and so is the first sight after crew-control starts. Both only
// set the baseline, as the fight counters do.
func (t *Tracker) Observe(unit string, present bool, template string, items []telemetry.LiveItem) Gain {
	t.mu.Lock()
	defer t.mu.Unlock()
	// A nil inventory is a poll that could not read one, and is not told
	// apart from a genuinely empty one: taking it as empty would make the
	// next full read look like every stack was just picked up. Treating it as
	// unknown only costs the one poll's gain.
	if !present || items == nil {
		delete(t.units, unit)
		return Gain{}
	}
	cur := totals(items)
	prev, seen := t.units[unit]
	t.units[unit] = cur
	if !seen || !Gathering(template) {
		return Gain{}
	}
	var g Gain
	for name, n := range cur {
		d := min(n-prev[name], maxPerItem)
		if d <= 0 {
			continue
		}
		if g.ByClass == nil {
			g.ByClass, g.Items = map[string]int{}, map[string]int{}
		}
		g.ByClass[Classify(name)] += d
		g.Items[name] += d
	}
	return g
}
