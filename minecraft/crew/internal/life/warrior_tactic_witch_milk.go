package life

import (
	"context"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// witch/milk cures the witch's potions while the fight is on: a unit under
// poison, fatal poison or slowness — the effects a witch's thrown potions
// carry — drinks a milk bucket, rather than fight through them. It borrows
// the heal meal's machinery, which already holds the eaten-hold discipline,
// the guard from an enemy closing in and the report on a bite the server
// never fights over, so the cure obeys exactly the rules any meal obeys.
// Weakness is left out on purpose: it dulls the unit's own harm but does not
// wear the fight's chances down the way poison and slowness do, and a bucket
// spent on it is one lost to what hurts next. Honey stays out too: its drink
// runs longer than a meal's clock holds, which leaves the item use hanging
// and the meal reported as never landing.

// witchCureEffects are the effects a witch's thrown potion leaves, by the
// names the effect packets carry.
var witchCureEffects = map[string]bool{
	"slowness":     true,
	"poison":       true,
	"fatal_poison": true,
}

// witchCureItem is milk, the one cure whose drink fits a meal's clock.
const witchCureItem = "milk_bucket"

type witchMilk struct{}

func init() { RegisterTactic(&witchMilk{}) }

func (*witchMilk) Name() string           { return "witch/milk" }
func (*witchMilk) Family() tactics.Family { return tactics.FamilyWitch }

func (*witchMilk) Engage(_ context.Context, f *Fight) (bool, error) {
	l := f.Life
	// The modes the shared decisions have already taken outrank the cure,
	// and a meal the back-off clock has not pardoned yet is not retried:
	// that clock is there because the previous meal never landed.
	if l.mode != modeFight || l.tick < l.eat.blockedUntil {
		return false, nil
	}
	if !witchSplashed(f.Snapshot) {
		return false, nil
	}
	cup, ok := witchCureAt(f.Snapshot)
	// An enemy inside the meal's own clearance walks a cure off a tick
	// after it starts; the fight, not the drink, is the answer.
	if !ok || l.enemyNear(f.Pose.Feet, f.Params.HealSafe) {
		return false, nil
	}
	return true, l.startCure(f.Actuator, cup, f.Snapshot)
}

// witchSplashed says whether the unit is under an effect a witch's thrown
// potion carries.
func witchSplashed(s belief.Snapshot) bool {
	for _, e := range s.Effects {
		if witchCureEffects[e.Name] {
			return true
		}
	}
	return false
}

// witchCureAt is the cure on the hotbar, if any. Only the hotbar counts: the
// server drops a meal not started from a seen stack.
func witchCureAt(s belief.Snapshot) (belief.Item, bool) {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && it.Name == witchCureItem {
			return it, true
		}
	}
	return belief.Item{}, false
}

// startCure seeds the meal with the cure and steps it, the way startHeal
// seeds one with food. The eat state machine sends exactly one input per
// tick and lets the server's own timing call each phase, so the cure is
// neither selected, held nor released by hand here.
func (l *Life) startCure(a skill.Actuator, cup belief.Item, s belief.Snapshot) error {
	l.lowerShield(a)
	l.cancelDraw(a)
	l.warrior.healing = true
	l.eat = eatState{active: true, slot: cup.Slot, food: cup, startHunger: s.Hunger, startCount: cup.Count, blockedUntil: l.eat.blockedUntil}
	if s.HeldKnown && s.HeldSlot == cup.Slot {
		l.eat.phase = eatHold
	}
	return l.warriorHealing(a, s)
}
