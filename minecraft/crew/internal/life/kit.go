package life

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/sessionid"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/unitevent"
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
	// A chest that is merely early — its chunk has not arrived yet, or its
	// click answered with no window during the seconds around a join — is
	// retried this soon: chunks stream in over seconds, and a crowd of units
	// joining at once can wait longer than one visit's own answer window.
	kitRetryBackoff = 100
	// The short retry is allowed this many times in a row before the chest
	// is left to the failure back-off, so a chest that never answers cannot
	// keep the unit walking back.
	kitRetryTries = 6
	// A visit is a short walk and one chest; one that takes a minute is stuck.
	kitLimit = 1200
	// Farther than this from the chest a unit stays where it is: the walk
	// leaves the loaded chunks and outlasts the visit limit, so it could only
	// fail, and the task queue waits for the whole of a failing visit. A Kit
	// may set its own reach.
	kitReach = 96
	// No reach is set past this: at walking pace kitLimit ticks cover about
	// 250 blocks of straight line, and a route is longer than that.
	kitMaxReach = 160
	// The back-off after a failed visit doubles with each failure in a row, up
	// to this, so a unit that cannot reach its kit stops spending its time on it.
	kitMaxBackoff = 18000
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

// armourPieces are the four worn pieces, in the order of the armour slots.
var armourPieces = [4]string{"helmet", "chestplate", "leggings", "boots"}

// wornSlots name those slots as belief reports them.
var wornSlots = [4]string{"helmet", "chest", "legs", "boots"}

var armourMaterials = []string{"netherite", "diamond", "iron", "chainmail", "copper", "golden", "leather"}
var toolMaterials = []string{"netherite", "diamond", "iron", "stone", "copper", "golden", "wooden"}

// bestFirst lists a piece or tool in every material, best first.
func bestFirst(materials []string, kind string) []string {
	out := make([]string, len(materials))
	for i, m := range materials {
		out[i] = m + "_" + kind
	}
	return out
}

// gearNames is every armour piece and tool a gear visit may carry off the
// hotbar.
var gearNames = func() []string {
	var out []string
	for _, p := range armourPieces {
		out = append(out, bestFirst(armourMaterials, p)...)
	}
	out = append(out, bestFirst(toolMaterials, "pickaxe")...)
	return append(out, bestFirst(toolMaterials, "axe")...)
}()

// Kit is a chest a unit rearms from.
type Kit struct {
	Chest protocol.BlockPos
	// Food is how many food items a visit takes at most.
	Food  int
	World skill.ChestWorld
	// Reach is how far from the chest, in blocks, a unit still sets out for
	// it; zero is kitReach.
	Reach float64
	// Gear adds armour, worn on the spot, and a pickaxe and an axe to what a
	// visit takes. Left off, a unit without armour is not counted as missing
	// anything, so a chest that stocks none is not walked to for nothing.
	Gear bool
}

type kitState struct {
	needing   bool
	needSince uint64
	visit     skill.Skill
	startedAt uint64
	// blockedUntil is the tick before which no visit starts.
	blockedUntil uint64
	// retryTries counts the visits in a row that met a chest merely early:
	// a chunk still not loaded, or an open that waited out its window, so
	// the short retry stays bounded.
	retryTries int
	// fails counts the failed visits in a row, until one completes.
	fails int
	// asked is what the visit under way was started for.
	asked kitMissing
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

// kitMissing is everything a visit could bring.
type kitMissing struct {
	weapon, food bool
	// armour is true for each worn slot that is empty.
	armour       [4]bool
	pickaxe, axe bool
}

func (m kitMissing) gear() bool {
	return m.armour[0] || m.armour[1] || m.armour[2] || m.armour[3] || m.pickaxe || m.axe
}

func (m kitMissing) any() bool { return m.weapon || m.food || m.gear() }

// missing says what a visit would bring. Armour and tools count only when the
// kit carries them.
func (l *Life) missing(s belief.Snapshot) kitMissing {
	var m kitMissing
	m.weapon, m.food = lacks(s)
	if l.cfg.Kit == nil || !l.cfg.Kit.Gear {
		return m
	}
	for i, slot := range wornSlots {
		m.armour[i] = true
		for _, p := range s.Armour {
			if p.Slot == slot {
				m.armour[i] = false
			}
		}
		// A piece that is carried but was not put on (the server refused it)
		// is not fetched again, or each back-off would bring another.
		for _, it := range s.Inventory {
			if strings.HasSuffix(it.Name, "_"+armourPieces[i]) {
				m.armour[i] = false
			}
		}
	}
	m.pickaxe, m.axe = true, true
	for _, it := range s.Inventory {
		switch {
		case strings.HasSuffix(it.Name, "_pickaxe"):
			m.pickaxe = false
		case strings.HasSuffix(it.Name, "_axe"):
			m.axe = false
		}
	}
	return m
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
	m := l.missing(s)
	if !m.any() {
		k.needing = false
		return false
	}
	if !k.needing {
		k.needing, k.needSince = true, l.tick
	}
	// A weapon and food go on the hotbar; with no slot free there only the
	// gear can be brought.
	if !hotbarFree(s) {
		m.weapon, m.food = false, false
	}
	return !working && m.any() && l.tick >= k.blockedUntil && l.tick-k.needSince >= kitSettleTicks && l.kitInReach(s)
}

// kitInReach says whether the chest is near enough to walk to.
func (l *Life) kitInReach(s belief.Snapshot) bool {
	chest := l.cfg.Kit.Chest
	dx, dz := float64(s.Position[0])-float64(chest.X()), float64(s.Position[2])-float64(chest.Z())
	reach := l.cfg.Kit.Reach
	if reach <= 0 {
		reach = kitReach
	}
	reach = min(reach, kitMaxReach)
	return math.Hypot(dx, dz) <= reach
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
		m := l.missing(s)
		if !hotbarFree(s) {
			m.weapon, m.food = false, false
		}
		k.asked = m
		n := 0
		if m.food {
			n = l.cfg.Kit.Food
		}
		k.visit, k.startedAt = l.newKit(m, n), l.tick
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
		code := task.KitReasonCode(err)
		l.logEvent("kit_failed", map[string]any{"error": err.Error(), "reason_code": code, "session_id": sessionid.Current()})
		unitevent.Emit(unitevent.Event{Kind: "kit_failed", ReasonCode: code, Reason: err.Error()})
		early := errors.Is(err, skill.ErrChestChunkNotLoaded) || errors.Is(err, skill.ErrChestNotOpened)
		if early && k.retryTries < kitRetryTries {
			// A chest whose chunk is merely late, or whose open failed while
			// the unit was still settling in, is tried again within seconds,
			// not the three-minute back-off.
			k.retryTries++
			k.blockedUntil = l.tick + kitRetryBackoff
			return
		}
		if !early {
			// A different failure breaks the row of early misses.
			k.retryTries = 0
		}
		k.blockedUntil = l.tick + uint64(min(kitFailBackoff<<k.fails, kitMaxBackoff))
		k.fails = min(k.fails+1, 4)
		return
	}
	k.retryTries, k.fails = 0, 0
	m := l.missing(l.self())
	if !m.any() {
		l.logEvent("kit_restocked", nil)
		return
	}
	var missing []string
	if m.weapon {
		missing = append(missing, "weapon")
	}
	if m.food {
		missing = append(missing, "food")
	}
	if m.gear() {
		missing = append(missing, "gear")
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

func (l *Life) newKit(m kitMissing, food int) skill.Skill {
	if l.makeKit != nil {
		return l.makeKit(m.weapon, food)
	}
	opts := []skill.Option{skill.IntoHotbar()}
	if m.weapon {
		opts = append(opts, skill.OneOf(1, swords...))
	}
	if food > 0 {
		opts = append(opts, skill.OneOf(food, foodsBestFirst...))
	}
	if m.gear() {
		opts = append(opts, skill.Anywhere(gearNames...), skill.Wear())
		for i, lacking := range m.armour {
			if lacking {
				opts = append(opts, skill.OneOf(1, bestFirst(armourMaterials, armourPieces[i])...))
			}
		}
		if m.pickaxe {
			opts = append(opts, skill.OneOf(1, bestFirst(toolMaterials, "pickaxe")...))
		}
		if m.axe {
			opts = append(opts, skill.OneOf(1, bestFirst(toolMaterials, "axe")...))
		}
	}
	if l.log != nil {
		opts = append(opts, skill.WithLogger(l.log))
	}
	return skill.NewWithdrawChest(l.cfg.Kit.World, l.self, l.cfg.Kit.Chest, nil, opts...)
}
