package telemetry

import (
	"fmt"
	"time"
)

// Wear buckets. The public feed shows only these; the exact percentage stays
// on the operator-only views and the gauge.
const (
	BucketLow      = "low"
	BucketCritical = "critical"

	// wearLowPct and wearCriticalPct are the durability left at or under
	// which a piece is reported. Critical is about a stack's worth of use for
	// a fast-wearing tool, which is the warning a gather needs.
	wearLowPct      = 25
	wearCriticalPct = 10
)

// wearSlots are the places an item's wear is watched, in the order of
// observation.wear and track.wear.
var wearSlots = [...]string{"held", "helmet", "chest", "legs", "boots"}

const heldWear = 0

// bucketOf is 0 when the piece is fine, else 1 for low and 2 for critical, so
// a worse bucket is a larger number. An unknown reading is not a bucket and
// is skipped by the caller.
func bucketOf(pct *int) int {
	switch {
	case pct == nil:
		return 0
	case *pct <= wearCriticalPct:
		return 2
	case *pct <= wearLowPct:
		return 1
	}
	return 0
}

func bucketName(b int) string {
	if b >= 2 {
		return BucketCritical
	}
	return BucketLow
}

// wearOf picks out what a unit holds and wears that can wear out, with the
// item names beside the readings. The held slot is looked up in the inventory
// only when the unit knows which it holds. A piece with no reading has nil
// wear but may still have a name.
func wearOf(held int, heldKnown bool, inv []LiveItem, armour []LiveArmour) ([len(wearSlots)]*int, [len(wearSlots)]string) {
	var out [len(wearSlots)]*int
	var items [len(wearSlots)]string
	if heldKnown {
		for _, it := range inv {
			if it.Slot == held {
				out[heldWear], items[heldWear] = it.DurabilityPct, it.Name
				break
			}
		}
	}
	for _, a := range armour {
		for i, name := range wearSlots {
			if i != heldWear && name == a.Slot {
				out[i], items[i] = a.DurabilityPct, a.Name
			}
		}
	}
	return out, items
}

// observeWear returns a tool_worn event for each slot whose item got worse.
// Wear is remembered per item, not per slot: a swap to another tool, or to an
// empty hand, says nothing about the tool put away, so swapping back to it
// is not news. Only repairing that same item re-arms it. The first reading
// of a session sets a baseline, so a unit that starts with a worn tool is not
// announced. Items sharing a name share a memory, which at worst hides a
// second worn copy of a tool already announced.
func (t *track) observeWear(wear [len(wearSlots)]*int, items [len(wearSlots)]string, now time.Time) []Event {
	var out []Event
	for i, pct := range wear {
		if pct == nil {
			continue
		}
		b := bucketOf(pct)
		if b > t.wear[i][items[i]] {
			level := "warn"
			if b >= 2 {
				level = "crit"
			}
			out = append(out, Event{
				At: now, Kind: "tool_worn", Level: level,
				Text:  fmt.Sprintf("%s worn: %s", wearSlots[i], bucketName(b)),
				Attrs: map[string]any{"slot": wearSlots[i], "bucket": bucketName(b)},
			})
		}
		t.rememberWear(i, items[i], b)
	}
	return out
}

// rememberWear records an item's bucket, forgetting items that are fine so
// the map holds only worn ones.
func (t *track) rememberWear(slot int, item string, b int) {
	if b == 0 {
		delete(t.wear[slot], item)
		return
	}
	if t.wear[slot] == nil {
		t.wear[slot] = map[string]int{}
	}
	t.wear[slot][item] = b
}
