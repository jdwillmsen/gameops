package life

import (
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const (
	// Natural regeneration runs only while food is at least this high.
	regenFood = 18
	// A rest ends once health is back to at least this.
	restUntilHealth = 18
	// A rest that has not healed the unit by now is given up, so a stuck unit
	// cannot stand still forever; 90 s heals about 20 points.
	restMaxTicks = 1800
	// After a rest that timed out or stalled, none starts again for a minute so
	// work can go on.
	restBackoff = 1200
	// A rest that a mob in view cut short may restart when it is gone.
	restThreatBackoff = 100
	// A golden apple's regeneration needs a few seconds to show, so a second
	// one is not eaten sooner than this after the first.
	gappleCooldown = 140
)

// Vitals are the thresholds of the health and food reflex, in health or food
// points out of 20. A zero value keeps the original behaviour: retreat at 6
// health, no resting, no golden apple, no check before a task starts.
type Vitals struct {
	// RetreatHealth is the health at or below which the reactive defence walks
	// away from a threat instead of fighting.
	RetreatHealth int
	// EmergencyHealth is the health at or below which a golden apple is eaten
	// whatever the hunger. It is the only time one is eaten by this reflex.
	EmergencyHealth int
	// RestBelow is the health under which a unit with no mob near stands still
	// to heal, pausing a running task. Zero never pauses a task for it.
	RestBelow int
	// TaskMinHealth is the health a unit needs before it starts a task; below
	// it, it rests first. Zero starts tasks at any health. With both zero a
	// unit never rests.
	TaskMinHealth int
	// TaskMinFood is the food a unit tops up to before it starts a task, when
	// it holds food to do it with. Zero adds no such meal.
	TaskMinFood int
}

// retreatAt is the health at or below which the unit retreats.
func (l *Life) retreatAt() float32 {
	if n := l.cfg.Vitals.RetreatHealth; n > 0 {
		return float32(n)
	}
	return lowHealth
}

// saturations are what each food adds to the saturation that keeps hunger from
// draining, which breaks a tie between two meals of the same size.
var saturations = map[string]float32{
	"cooked_beef": 12.8, "cooked_porkchop": 12.8, "pumpkin_pie": 4.8,
	"cooked_mutton": 9.6, "cooked_salmon": 9.6, "cooked_chicken": 7.2,
	"bread": 6, "baked_potato": 6, "cooked_cod": 6,
	"apple": 2.4, "carrot": 3.6, "melon_slice": 1.2, "cookie": 0.4, "sweet_berries": 0.4,
}

const goldenApple = "golden_apple"

// pickFood chooses the meal for a unit at the given hunger from its hotbar:
// the food that wastes the fewest points, then the one that keeps hunger up
// longest, then the larger. A steak is not spent on a hunger a carrot fills.
// Golden apples are never picked here.
func pickFood(inv []belief.Item, hunger float32) (belief.Item, bool) {
	var cands []belief.Item
	for _, it := range inv {
		// The server only lets a unit hold a hotbar stack.
		if it.Slot < hotbarSlots && foods[it.Name] > 0 {
			cands = append(cands, it)
		}
	}
	if len(cands) == 0 {
		return belief.Item{}, false
	}
	gap := max(0, fullHunger-int(hunger))
	slices.SortFunc(cands, func(a, b belief.Item) int {
		wa, wb := max(0, foods[a.Name]-gap), max(0, foods[b.Name]-gap)
		switch {
		case wa != wb:
			return wa - wb
		case saturations[a.Name] != saturations[b.Name]:
			if saturations[a.Name] > saturations[b.Name] {
				return -1
			}
			return 1
		case foods[a.Name] != foods[b.Name]:
			return foods[b.Name] - foods[a.Name]
		}
		return a.Slot - b.Slot
	})
	return cands[0], true
}

const fullHunger = 20

// emergency says the unit is hurt badly enough for a golden apple.
func (l *Life) emergency(s belief.Snapshot) bool {
	n := l.cfg.Vitals.EmergencyHealth
	return n > 0 && s.HealthKnown && s.Health <= float32(n)
}

// emergencyApple is the golden apple on the hotbar, if any.
func emergencyApple(inv []belief.Item) (belief.Item, bool) {
	for _, it := range inv {
		if it.Slot < hotbarSlots && it.Name == goldenApple {
			return it, true
		}
	}
	return belief.Item{}, false
}

// eatLimit is the hunger at or below which the unit eats. A hurt unit needs
// regeneration running, so it eats from 17; with no saturation left to burn
// the first point healed drops food below 18 at once, so it eats from 18. A
// unit about to start a task tops up to TaskMinFood.
func (l *Life) eatLimit(s belief.Snapshot, working bool) float32 {
	limit := float32(eatBelow)
	if s.HealthKnown && s.Health < fullHealth {
		limit = eatBelowHurt
		if s.SaturationKnown && s.Saturation <= 0 {
			limit = regenFood
		}
	}
	if !working {
		limit = max(limit, float32(l.cfg.Vitals.TaskMinFood))
	}
	return limit
}

type restState struct {
	active bool
	ticks  int
	// blockedUntil is the tick before which no rest starts.
	blockedUntil uint64
}

// restBelow is the health under which the unit rests. Before a task starts the
// bar is the higher of the two thresholds.
func (l *Life) restBelow(working bool) float32 {
	v := l.cfg.Vitals
	n := v.RestBelow
	if !working {
		n = max(n, v.TaskMinHealth)
	}
	return float32(n)
}

// wantsRest says the unit should stand still and heal this tick: hurt below
// the bar, fed enough for regeneration to run, and with no hostile mob near.
// A unit that cannot heal by standing (food under 18 and nothing to eat, which
// the meal check would have claimed) is left to its task, so a gate on health
// never strands one.
func (l *Life) wantsRest(s belief.Snapshot, working bool) bool {
	if l.rest.active {
		// resting says when it is over.
		return true
	}
	// Without a position the mobs around cannot be told from far ones.
	if l.restBelow(false) == 0 || s.Creative || !s.HealthKnown || !s.PositionKnown || l.tick < l.rest.blockedUntil {
		return false
	}
	if s.Health >= l.restBelow(working) {
		return false
	}
	if s.HungerKnown && s.Hunger < regenFood {
		return false
	}
	return !l.hostileNear(s)
}

// hostileNear reports a hostile mob within sight of the unit.
func (l *Life) hostileNear(s belief.Snapshot) bool {
	return l.Nearby(s.Position).Hostiles > 0
}

// resting counts a tick of a rest and ends it when it has done its work or
// cannot; the caller sends the tick's still input.
func (l *Life) resting(s belief.Snapshot, working bool) {
	r := &l.rest
	if !r.active {
		r.active, r.ticks = true, 0
		l.logEvent("rest", map[string]any{"health": s.Health, "hunger": s.Hunger, "working": working})
	}
	r.ticks++
	reason := ""
	switch {
	case s.Health >= float32(max(restUntilHealth, int(l.restBelow(working)))):
		reason = "healed"
	case s.HungerKnown && s.Hunger < regenFood:
		reason = "hungry"
	case l.hostileNear(s):
		reason = "threat"
	case r.ticks >= restMaxTicks:
		reason = "timeout"
	}
	if reason == "" {
		return
	}
	r.active = false
	switch reason {
	case "threat":
		r.blockedUntil = l.tick + restThreatBackoff
	case "hungry", "timeout":
		r.blockedUntil = l.tick + restBackoff
	}
	l.logEvent("rest_done", map[string]any{"reason": reason, "ticks": r.ticks, "health": s.Health})
}
