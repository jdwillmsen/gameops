package life

import (
	"context"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// perchEpsilon is how far a boss may drift between packets and still be
	// standing where it was.
	perchEpsilon = 0.05
	// perchStill is how long a boss must stand still before it counts as
	// perched: a second, against a flying dragon that never holds a position
	// for half that, and it costs a fortieth of a 40 s perch.
	perchStill = 20
	// perchStrike is how far from a perched boss's origin, measured from the
	// eye, a swing is taken. The server lands a hit out to about 6.4 blocks;
	// the margin covers the unit's pose being a tick behind its body.
	perchStrike = 6.0
	// perchStand is how near the perch's cell an approach walks to.
	perchStand = 3.0
)

// perched says the boss has stood still long enough to be on its perch.
func (b *bossState) perched(tick uint64) bool {
	return b.prof.perchOnly && tick-b.stillSince >= perchStill
}

// eyeToOrigin is the distance the server measures a hit by: from the unit's
// eye to the boss's origin.
func eyeToOrigin(p act.Pose, e belief.Entity) float64 {
	return float64(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}).Sub(e.Pos).Len())
}

// perchFight is the fight with a boss that can be hurt only while it
// perches: every warrior, whatever its role, swings at it from within reach
// and walks up to it while it sits, and holds its ground facing it
// otherwise. A bow does nothing to it, and chasing it in flight would only
// put the unit under its charge. A hold order still keeps the unit where it
// stands, swinging only at a perch already in reach.
func (l *Life) perchFight(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	b := &l.warrior.boss
	l.cancelDraw(a)
	l.lowerShield(a)
	if !b.perched(l.tick) {
		b.walk = nil
		return l.stillFacing(a, p, e)
	}
	if eyeToOrigin(p, e) <= perchStrike {
		b.walk = nil
		l.chase = nil
		return l.strike(a, p, e, s)
	}
	if l.warrior.hold {
		// The order is to keep position: a perch out of reach is not
		// walked to.
		b.walk = nil
		return l.stillFacing(a, p, e)
	}
	return l.perchApproach(ctx, a, p, e)
}

// perchApproach walks up to a perched boss. A walk that finds no way is not
// a reason to give the boss up, as a chase does: the perch is the one
// window, and the next one may be reachable.
func (l *Life) perchApproach(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity) error {
	b := &l.warrior.boss
	if l.tick < b.walkRetry {
		return l.stillFacing(a, p, e)
	}
	cell := cellOf(e.Pos)
	if b.walk == nil || (l.tick-b.walkAt >= chaseEvery && b.walkGoal != cell) {
		goal, ok := l.standNear(cell)
		if !ok {
			b.walkRetry = l.tick + regroupRetry
			return l.stillFacing(a, p, e)
		}
		b.walk = skill.NewNavigate(l.world, l.self, goal, perchStand)
		b.walkGoal, b.walkAt = cell, l.tick
	}
	switch st, _ := b.walk.Step(ctx, a); st {
	case skill.Running:
		return nil
	case skill.Failed:
		b.walkRetry = l.tick + regroupRetry
	}
	b.walk = nil
	return l.stillFacing(a, p, e)
}
