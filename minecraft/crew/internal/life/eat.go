package life

import (
	"context"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// Natural regeneration needs 18 of 20 food, so a hurt unit eats from 17
	// down: a point or two of waste buys the healing. An unhurt one waits until
	// a whole bread (5 points) fits with room to spare.
	eatBelowHurt = 17
	eatBelow     = 14
	fullHealth   = 20

	// Eating takes 32 ticks; the hold carries a little margin for the
	// server's own clock.
	eatTicks = 34
	// After the release the server may still be applying the meal.
	eatSettleTicks = 20
	// A meal that never lands (wrong item state, a server that wants
	// something else) is not retried for a minute.
	eatFailBackoff = 1200
	// A meal cut short by a fight may restart once the fight is over.
	eatAbortBackoff = 100
)

// foods are what a unit will eat and the food points each restores. Anything
// that poisons, teleports or wastes a rare item is left out.
var foods = map[string]int{
	"cooked_beef": 8, "cooked_porkchop": 8, "pumpkin_pie": 8,
	"cooked_mutton": 6, "cooked_salmon": 6, "cooked_chicken": 6,
	"bread": 5, "baked_potato": 5, "cooked_cod": 5,
	"apple": 4, "carrot": 3, "melon_slice": 2, "cookie": 2, "sweet_berries": 2,
}

// isFood reports a stack a unit may eat.
func isFood(name string) bool { _, ok := foods[name]; return ok }

type eatPhase uint8

const (
	eatSelect eatPhase = iota
	eatHold
	eatSettle
)

type eatState struct {
	active bool
	phase  eatPhase
	slot   int
	food   belief.Item
	// Hunger and stack size at the start, which is how the end of the meal
	// shows: the server announces neither.
	startHunger float32
	startCount  int
	ticks       int
	// blockedUntil is the tick before which no meal starts.
	blockedUntil uint64
}

// Tending says whether the unit has something to see to besides its task, and
// the runner then hands it the tick through Tend. working says a task is
// running. It is only asked when the unit is not engaged in a fight.
func (l *Life) Tending(s belief.Snapshot, working bool) bool {
	if l.eat.active {
		return true
	}
	// A visit under way has a chest open, so a meal waits for it.
	if l.kit.visit != nil {
		return true
	}
	if _, ok := l.wantsMeal(s); ok {
		return true
	}
	return l.kitWants(s, working)
}

// Tend sends exactly one input for a tick Tending claimed.
func (l *Life) Tend(ctx context.Context, a skill.Actuator, s belief.Snapshot) error {
	l.idle = idleState{}
	if !l.eat.active && l.kit.visit == nil {
		if food, ok := l.wantsMeal(s); ok {
			l.eat = eatState{active: true, slot: food.Slot, food: food, startHunger: s.Hunger, startCount: food.Count, blockedUntil: l.eat.blockedUntil}
			if s.HeldKnown && s.HeldSlot == food.Slot {
				l.eat.phase = eatHold
			}
		}
	}
	if l.eat.active {
		return l.eating(a, s)
	}
	return l.tendKit(ctx, a, s)
}

// wantsMeal picks the food to eat now, if the unit is hungry enough.
func (l *Life) wantsMeal(s belief.Snapshot) (belief.Item, bool) {
	if !l.cfg.Eat || !s.HungerKnown || l.tick < l.eat.blockedUntil {
		return belief.Item{}, false
	}
	limit := float32(eatBelow)
	if s.HealthKnown && s.Health < fullHealth {
		limit = eatBelowHurt
	}
	if s.Hunger > limit {
		return belief.Item{}, false
	}
	var best belief.Item
	bestPts := 0
	for _, it := range s.Inventory {
		// The server only lets a unit hold a hotbar stack.
		if it.Slot >= hotbarSlots {
			continue
		}
		if pts := foods[it.Name]; pts > bestPts {
			best, bestPts = it, pts
		}
	}
	return best, bestPts > 0
}

// eating advances the meal by one tick.
func (l *Life) eating(a skill.Actuator, s belief.Snapshot) error {
	e := &l.eat
	p := a.Pose()
	still := act.Input{Yaw: p.Yaw, Pitch: p.Pitch}
	cur := stackAt(s, e.slot, e.food.Name)

	switch e.phase {
	case eatSelect:
		if err := a.SelectHotbar(e.slot, e.food); err != nil {
			return l.abandonMeal(a, err, still)
		}
		e.phase = eatHold
		return a.Tick(still)
	case eatHold:
		if e.ticks == 0 {
			if err := a.UseItem(e.slot, cur); err != nil {
				return l.abandonMeal(a, err, still)
			}
			still.StartUsing = true
		}
		e.ticks++
		if l.mealDone(s, cur) {
			err := a.ReleaseItem(e.slot, cur)
			l.endMeal(true, s)
			if terr := a.Tick(still); err == nil {
				err = terr
			}
			return err
		}
		if e.ticks >= eatTicks {
			e.phase, e.ticks = eatSettle, 0
			if err := a.ReleaseItem(e.slot, cur); err != nil {
				return err
			}
		}
		return a.Tick(still)
	}
	e.ticks++
	if l.mealDone(s, cur) {
		l.endMeal(true, s)
	} else if e.ticks >= eatSettleTicks {
		l.endMeal(false, s)
	}
	return a.Tick(still)
}

func (l *Life) mealDone(s belief.Snapshot, cur belief.Item) bool {
	return cur.Count < l.eat.startCount || s.Hunger > l.eat.startHunger
}

func (l *Life) endMeal(ok bool, s belief.Snapshot) {
	e := &l.eat
	f := map[string]any{"food": e.food.Name, "hunger": s.Hunger}
	if ok {
		l.logEvent("eat", f)
		e.blockedUntil = l.tick + attackEvery
	} else {
		l.logEvent("eat_failed", f)
		e.blockedUntil = l.tick + eatFailBackoff
	}
	e.active = false
}

// abandonMeal drops a meal that cannot be sent, as an item the server's
// table no longer lists.
func (l *Life) abandonMeal(a skill.Actuator, err error, in act.Input) error {
	l.eat.active = false
	l.eat.blockedUntil = l.tick + eatFailBackoff
	l.logEvent("eat_failed", map[string]any{"food": l.eat.food.Name, "error": err.Error()})
	return a.Tick(in)
}

// stopEating lets go of a meal a fight interrupts. It sends no input, which
// the fight's own tick does.
func (l *Life) stopEating(a skill.Actuator, s belief.Snapshot) {
	e := &l.eat
	if !e.active {
		return
	}
	if e.phase == eatHold && e.ticks > 0 {
		_ = a.ReleaseItem(e.slot, stackAt(s, e.slot, e.food.Name))
	}
	e.active = false
	e.blockedUntil = l.tick + eatAbortBackoff
}

// stackAt is the stack in a slot when it still holds the item, else the item
// as it was.
func stackAt(s belief.Snapshot, slot int, name string) belief.Item {
	for _, it := range s.Inventory {
		if it.Slot == slot && it.Name == name {
			return it
		}
	}
	return belief.Item{Slot: slot, Name: name}
}
