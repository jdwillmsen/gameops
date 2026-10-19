package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// retreat walks away from a threat to roughly far blocks off, over ground
// the planner can cross. The goal is chosen again every second, because what
// it flees from moves. With nowhere to go the unit fights instead.
func (l *Life) retreat(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, far float64) error {
	if l.flee == nil || l.tick-l.fleeAt >= fleeEvery {
		goal, ok := l.fleeGoal(p, e, far)
		if !ok {
			// Cornered; the next Engaged sees the limit and fights.
			l.retreating = retreatLimit
			return l.stillFacing(a, p, e)
		}
		l.flee = skill.NewNavigate(l.world, l.self, goal, 1)
		l.fleeAt = l.tick
	}
	st, _ := l.flee.Step(ctx, a)
	if st == skill.Running {
		return nil
	}
	l.flee = nil
	l.fleeAt = l.tick
	return l.stillFacing(a, p, e)
}

// away is the unit vector from the threat to the unit.
func away(p act.Pose, e belief.Entity) mgl32.Vec2 {
	d := mgl32.Vec2{p.Feet.X() - e.Pos.X(), p.Feet.Z() - e.Pos.Z()}
	if d.Len() < 1e-3 {
		return mgl32.Vec2{0, 1}
	}
	return d.Normalize()
}

// fleeGoal picks the first reachable standing cell, away from the threat
// first and then to its sides, that ends up farther from it than the unit is.
func (l *Life) fleeGoal(p act.Pose, e belief.Entity, far float64) (protocol.BlockPos, bool) {
	dir := away(p, e)
	from := cellOf(p.Feet)
	now := horizontal(p.Feet, e.Pos)
	for _, turn := range [...]float64{0, 45, -45, 90, -90, 135, -135} {
		r := turn * math.Pi / 180
		c, s := float32(math.Cos(r)), float32(math.Sin(r))
		d := mgl32.Vec2{dir.X()*c - dir.Y()*s, dir.X()*s + dir.Y()*c}
		for _, dist := range [...]float64{far, far * 0.6} {
			x := int32(math.Floor(float64(p.Feet.X()) + float64(d.X())*dist))
			z := int32(math.Floor(float64(p.Feet.Z()) + float64(d.Y())*dist))
			goal, ok := cellAt(l.world, x, z, from.Y())
			if !ok {
				continue
			}
			gp := mgl32.Vec3{float32(goal.X()) + 0.5, float32(goal.Y()), float32(goal.Z()) + 0.5}
			if horizontal(gp, e.Pos) < now+1 {
				continue
			}
			plan, err := path.Find(l.world, from, goal, fleeNodes)
			if err != nil || !safePath(l.world, plan, from.Y()-maxDrop, from.Y()+1) {
				continue
			}
			return goal, true
		}
	}
	return protocol.BlockPos{}, false
}

// maxDrop is how far a retreat may step down: a fall of three blocks or less
// does no damage.
const maxDrop = 3
