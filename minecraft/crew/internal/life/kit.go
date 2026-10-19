package life

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// A unit that has just spawned has no inventory yet, and one that just
	// died is about to be given its respawn; waiting also keeps a stack that is
	// momentarily out of belief from sending the unit to the chest.
	kitSettleTicks = 100
	// A chest that could not make the unit whole is left alone for three
	// minutes, so an empty kit does not keep the unit walking back and forth.
	kitEmptyBackoff = 3600
	kitFailBackoff  = 3600
	// A visit that a fight cut short is tried again soon.
	kitAbortBackoff = 200
	// A visit is a short walk and one chest; one that takes a minute is stuck.
	kitLimit = 1200
)

// swords is the order a unit prefers them in.
var swords = []string{"netherite_sword", "diamond_sword", "iron_sword", "stone_sword", "copper_sword", "golden_sword", "wooden_sword"}

// foodsBestFirst is every food a unit eats, richest first, so a visit takes
// steak before bread.
var foodsBestFirst = func() []string {
	names := make([]string, 0, len(foods))
	for n := range foods {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int {
		if d := foods[b] - foods[a]; d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	return names
}()

// Kit is a chest a unit rearms from.
type Kit struct {
	Chest protocol.BlockPos
	// Food is how many food items a visit takes at most.
	Food  int
	World skill.ChestWorld
}

type kitState struct {
	needing   bool
	needSince uint64
	visit     skill.Skill
	startedAt uint64
	// blockedUntil is the tick before which no visit starts.
	blockedUntil uint64
}

// lacks says what the unit is missing that the kit holds: a sword or axe, and
// food, both on the hotbar because that is all a unit can hold.
func lacks(s belief.Snapshot) (weapon, food bool) {
	weapon, food = true, true
	for _, it := range s.Inventory {
		if it.Slot >= hotbarSlots {
			continue
		}
		if weaponScore(it) >= axeBase {
			weapon = false
		}
		if isFood(it.Name) {
			food = false
		}
	}
	return weapon, food
}

// hotbarFree says whether a hotbar slot is empty. Whatever a unit lacks has no
// stack on the hotbar to merge onto, so a visit with no empty slot could only
// fail to bring it.
func hotbarFree(s belief.Snapshot) bool {
	used := 0
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots {
			used++
		}
	}
	return used < hotbarSlots
}

// kitWants says whether the unit should go to the kit now. working says a
// task is running, which a visit never interrupts.
func (l *Life) kitWants(s belief.Snapshot, working bool) bool {
	k := &l.kit
	if l.cfg.Kit == nil {
		return false
	}
	if k.visit != nil {
		return true
	}
	weapon, food := lacks(s)
	if !weapon && !food {
		k.needing = false
		return false
	}
	if !k.needing {
		k.needing, k.needSince = true, l.tick
	}
	return !working && hotbarFree(s) && l.tick >= k.blockedUntil && l.tick-k.needSince >= kitSettleTicks
}

// tendKit steps the visit to the chest by one tick, starting it first if none is
// under way. The tick always carries one input: a withdraw that ends closes
// the chest without sending one.
func (l *Life) tendKit(ctx context.Context, a skill.Actuator, s belief.Snapshot) error {
	before := a.Ticks()
	st, err := l.advanceKit(ctx, a, s)
	if st == skill.Running {
		return nil
	}
	l.endKit(err)
	if a.Ticks() == before {
		return l.still(a)
	}
	return nil
}

func (l *Life) advanceKit(ctx context.Context, a skill.Actuator, s belief.Snapshot) (skill.Status, error) {
	k := &l.kit
	if k.visit == nil {
		weapon, food := lacks(s)
		n := 0
		if food {
			n = l.cfg.Kit.Food
		}
		k.visit, k.startedAt = l.newKit(weapon, n), l.tick
		if err := k.visit.Ready(s); err != nil {
			return skill.Failed, err
		}
	}
	if l.tick-k.startedAt > kitLimit {
		if st, ok := k.visit.(skill.Stopper); ok {
			st.Stop(a)
		}
		return skill.Failed, errors.New("visit took too long")
	}
	st, err := k.visit.Step(ctx, a)
	if st == skill.Failed && err == nil {
		err = errors.New("visit failed")
	}
	return st, err
}

// endKit closes a visit and judges it on what the unit holds now. The skill
// waited for its withdrawals to show in belief before it finished, so a fresh
// snapshot already counts them.
func (l *Life) endKit(err error) {
	k := &l.kit
	full, _ := k.visit.(interface{ Full() bool })
	k.visit = nil
	if err != nil {
		l.logEvent("kit_failed", map[string]any{"error": err.Error()})
		k.blockedUntil = l.tick + kitFailBackoff
		return
	}
	weapon, food := lacks(l.self())
	if !weapon && !food {
		l.logEvent("kit_restocked", nil)
		return
	}
	var missing []string
	if weapon {
		missing = append(missing, "weapon")
	}
	if food {
		missing = append(missing, "food")
	}
	event := "kit_empty"
	if full != nil && full.Full() && !hotbarFree(l.self()) {
		event = "kit_no_room"
	}
	l.logEvent(event, map[string]any{"missing": missing})
	k.blockedUntil = l.tick + kitEmptyBackoff
}

// stopKit abandons a visit a fight interrupts, closing the chest. It sends no
// input, which the fight's own tick does.
func (l *Life) stopKit(a skill.Actuator) {
	k := &l.kit
	if k.visit == nil {
		return
	}
	if st, ok := k.visit.(skill.Stopper); ok {
		st.Stop(a)
	}
	k.visit = nil
	k.blockedUntil = l.tick + kitAbortBackoff
}

func (l *Life) newKit(weapon bool, food int) skill.Skill {
	if l.makeKit != nil {
		return l.makeKit(weapon, food)
	}
	opts := []skill.Option{skill.IntoHotbar()}
	if weapon {
		opts = append(opts, skill.OneOf(1, swords...))
	}
	if food > 0 {
		opts = append(opts, skill.OneOf(food, foodsBestFirst...))
	}
	if l.log != nil {
		opts = append(opts, skill.WithLogger(l.log))
	}
	return skill.NewWithdrawChest(l.cfg.Kit.World, l.self, l.cfg.Kit.Chest, nil, opts...)
}
