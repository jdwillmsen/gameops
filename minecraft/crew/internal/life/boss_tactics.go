package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// Boss tactics durations are in ticks of 50 ms, distances in blocks.
const (
	// guardLead is how long before the boss's next blow is due the Vanguard
	// stops swinging and holds its shield up to meet it.
	guardLead = 8
	// guardTail holds the guard a little past the due tick, for a blow that
	// lands late.
	guardTail = 6
	// bossReach is how near the boss a blow is taken to be its melee: the
	// warden's melee box reaches past a unit's own arm.
	bossReach = meleeReach + 1
	// slideTicks is how long a unit that could not back off steps sideways
	// instead, before trying to back off again.
	slideTicks = 20
	// regroupTicks bounds a regroup to 20 s: survivors the boss did not
	// follow go back to fighting it by role rather than park for good.
	regroupTicks = 400
	// regroupRetry is how long a walk back that found no way waits before
	// it is planned again: the search is the costly part, and the start
	// does not move.
	regroupRetry = 60
	// rallyMargin is how far past the boom a squad that does not know its
	// start regroups.
	rallyMargin = 5
	// arrowStep is the spacing of the samples along a bow's line.
	arrowStep = 0.25
)

// bossNear is a boss within its own focus range of the anchor, which reaches
// past focusRange: the Ranger's band is outside it, and a Ranger leading
// itself would otherwise drop the boss it just backed off from. The lowest
// id wins, as focusTarget's tie-break does, so the squad agrees on it.
func (l *Life) bossNear(anchor mgl32.Vec3) (belief.Entity, bool) {
	var best belief.Entity
	l.ents.Each(func(e belief.Entity) {
		if e.Player {
			return
		}
		prof, ok := bossOf(e.Type)
		if !ok || l.ignore[e.ID] > l.tick {
			return
		}
		if (!prof.flies && math.Abs(float64(e.Pos.Y()-anchor.Y())) > threatHeight) || horizontal(e.Pos, anchor) > prof.focusRange {
			return
		}
		if best.ID == 0 || e.ID < best.ID {
			best = e
		}
	})
	return best, best.ID != 0
}

// bossEngaged is the boss's part of warriorEngaged: when the target is a
// boss it notes the blows that landed, the Striker's standing, the squad's
// losses and whether to eat, and fights on — what falling back on a
// leader would do to a hurt unit, the role's own band does better here.
// Any other target leaves the mode as chosen.
func (l *Life) bossEngaged(p act.Pose, s belief.Snapshot, hurt bool, best belief.Entity, m mode) mode {
	w := l.warrior
	b := &w.boss
	prof, ok := bossOf(best.Type)
	if best.ID == 0 || !ok {
		b.active, b.regroup, b.walk = false, false, nil
		return m
	}
	if b.id != best.ID {
		b.id, b.start = best.ID, p.Feet
		b.hitAt, b.shieldOutUntil = 0, 0
		b.regroupUntil, b.rallied = 0, false
		b.walk, b.walkRetry = nil, 0
		b.stillAt, b.stillSince = best.Pos, l.tick
	}
	b.active, b.prof = true, prof
	if best.Pos.Sub(b.stillAt).Len() > perchEpsilon {
		b.stillAt, b.stillSince = best.Pos, l.tick
	}

	d := horizontal(p.Feet, best.Pos)
	if hurt && d <= bossReach {
		// A blow that got through a raised shield says the shield is
		// disabled; one the shield stopped shows no hurt at all.
		if w.shield {
			b.shieldOutUntil = l.tick + prof.shieldBreak
		}
		b.hitAt = l.tick
	}
	if every := prof.meleeEvery; every > 0 && b.hitAt != 0 && !hurt && w.shield &&
		d <= bossReach && l.tick >= b.shieldOutUntil {
		// The due blow passed without hurting a raised shield: the shield
		// took it, and a blocked blow disables it. The beat moves to it.
		if since := l.tick - b.hitAt; since >= every && since%every == guardTail {
			due := l.tick - guardTail
			b.hitAt, b.shieldOutUntil = due, due+prof.shieldBreak
		}
	}

	health := float32(fullHealth)
	if s.HealthKnown {
		health = s.Health
	}
	switch {
	case health < prof.fallBelow:
		b.fallen = true
	case health > prof.commitAbove:
		b.fallen = false
	}

	down := false
	if r, ok := w.squad.(Roster); ok {
		down = r.Down() >= prof.regroupDown
	}
	switch {
	case !down:
		b.regroupUntil, b.rallied = 0, false
	case b.regroupUntil == 0:
		b.regroupUntil = l.tick + regroupTicks
		b.rally = rallyFrom(best.Pos, b.start, p.Feet, prof.boom+rallyMargin)
	}
	if down && b.rallied && inMeleeReach(p, best) {
		// The boss followed the survivor back: there is nowhere further to
		// regroup to, so the regroup is over and the fight is on.
		b.regroupUntil = min(b.regroupUntil, l.tick)
	}
	b.regroup = down && l.tick < b.regroupUntil

	// The meal waits for the boss to be far enough off for it to finish.
	w.healWant = float64(health) <= w.p.HealBelow && d > prof.healClear &&
		!l.enemyNear(p.Feet, w.p.HealSafe) && l.healFood(s).Name != ""
	return modeFight
}

// mealThreatened says whether the meal under way must be let go: an enemy
// within arm's reach of it or, in a boss fight, the boss inside the meal's
// clearance.
func (l *Life) mealThreatened(p act.Pose) bool {
	if l.enemyNear(p.Feet, l.warrior.p.HealSafe) {
		return true
	}
	b := &l.warrior.boss
	if !b.active {
		return false
	}
	e, ok := l.ents.Get(b.id)
	return ok && horizontal(p.Feet, e.Pos) <= b.prof.healClear
}

// bossFight is the warrior's fight with a boss, on its role's terms: the
// Vanguard alone holds the melee, the Striker joins it only under cover,
// and the Ranger shoots from outside what the boss reaches.
func (l *Life) bossFight(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	w := l.warrior
	if w.boss.prof.perchOnly {
		return l.perchFight(ctx, a, p, e, s)
	}
	if w.boss.regroup {
		l.cancelDraw(a)
		return l.bossRegroup(ctx, a, p, e, s)
	}
	w.boss.walk = nil
	switch w.role {
	case RoleVanguard:
		l.cancelDraw(a)
		if inMeleeReach(p, e) && l.bossGuard(s) {
			return l.raiseShield(a, p, s)
		}
		return l.warriorMelee(ctx, a, p, e, s)
	case RoleStriker:
		l.cancelDraw(a)
		return l.bossStriker(ctx, a, p, e, s)
	}
	return l.bossRanger(ctx, a, p, e, s)
}

// bossGuard says whether the boss's next blow is due, so the Vanguard holds
// its shield rather than swing into it. The cadence runs from the last blow
// that hurt: a blocked one shows nothing, so the beat is kept rather than
// reset. Before a first blow there is no beat to keep, and a first blow a
// raised shield happened to block shows nothing either, so hitAt stays zero
// and the guard waits for a blow that lands. A shield the boss disabled
// guards nothing.
func (l *Life) bossGuard(s belief.Snapshot) bool {
	b := &l.warrior.boss
	every := b.prof.meleeEvery
	if every == 0 || b.hitAt == 0 || l.tick < b.shieldOutUntil {
		return false
	}
	if _, ok := shieldAt(s); !ok {
		return false
	}
	since := l.tick - b.hitAt
	phase := since % every
	return phase >= every-guardLead || (since >= every && phase < guardTail)
}

// bossStriker waits off the boss and joins the melee only while the
// Vanguard is on the boss to take its blows and the Striker has health to
// spare. Too hurt to join, it waits beyond the boom, which would wear down
// what it needs to commit; hurt past the fall line, it leaves the darkness
// until healed.
func (l *Life) bossStriker(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	b := &l.warrior.boss
	switch {
	case b.fallen:
		return l.bossKeep(a, p, e, s, math.Max(b.prof.darkness+1, b.prof.strikerFar), math.Inf(1))
	case s.HealthKnown && s.Health <= b.prof.commitAbove, b.regroupUntil != 0:
		// Hurt, or the squad too far down to hold the boss off it.
		near := math.Max(b.prof.boom+1, b.prof.strikerNear)
		return l.bossKeep(a, p, e, s, near, near+b.prof.strikerFar-b.prof.strikerNear)
	case l.strikerCovered(p, e):
		return l.warriorMelee(ctx, a, p, e, s)
	}
	return l.bossKeep(a, p, e, s, b.prof.strikerNear, b.prof.strikerFar)
}

// strikerCovered says whether the Vanguard is close enough to the boss to be
// what it hits. Without a roster the leader stands in for the Vanguard,
// whom the chain of command puts first.
func (l *Life) strikerCovered(p act.Pose, e belief.Entity) bool {
	w := l.warrior
	van, ok := mgl32.Vec3{}, false
	if r, isRoster := w.squad.(Roster); isRoster {
		van, ok = r.Member(RoleVanguard)
	} else {
		van, ok = w.leader(p)
	}
	return ok && horizontal(van, e.Pos) <= w.boss.prof.commitWithin
}

// bossKeep holds a band off the boss, facing it, with a shield against an
// arrow closing in.
func (l *Life) bossKeep(a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot, near, far float64) error {
	in, aimed := l.bandStep(p, e, near, far, false)
	if aimed && in.Forward == 0 && l.arrowIncoming(p) {
		return l.raiseShield(a, p, s)
	}
	l.lowerShield(a)
	return a.Tick(in)
}

// bossRanger holds the Ranger's band and shoots down a clear line on the
// bow's cadence. A Ranger without a bow keeps the band all the same: the
// melee is the Vanguard's, until the boss is at arm's length and either
// going for the Ranger or has it cornered, when the sword is the last resort.
func (l *Life) bossRanger(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	w := l.warrior
	b := &w.boss
	l.lowerShield(a)
	faced := l.bossFaces(e, p)
	in, aimed := l.bandStep(p, e, b.prof.rangerNear, b.prof.rangerFar, faced)
	if inMeleeReach(p, e) && (faced || l.tick < b.corneredUntil) {
		l.cancelDraw(a)
		return l.warriorMelee(ctx, a, p, e, s)
	}
	bow := bowAt(s)
	if !l.rangerArmed(s) {
		l.cancelDraw(a)
		return a.Tick(in)
	}
	// A held draw keeps the meal from starting, and no line may come for as
	// long as the boss moves: let the draw go so the next tick can eat.
	if w.healWant {
		l.cancelDraw(a)
		return a.Tick(in)
	}
	// A strafe faces along the step, not the boss, so the arrow waits for
	// the next tick that faces it.
	shoot := aimed && l.bowLine(p, e)
	switch {
	case w.drawing:
		w.drawTicks++
		if w.drawTicks >= bowDraw && shoot {
			if err := a.ReleaseItem(bow.Slot, stackAt(s, bow.Slot, bow.Name)); err != nil {
				return err
			}
			w.drawing, w.drawTicks, w.drawItem = false, 0, belief.Item{}
			w.lastShot = l.tick
			w.struck = e.ID
		}
	case shoot && (w.lastShot == 0 || l.tick-w.lastShot >= ticksOf(w.p.ShotEvery)):
		if err := a.SelectHotbar(bow.Slot, bow); err != nil {
			return err
		}
		if err := a.UseItem(bow.Slot, bow); err != nil {
			return err
		}
		w.drawing, w.drawTicks, w.drawItem = true, 0, bow
		in.StartUsing = true
	}
	return a.Tick(in)
}

// bandStep is the walk that keeps a unit between near and far off the boss:
// back off facing it, close in facing it, or stand. A unit the boss faces,
// or one whose backing off moved nowhere, steps sideways as well, and one
// shut in on both sides as well stands facing the boss for a while. aimed
// says the input faces the boss.
func (l *Life) bandStep(p act.Pose, e belief.Entity, near, far float64, faced bool) (act.Input, bool) {
	b := &l.warrior.boss
	in := l.face(p, e)
	if l.warrior.hold {
		return in, true
	}

	moved := math.Hypot(float64(p.Feet.X()-b.lastX), float64(p.Feet.Z()-b.lastZ))
	b.lastX, b.lastZ = p.Feet.X(), p.Feet.Z()
	if b.stuck < math.MaxUint8 && b.moving && moved < 0.05 {
		b.stuck++
	} else {
		b.stuck = 0
	}
	if b.blocked > 0 && math.Hypot(float64(p.Feet.X()-b.blockX), float64(p.Feet.Z()-b.blockZ)) > 1 {
		b.blocked = 0
	}
	if b.stuck >= backStuckTicks {
		// A wall or a body: slide along it, and turn the other way when
		// the slide itself is what stuck. Shut in that way too, the unit
		// is cornered: flipping on would keep it turned off the boss for
		// good, with no arrow and no blow, so it stands and fights.
		b.stuck = 0
		if b.blocked == 0 {
			b.blockX, b.blockZ = p.Feet.X(), p.Feet.Z()
		}
		b.blocked++
		switch {
		case b.blocked >= 3:
			b.blocked, b.slideUntil = 0, 0
			b.corneredUntil = l.tick + l.cornerTicks()
		case b.blocked == 2:
			b.strafe = -b.strafe
			b.slideUntil = l.tick + slideTicks
		default:
			b.slideUntil = l.tick + slideTicks
		}
	}
	if b.strafe == 0 {
		b.strafe = 1
	}

	radial := 0
	switch d := horizontal(p.Feet, e.Pos); {
	case d < near:
		radial = -1
	case d > far:
		radial = 1
	}
	aimed := true
	switch {
	case l.tick < b.corneredUntil:
		// Cornered: stand where it is, facing the boss.
	case faced || l.tick < b.slideUntil:
		to := mgl32.Vec2{e.Pos.X() - p.Feet.X(), e.Pos.Z() - p.Feet.Z()}
		if to.Len() < 1e-3 {
			break
		}
		to = to.Normalize()
		side := float32(b.strafe)
		dir := mgl32.Vec2{-to.Y() * side, to.X() * side}.Add(to.Mul(float32(radial)))
		in = act.Input{Yaw: float32(yawOf(dir.Normalize())), Forward: 1}
		aimed = false
	case radial != 0:
		// Facing the boss, a negative walk is a step back.
		in.Forward = float32(radial)
	}
	b.moving = in.Forward != 0
	return in, aimed
}

// cornerTicks is how long a unit that could neither back off nor slide
// either way stands and fights where it is: the bow's cadence wait plus a
// full draw, so a Ranger gets an arrow off before it tries again.
func (l *Life) cornerTicks() uint64 {
	return ticksOf(l.warrior.p.ShotEvery) + bowDraw
}

// bossFaces says whether the boss's head points at the unit, which is who
// it is about to go for.
func (l *Life) bossFaces(e belief.Entity, p act.Pose) bool {
	to := mgl32.Vec2{p.Feet.X() - e.Pos.X(), p.Feet.Z() - e.Pos.Z()}
	if to.Len() < 1e-3 {
		return true
	}
	off := math.Mod(float64(e.HeadYaw)-yawOf(to), 360)
	if off > 180 {
		off -= 360
	} else if off < -180 {
		off += 360
	}
	return math.Abs(off) <= l.warrior.boss.prof.facingCone
}

// bowLine says whether an arrow from the unit's eye flies clear to the
// boss's body: no solid block on the straight line between them. The arc
// rises over the line, so a clear line is the conservative test.
func (l *Life) bowLine(p act.Pose, e belief.Entity) bool {
	from := p.Feet.Add(mgl32.Vec3{0, 1.62, 0})
	d := e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}).Sub(from)
	n := int(d.Len()/arrowStep) + 1
	for i := 1; i < n; i++ {
		if l.body.Solid(cellOf(from.Add(d.Mul(float32(i) / float32(n))))) {
			return false
		}
	}
	return true
}

// bossRegroup walks a survivor back to the squad's start instead of trading
// blows two-handed, and holds there facing the boss, until the boss follows
// it into reach or the regroup runs out, when bossEngaged ends it.
func (l *Life) bossRegroup(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	w := l.warrior
	b := &w.boss
	at := b.rally
	if r, ok := w.squad.(Roster); ok {
		if st, ok := r.Start(); ok {
			at = st
		}
	}
	if horizontal(p.Feet, at) <= w.p.RegroupWithin {
		b.walk, b.rallied = nil, true
		if l.shieldUp(p, s) {
			return l.raiseShield(a, p, s)
		}
		l.lowerShield(a)
		return l.stillFacing(a, p, e)
	}
	l.lowerShield(a)
	if l.tick < b.walkRetry {
		return l.stillFacing(a, p, e)
	}
	cell := cellOf(at)
	if b.walk == nil || (l.tick-b.walkAt >= chaseEvery && b.walkGoal != cell) {
		goal, ok := l.standNear(cell)
		if !ok {
			b.walkRetry = l.tick + regroupRetry
			return l.stillFacing(a, p, e)
		}
		b.walk = skill.NewNavigate(l.world, l.self, goal, w.p.RegroupWithin/2)
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

// rallyFrom is where a squad that does not know its start regroups: where
// the unit began the fight, unless that is within clear of the boss, as
// it is for one that began at the boss's side. Then it is clear off the
// boss along the line through where the unit began, or through the unit
// itself when it began on top of the boss.
func rallyFrom(boss, began, feet mgl32.Vec3, clear float64) mgl32.Vec3 {
	if horizontal(began, boss) >= clear {
		return began
	}
	dir := mgl32.Vec2{began.X() - boss.X(), began.Z() - boss.Z()}
	if dir.Len() < 1e-3 {
		dir = mgl32.Vec2{feet.X() - boss.X(), feet.Z() - boss.Z()}
	}
	if dir.Len() < 1e-3 {
		dir = mgl32.Vec2{1, 0}
	}
	dir = dir.Normalize().Mul(float32(clear))
	return mgl32.Vec3{boss.X() + dir.X(), began.Y(), boss.Z() + dir.Y()}
}
