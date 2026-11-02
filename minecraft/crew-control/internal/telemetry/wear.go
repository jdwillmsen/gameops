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

// bucketOf is 0 when the piece is fine or its wear unknown, else 1 for low
// and 2 for critical, so a worse bucket is a larger number.
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

// wearOf picks out what a unit holds and wears that can wear out. The held
// slot is looked up in the inventory only when the unit knows which it holds.
func wearOf(held int, heldKnown bool, inv []LiveItem, armour []LiveArmour) [len(wearSlots)]*int {
	var out [len(wearSlots)]*int
	if heldKnown {
		for _, it := range inv {
			if it.Slot == held {
				out[heldWear] = it.DurabilityPct
				break
			}
		}
	}
	for _, a := range armour {
		for i, name := range wearSlots {
			if i != heldWear && name == a.Slot {
				out[i] = a.DurabilityPct
			}
		}
	}
	return out
}

// observeWear returns a tool_worn event for each slot whose bucket got worse.
// A repaired or swapped piece that is fine again re-arms its slot, and the
// first reading of a session sets a baseline, so a unit that starts with a
// worn tool is not announced.
func (t *track) observeWear(wear [len(wearSlots)]*int, now time.Time) []Event {
	var out []Event
	for i, pct := range wear {
		b := bucketOf(pct)
		if b > t.wear[i] {
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
		t.wear[i] = b
	}
	return out
}
