package life

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// focusQuery carries what the shared target choice is a function of. The
// anchor, the tick, the ignore map and the attacker all come from state
// every unit in the squad sees the same way, so the same query picks the
// same target on every unit without a message passing between them.
type focusQuery struct {
	// anchor is the leader's feet: a threat is focused within focusRange of
	// the leader, not of whichever unit is choosing.
	anchor mgl32.Vec3
	// self is the choosing unit's own feet, for the one unit-local rule: a
	// mob already in this unit's melee reach outranks this unit's own
	// give-up on it.
	self mgl32.Vec3
	tick uint64
	// ignore is this unit's own give-up map, so a target it cannot reach is
	// not chosen again while the entry stands.
	ignore map[uint64]uint64
	// attacker and attackerUntil are the suspect window: a provoked mob
	// counts as a threat only while what hurt the unit recently is it.
	attacker, attackerUntil uint64
	// wantType is the focus order's preferred entity type, "" for none.
	wantType string
	// healthOf knows a threat's health when something does, else nil: no
	// packet carries a mob's health today, so the id tie-break decides
	// until one does.
	healthOf func(uint64) (float32, bool)
}

// focusTarget picks the squad's shared target: the lowest-health threat
// within reach of the leader, ties broken by entity id, so three units
// looking at the same world swing at the same mob. A threat is a hostile
// mob, a provoked one that hurt the unit recently, never a player, and
// creepers stay out of it: no melee focus is worth a blast. It is pure in
// what matters to the squad: the same entities, anchor and order pick the
// same target on every unit.
func focusTarget(ents *belief.Entities, q focusQuery) (belief.Entity, bool) {
	var (
		best     belief.Entity
		bestHp   = math.MaxFloat64
		bestID   uint64
		bestWant bool
	)
	ents.Each(func(e belief.Entity) {
		if e.Player {
			return
		}
		k := kindOf(e.Type)
		if k == other || k == flee || (k == provoked && !q.suspect(e.ID)) {
			return
		}
		if math.Abs(float64(e.Pos.Y()-q.anchor.Y())) > threatHeight {
			return
		}
		if horizontal(e.Pos, q.anchor) > focusRange {
			return
		}
		// A mob this unit gave up on stays given up unless it is close
		// enough to hit anyway, which only this unit can judge.
		if q.ignore[e.ID] > q.tick && !q.suspect(e.ID) && !inMeleeReachAt(q.self, e.Pos) {
			return
		}
		hp := float64(fullHealth)
		if q.healthOf != nil {
			if h, ok := q.healthOf(e.ID); ok {
				hp = float64(h)
			}
		}
		want := q.wantType != "" && name(e.Type) == q.wantType
		better := want && !bestWant
		if !better && want == bestWant {
			better = hp < bestHp || (hp == bestHp && e.ID < bestID)
		}
		if best.ID == 0 || better {
			best, bestHp, bestID, bestWant = e, hp, e.ID, want
		}
	})
	return best, best.ID != 0
}

// suspect says whether id is what hurt the unit recently.
func (q focusQuery) suspect(id uint64) bool {
	return id == q.attacker && q.tick < q.attackerUntil
}

// inMeleeReachAt is inMeleeReach for a position instead of a whole pose.
func inMeleeReachAt(feet mgl32.Vec3, pos mgl32.Vec3) bool {
	return horizontal(feet, pos) <= meleeReach &&
		math.Abs(float64(pos.Y()-feet.Y())) <= reachHeight
}
