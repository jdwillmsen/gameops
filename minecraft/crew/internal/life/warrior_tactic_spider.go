package life

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// spiderTacticAllowed is the safety gate both spider variants pass before
// taking a tick. A meal, a shared retreat, step-back or regroup, a standing
// hold and an arrow in flight are the role's own decisions: a family tactic must not talk
// the unit out of healing, obeying an order, or raising its shield.
func spiderTacticAllowed(f *Fight) bool {
	w := f.Life.warrior
	if w == nil || f.Target.ID == 0 || w.healWant || w.healing {
		return false
	}
	if f.Life.mode != modeFight {
		return false
	}
	switch w.order().Kind {
	case OrderHold, OrderRetreat, OrderRegroup:
		return false
	}
	return !f.Life.arrowIncoming(f.Pose)
}

// sweepClear says whether a straight walk of length blocks from feet along
// dir stays on floor the planner would accept. Tactics that step without the
// pathing layer use it so a one-block strip of water or lava between the
// unit and the far cell cannot be walked over unseen: the probes sit half a
// block apart, never wider than a cell, and each cell must hold a safe
// stance at the feet's level or one below.
func sweepClear(w path.Blocks, feet mgl32.Vec3, dir mgl32.Vec2, length float64) bool {
	y := int32(math.Floor(float64(feet.Y()) + 0.01))
	probe := func(d float64) bool {
		x := int32(math.Floor(float64(feet.X()) + float64(dir.X())*d))
		z := int32(math.Floor(float64(feet.Z()) + float64(dir.Y())*d))
		return safeStand(w, protocol.BlockPos{x, y, z}) || safeStand(w, protocol.BlockPos{x, y - 1, z})
	}
	for d := 0.5; d < length; d += 0.5 {
		if !probe(d) {
			return false
		}
	}
	return probe(length)
}
