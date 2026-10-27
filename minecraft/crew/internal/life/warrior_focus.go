package life

import (
	"cmp"
	"math"
	"slices"

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
	// focusRange is how far from the anchor a threat is still focused, per
	// threat, so a family's tuning can reach further for what it must. The
	// squad agrees on a focus only while its units share a profile. nil is
	// the shipped range for every threat.
	focusRange func(belief.Entity) float64
	// healthOf knows a threat's health when something does, else nil: no
	// packet carries a mob's health today, so the id tie-break decides
	// until one does.
	healthOf func(uint64) (float32, bool)
	// player says whether a player may be targeted at all; nil is never.
	player func(belief.Entity) bool
}

// focusTarget picks the squad's shared target: the lowest-health threat
// within reach of the leader, ties broken by entity id, so three units
// looking at the same world swing at the same mob. A threat is a hostile
// mob, a provoked one that hurt the unit recently, never a player, and
// creepers stay out of it: no melee focus is worth a blast. A player is
// focused only when the query's player rule allows it, and then outranks
// every mob: the nearest one to the choosing unit wins. It is pure in what
// matters to the squad: the same entities, anchor and order pick the same
// target on every unit.
func focusTarget(ents *belief.Entities, q focusQuery) (belief.Entity, bool) {
	var (
		best     belief.Entity
		bestHp   = math.MaxFloat64
		bestID   uint64
		bestWant bool

		foe   belief.Entity
		foeD  = math.MaxFloat64
		foeOK bool
	)
	ents.Each(func(e belief.Entity) {
		if e.Player {
			if q.player == nil || !q.player(e) ||
				math.Abs(float64(e.Pos.Y()-q.anchor.Y())) > threatHeight ||
				horizontal(e.Pos, q.anchor) > q.rangeOf(e) ||
				(q.ignore[e.ID] > q.tick && !inMeleeReachAt(q.self, e.Pos)) {
				return
			}
			d := horizontal(e.Pos, q.self)
			if !foeOK || d < foeD || (d == foeD && e.ID < foe.ID) {
				foe, foeD, foeOK = e, d, true
			}
			return
		}
		if !q.mobThreat(e) {
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
	if foeOK {
		return foe, true
	}
	return best, best.ID != 0
}

// mobThreat says whether a mob is one the squad may focus: hostile, or
// provoked and recently the unit's attacker, never a creeper, within height
// and range of the anchor.
func (q focusQuery) mobThreat(e belief.Entity) bool {
	k := kindOf(e.Type)
	if k == other || k == flee || (k == provoked && !q.suspect(e.ID)) {
		return false
	}
	if math.Abs(float64(e.Pos.Y()-q.anchor.Y())) > threatHeight {
		return false
	}
	if horizontal(e.Pos, q.anchor) > q.rangeOf(e) {
		return false
	}
	// A mob this unit gave up on stays given up unless it is close enough to
	// hit anyway, which only this unit can judge.
	return !(q.ignore[e.ID] > q.tick && !q.suspect(e.ID) && !inMeleeReachAt(q.self, e.Pos))
}

// focusCandidates are the mobs the shared choice picks among, by id so every
// unit lists them in the same order.
func focusCandidates(ents *belief.Entities, q focusQuery) []belief.Entity {
	var out []belief.Entity
	ents.Each(func(e belief.Entity) {
		if !e.Player && q.mobThreat(e) {
			out = append(out, e)
		}
	})
	slices.SortFunc(out, func(a, b belief.Entity) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// rangeOf is how far from the anchor e is still focused.
func (q focusQuery) rangeOf(e belief.Entity) float64 {
	if q.focusRange == nil {
		return focusRange
	}
	return q.focusRange(e)
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
