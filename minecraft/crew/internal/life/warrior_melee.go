package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// shieldItemName is what the kit hands a warrior to block with.
const shieldItemName = "shield"

// warriorMelee carries a melee fight: close in — the Striker around the
// flank — circle the target between swings, shield what is coming, and
// strike on the cadence, the Striker from mid-fall for the critical hit.
func (l *Life) warriorMelee(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	w := l.warrior

	if !inMeleeReach(p, e) {
		w.critUntil = 0
		if w.hold {
			// The order is to keep position: face what stands off, and
			// let it come.
			return l.stillFacing(a, p, e)
		}
		l.lowerShield(a)
		return l.warriorApproach(ctx, a, p, e)
	}

	if l.tick-l.lastAttack >= attackEvery {
		l.lowerShield(a)
		if w.role == RoleStriker && p.OnGround && l.tick >= w.critUntil && !l.arrowIncoming(p) {
			// Jump straight up: the fall gives the critical hit, and
			// staying over the same cell gives the landing.
			w.critUntil = l.tick + critWindow
			yaw, pitch := lookAt(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}), e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}))
			return a.Tick(act.Input{Yaw: yaw, Pitch: pitch, Jump: true})
		}
		// A jump under way holds the swing for the fall; anything else,
		// including a jump that has run out, swings now.
		if p.OnGround || p.Rise() < 0 || l.tick >= w.critUntil {
			w.struck = e.ID
			return l.strike(a, p, e, s)
		}
		yaw, pitch := lookAt(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}), e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}))
		return a.Tick(act.Input{Yaw: yaw, Pitch: pitch})
	}

	if l.shieldUp(p, s) {
		return l.raiseShield(a, p, s)
	}
	// A raised shield walks at a crawl: circling wants the hand free.
	l.lowerShield(a)
	return l.circle(a, p, e)
}

// shieldUp says whether this tick is one to hide behind the shield. An arrow
// closing on the unit always is. The Vanguard's standing job is broader: any
// shooter in the fight, any mob at arm's length. The others read a melee hit
// as imminent only once one has landed, because Bedrock shows no windup to
// read it from — and circling is the dodge that keeps the first one away.
func (l *Life) shieldUp(p act.Pose, s belief.Snapshot) bool {
	if _, ok := shieldAt(s); !ok {
		return false
	}
	if l.arrowIncoming(p) {
		return true
	}
	atArm := l.enemyNear(p.Feet, meleeReach)
	if l.warrior.role == RoleVanguard {
		return l.shooterNear(p) || atArm
	}
	return atArm && l.tick < l.warrior.hurtUntil
}

// shooterNear says whether a shooter stands within bow range on the level,
// which for a Vanguard is a raised shield whatever it aims at.
func (l *Life) shooterNear(p act.Pose) bool {
	found := false
	l.ents.Each(func(e belief.Entity) {
		if found || e.Player || !shooters[name(e.Type)] {
			return
		}
		if math.Abs(float64(e.Pos.Y()-p.Feet.Y())) > threatHeight {
			return
		}
		if horizontal(p.Feet, e.Pos) <= shooterRange {
			found = true
		}
	})
	return found
}

// raiseShield holds the shield out and this tick's facing with it. The use
// starts once and stands until released, as a meal does.
func (l *Life) raiseShield(a skill.Actuator, p act.Pose, s belief.Snapshot) error {
	w := l.warrior
	in := act.Input{Yaw: p.Yaw, Pitch: p.Pitch}
	if !w.shield {
		shield, ok := shieldAt(s)
		if !ok {
			return l.still(a)
		}
		if err := a.SelectHotbar(shield.Slot, shield); err != nil {
			return err
		}
		if err := a.UseItem(shield.Slot, shield); err != nil {
			return err
		}
		w.shield, w.shieldItem = true, shield
		in.StartUsing = true
	}
	return a.Tick(in)
}

// lowerShield lets the shield down, as a swing, a sprint or a meal needs the
// hand. It sends no input; the caller's own tick does.
func (l *Life) lowerShield(a skill.Actuator) {
	w := l.warrior
	if !w.shield {
		return
	}
	_ = a.ReleaseItem(w.shieldItem.Slot, w.shieldItem)
	w.shield = false
}

// shieldAt is the shield on the hotbar, when the kit gave one.
func shieldAt(s belief.Snapshot) (belief.Item, bool) {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && it.Name == shieldItemName {
			return it, true
		}
	}
	return belief.Item{}, false
}

// circle walks the unit around its target: the melee range held, the
// shooter's line on the leader left, the leader not crowded, and the world's
// blocks answered by turning the other way. The Vanguard plants instead —
// holding the front is its circling.
func (l *Life) circle(a skill.Actuator, p act.Pose, e belief.Entity) error {
	w := l.warrior
	if w.role == RoleVanguard {
		return l.stillFacing(a, p, e)
	}
	d := horizontal(p.Feet, e.Pos)
	if d < orbitMin {
		// Too close: back a step off it, facing it.
		yaw, pitch := lookAt(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}), e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}))
		return a.Tick(act.Input{Yaw: yaw, Pitch: pitch, Forward: -1})
	}
	anchor, leaderOK := w.leader(p)
	to := mgl32.Vec2{e.Pos.X() - p.Feet.X(), e.Pos.Z() - p.Feet.Z()}
	if to.Len() < 1e-3 {
		return l.stillFacing(a, p, e)
	}
	to = to.Normalize()

	// The tangent ahead of either orbit direction, scored: leaving a
	// shooter's line first, clearing the leader second.
	bestYaw, bestScore := 0.0, math.Inf(-1)
	for _, dir := range [...]int8{1, -1} {
		t := mgl32.Vec2{-to.Y() * float32(dir), to.X() * float32(dir)}
		next := p.Feet.Add(mgl32.Vec3{t.X(), 0, t.Y()})
		score := 0.0
		if leaderOK {
			if _, dist, ok := l.shooterLineAt(next, anchor); ok && dist <= lineWidth {
				score -= 2
			}
			if h := horizontal(next, anchor); h < 1.2 {
				score--
			}
		}
		if dir == w.orbit {
			score += 0.5
		}
		if score > bestScore {
			bestScore, bestYaw = score, yawOf(t)
		}
	}
	if err := a.Tick(act.Input{Yaw: float32(bestYaw), Forward: 1}); err != nil {
		return err
	}
	// A circling step that moved nowhere is a wall or a body: turn the
	// other way.
	if (mgl32.Vec2{p.Feet.X() - w.circX, p.Feet.Z() - w.circZ}).Len() < 0.05 {
		if w.circStuck++; w.circStuck >= 3 {
			w.orbit, w.circStuck = -w.orbit, 0
		}
	} else {
		w.circStuck = 0
	}
	w.circX, w.circZ = p.Feet.X(), p.Feet.Z()
	return nil
}

// yawOf is the heading of a direction on the floor.
func yawOf(dir mgl32.Vec2) float64 {
	return math.Atan2(float64(-dir.X()), float64(dir.Y())) * 180 / math.Pi
}

// shooterLineAt is how far a point stands from the nearest shooter's line on
// the anchor, and whether any shooter has one. The anchor is the shooter's
// mark: what stands on the line is in the way of its arrows.
func (l *Life) shooterLineAt(at mgl32.Vec3, anchor mgl32.Vec3) (belief.Entity, float64, bool) {
	var (
		best  belief.Entity
		bestD = math.MaxFloat64
	)
	l.ents.Each(func(e belief.Entity) {
		if e.Player || !shooters[name(e.Type)] {
			return
		}
		if math.Abs(float64(e.Pos.Y()-anchor.Y())) > threatHeight {
			return
		}
		if horizontal(e.Pos, anchor) > shooterRange {
			return
		}
		if d := lineDistance(at, e.Pos, anchor); d < bestD {
			best, bestD = e, d
		}
	})
	return best, bestD, best.ID != 0
}

// lineDistance is how far a point stands from the segment from a to b.
func lineDistance(pt, a, b mgl32.Vec3) float64 {
	ax, az := float64(pt.X()-a.X()), float64(pt.Z()-a.Z())
	bx, bz := float64(b.X()-a.X()), float64(b.Z()-a.Z())
	if bx == 0 && bz == 0 {
		return math.Hypot(ax, az)
	}
	t := (ax*bx + az*bz) / (bx*bx + bz*bz)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(ax-t*bx, az-t*bz)
}

// warriorApproach closes in on the target: the Striker by a flank cell wide
// of the leader's line on it, the others head on. The plan is redone only
// when the target moves to another cell, as the reactive chase is.
func (l *Life) warriorApproach(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity) error {
	cell := cellOf(e.Pos)
	if l.chase == nil || (l.tick-l.chaseAt >= chaseEvery && l.chaseGoal != cell) {
		goal, ok := l.flankCell(p, e)
		within := chaseWithin
		if !ok {
			goal, ok = l.standNear(cell)
		}
		if !ok {
			l.giveUp(e.ID, endNoStandCell)
			return l.stillFacing(a, p, e)
		}
		if ok && isFlank(goal, cell) {
			// A flank point is walked right up to: stopping a block short
			// of it leaves the swing out of reach.
			within = 0.5
		}
		l.chase = skill.NewNavigate(l.world, l.self, goal, within)
		l.chaseGoal, l.chaseAt = cell, l.tick
	}
	switch st, _ := l.chase.Step(ctx, a); st {
	case skill.Running:
		return nil
	case skill.Failed:
		l.giveUp(e.ID, endNoPath)
	}
	return l.stillFacing(a, p, e)
}

// isFlank says whether the goal stands wide of the target's own cell, which
// is what makes it a flank rather than a head-on stand.
func isFlank(goal protocol.BlockPos, target protocol.BlockPos) bool {
	dx, dz := goal.X()-target.X(), goal.Z()-target.Z()
	return dx*dx+dz*dz > 0
}

// flankCell is where a Striker hits from: a standable cell a couple of
// blocks wide of the target, on the side of the leader's line on it the unit
// already works from, so the squad spreads around what it kills instead of
// queueing at its front. Only a Striker with a leader flanks; everyone else
// takes the front.
func (l *Life) flankCell(p act.Pose, e belief.Entity) (protocol.BlockPos, bool) {
	w := l.warrior
	if w.role != RoleStriker {
		return protocol.BlockPos{}, false
	}
	anchor, leaderOK := w.leader(p)
	if !leaderOK {
		return protocol.BlockPos{}, false
	}
	// The target's front is towards the leader; the flank is to the side
	// of that line, where the unit already is.
	dx, dz := e.Pos.X()-anchor.X(), e.Pos.Z()-anchor.Z()
	length := math.Hypot(float64(dx), float64(dz))
	if length < 1e-3 {
		return protocol.BlockPos{}, false
	}
	dx, dz = dx/float32(length), dz/float32(length)
	px, pz := -dz, dx
	if px*(p.Feet.X()-anchor.X())+pz*(p.Feet.Z()-anchor.Z()) < 0 {
		px, pz = -px, -pz
	}
	near := cellOf(p.Feet)
	for _, dist := range [...]float32{2, 1} {
		x := int32(math.Floor(float64(e.Pos.X() + px*dist)))
		z := int32(math.Floor(float64(e.Pos.Z() + pz*dist)))
		if goal, ok := cellAt(l.world, x, z, near.Y()); ok {
			return goal, true
		}
	}
	return protocol.BlockPos{}, false
}
