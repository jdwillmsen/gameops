package life

import (
	"context"
	"math"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Brute tuning that no profile owns. The family's health and focus numbers
// still come from Params; these describe the mobs' own movements and the
// geometry of a sidestep, which are facts about the game, not preferences.
const (
	// bruteRushStep is how far a brute must close in one tick before the
	// tactic treats it as committed to a gore or ram rather than walking.
	// A walking unit covers about 0.22 blocks a tick; a charging hoglin or
	// ravager covers well over twice that, so the threshold sits between a
	// walk and a rush.
	bruteRushStep = 0.45
	// bruteRushTicks is how many consecutive samples must look like a rush:
	// one fast sample can be a server position correction, not a charge.
	bruteRushTicks = 2
	// bruteWarnRange is how near a detected rush is answered. A gore covers
	// the last two blocks in a few ticks, so reacting at five leaves the
	// unit time to clear the line without running from the fight.
	bruteWarnRange = 5.0
	// bruteLungeTicks is how long one sidestep keeps the unit off the line
	// after the last fast sample. A hoglin or ravager crosses the last few
	// blocks in about half a second and then has to turn; the window covers
	// that turn.
	bruteLungeTicks = 8
	// bruteStepAway is how far off the charge line the sidestep goal sits.
	// Two blocks is more than a body's width, so a gore aimed where the
	// unit stood misses even when the server lets the mob's body overlap a
	// corner of the old cell.
	bruteStepAway = 2.0
	// bruteRoarRing is the radius of a ravager's roar in Bedrock. Inside it
	// a raised shield is worth more than the walk a lowered one buys.
	bruteRoarRing = 4.0
	// bruteMateLead is how far beyond a squad-mate the pillar retreat aims.
	// Standing on the mate does not put the mate between the unit and the
	// brute; a body's width or two does.
	bruteMateLead = 2.0
	// bruteFallbackFar is how far a fallback retreat steps when no sidestep
	// or pillar cell is safe. It must clear the press range, not merely the
	// charge line, or the unit stays inside the brute's next swing.
	bruteFallbackFar = 4.0
	// bruteGoalWithin is how near a walk's goal cell counts as arrived. A
	// body occupies most of a cell, so asking for the centre would make the
	// pillar stand in the brute's path while it jockeys for the last inch.
	bruteGoalWithin = 0.6
	// bruteStateCap bounds the per-unit tactic state. Entries are keyed by
	// unit because the tactic values are shared by every unit in the process.
	bruteStateCap = 256
	// bruteStateIdle is how long an unseen unit's state may live before a
	// prune drops it. Units number in the dozens, so this is only a leak
	// guard for a process that churns sessions.
	bruteStateIdle = 2400
)

// bruteSidestep fights a heavy knockback brute by staying off the line its
// gore or ram travels: step wide when the charge starts, close again after it
// whiffs, and keep a shield between the unit and a ravager's roar.
type bruteSidestep struct{}

func (bruteSidestep) Name() string { return "brute/sidestep" }

func (bruteSidestep) Family() tactics.Family { return tactics.FamilyBrute }

func (t bruteSidestep) Engage(ctx context.Context, f *Fight) (handled bool, err error) {
	if !bruteEngageable(f) {
		return false, nil
	}
	l := f.Life
	p := f.Actuator.Pose()
	st := bruteStateFor(l, t.Name(), l.tick)
	started := st.sample(f.Target, p.Feet, l.tick)
	if started {
		st.stepOK = false
	}

	if l.tick < st.lungeUntil {
		// The charge is still under way: leave the line it is aimed along.
		l.cancelDraw(f.Actuator)
		l.lowerShield(f.Actuator)
		l.chase = nil
		if goal, ok := l.bruteStepCell(f, st); ok {
			return bruteWalk(ctx, f, st, goal)
		}
		// No safe cell beside the line: give ground, which is the same
		// answer from farther away.
		return true, l.retreat(ctx, f.Actuator, p, f.Target, bruteFallbackFar)
	}

	st.stepOK = false
	if inMeleeReach(p, f.Target) {
		// A half-drawn bow has no place in a swing or a shield.
		l.cancelDraw(f.Actuator)
		if l.tick-l.lastAttack >= attackEvery {
			// The rush is over and the swing is off cadence: the whiff is
			// the opening.
			l.chase = nil
			l.lowerShield(f.Actuator)
			return true, l.strike(f.Actuator, p, f.Target, f.Snapshot)
		}
		// Bedrock sends no animation packet for the roar, so the shield
		// cannot wait for a wind-up: inside the ring it stays up whenever
		// the unit is not swinging, which is exactly when a bite or the
		// roar can land.
		if name(f.Target.Type) == "ravager" && horizontal(p.Feet, f.Target.Pos) <= bruteRoarRing {
			if _, ok := shieldAt(f.Snapshot); ok {
				return true, l.raiseShield(f.Actuator, p, f.Snapshot)
			}
		}
		// Hold the flank between swings, as the role fight does.
		l.chase = nil
		l.lowerShield(f.Actuator)
		return true, l.circle(f.Actuator, p, f.Target)
	}
	// Out of reach and no rush under way: close on it, letting the role's
	// own approach keep the squad's line.
	l.cancelDraw(f.Actuator)
	l.lowerShield(f.Actuator)
	return true, l.warriorApproach(ctx, f.Actuator, p, f.Target)
}

// brutePillar is the fallback for a badly hurt unit: a brute that is trading
// blows with it will finish it, so it gives ground through the squad's line
// and lets a healthier unit take the next swing.
type brutePillar struct{}

func (brutePillar) Name() string { return "brute/pillar" }

func (brutePillar) Family() tactics.Family { return tactics.FamilyBrute }

func (t brutePillar) Engage(ctx context.Context, f *Fight) (handled bool, err error) {
	if !bruteEngageable(f) {
		return false, nil
	}
	if !f.Snapshot.HealthKnown || float64(f.Snapshot.Health) > f.Params.HealBelow {
		return false, nil
	}
	l := f.Life
	p := f.Actuator.Pose()
	mate, ok := l.bruteMate(p, f.Params.FocusRange)
	if !ok {
		// Nobody to pass the brute to: the role's own heal-or-fight logic
		// knows better than a retreat into empty ground.
		return false, nil
	}
	st := bruteStateFor(l, t.Name(), l.tick)
	l.cancelDraw(f.Actuator)
	l.lowerShield(f.Actuator)
	l.chase = nil
	goal, ok := l.brutePillarCell(p, f.Target, mate)
	if !ok {
		return true, l.retreat(ctx, f.Actuator, p, f.Target, bruteFallbackFar)
	}
	return bruteWalk(ctx, f, st, goal)
}

// bruteEngageable is the shared gate: a tactic never overrides a standing
// order, a fight the engine has already decided to leave, or a meal it has
// decided to eat. It also refuses anything that is not a hostile (or a
// provoked mob that has already hit the unit), so a family entry cannot make
// the unit attack a bystander.
func bruteEngageable(f *Fight) bool {
	l := f.Life
	w := l.warrior
	if w == nil || f.Family != tactics.FamilyBrute {
		return false
	}
	switch kindOf(f.Target.Type) {
	case hostile:
	case provoked:
		if !l.isSuspect(f.Target.ID) {
			return false
		}
	default:
		return false
	}
	switch w.order().Kind {
	case OrderHold, OrderRetreat, OrderRegroup:
		return false
	}
	if l.mode != modeFight || w.healWant {
		return false
	}
	return true
}

// bruteState is what a tactic remembers about one unit between ticks. The
// tactic values are registered once per process and shared by every unit, so
// per-unit memory lives here, keyed by unit and tactic name.
type bruteState struct {
	seen uint64

	targetID   uint64
	targetPos  mgl32.Vec3
	sampleTick uint64
	rushTicks  uint8
	// lungeUntil is the tick the current sidestep stops covering.
	lungeUntil uint64
	// chargeDir is the direction the detected rush travelled.
	chargeDir mgl32.Vec2

	// stepGoal is the cell picked when the rush began, kept for the whole
	// sidestep so the unit does not chase a goal that moves with it.
	stepGoal protocol.BlockPos
	stepOK   bool

	// move is the walk under way, with its goal and the tick it was made.
	move     skill.Skill
	moveGoal protocol.BlockPos
	moveAt   uint64
}

type bruteKey struct {
	life   *Life
	tactic string
}

var bruteStates = struct {
	sync.Mutex
	by map[bruteKey]*bruteState
}{by: map[bruteKey]*bruteState{}}

// bruteStateFor returns the unit's memory for a tactic, making room first if
// a long-lived process has accumulated more units than there can be fighting.
func bruteStateFor(l *Life, tactic string, tick uint64) *bruteState {
	bruteStates.Lock()
	defer bruteStates.Unlock()
	k := bruteKey{life: l, tactic: tactic}
	st, ok := bruteStates.by[k]
	if !ok {
		if len(bruteStates.by) >= bruteStateCap {
			brutePruneLocked(tick)
		}
		st = &bruteState{}
		bruteStates.by[k] = st
	}
	st.seen = tick
	return st
}

func brutePruneLocked(tick uint64) {
	for k, st := range bruteStates.by {
		if tick >= st.seen+bruteStateIdle {
			delete(bruteStates.by, k)
		}
	}
	if len(bruteStates.by) < bruteStateCap {
		return
	}
	var (
		oldest     bruteKey
		oldestAt   uint64 = math.MaxUint64
		haveOldest bool
	)
	for k, st := range bruteStates.by {
		if !haveOldest || st.seen < oldestAt {
			oldest, oldestAt, haveOldest = k, st.seen, true
		}
	}
	if haveOldest {
		delete(bruteStates.by, oldest)
	}
}

// sample remembers where the target was last tick and reports whether a new
// rush has just been detected. The closing speed is measured toward the unit,
// not along the floor, so a mob circling does not look like a charge.
func (st *bruteState) sample(e belief.Entity, feet mgl32.Vec3, tick uint64) (started bool) {
	if st.targetID != e.ID || st.sampleTick+1 != tick {
		st.targetID, st.targetPos, st.sampleTick = e.ID, e.Pos, tick
		st.rushTicks = 0
		return false
	}
	dx := e.Pos.X() - st.targetPos.X()
	dz := e.Pos.Z() - st.targetPos.Z()
	moved := math.Hypot(float64(dx), float64(dz))
	closing := horizontal(st.targetPos, feet) - horizontal(e.Pos, feet)
	st.targetID, st.targetPos, st.sampleTick = e.ID, e.Pos, tick

	// A rush matters only while the unit is still out of reach: once a
	// brute is on top of it the next swing is the problem, and continuing
	// to sidestep every fast sample would keep the unit from ever striking
	// a brute that simply keeps chasing.
	fast := closing >= bruteRushStep && moved >= bruteRushStep &&
		horizontal(e.Pos, feet) <= bruteWarnRange &&
		horizontal(e.Pos, feet) > meleeReach
	if !fast {
		st.rushTicks = 0
		return false
	}
	if st.rushTicks < bruteRushTicks {
		st.rushTicks++
	}
	if st.rushTicks < bruteRushTicks {
		return false
	}
	lunging := tick < st.lungeUntil
	st.chargeDir = mgl32.Vec2{dx / float32(moved), dz / float32(moved)}
	st.lungeUntil = tick + bruteLungeTicks
	return !lunging
}

// bruteStepCell picks the cell the sidestep walks to: two blocks to the side
// of the line the rush travels, on the flank the unit already works from.
// CellAt admits only safe floor, so the step cannot walk into lava or a drop
// the planner would refuse.
func (l *Life) bruteStepCell(f *Fight, st *bruteState) (protocol.BlockPos, bool) {
	if st.stepOK {
		return st.stepGoal, true
	}
	p := f.Actuator.Pose()
	dir := st.chargeDir
	if dir.Len() < 1e-3 {
		// No motion to read: a gore follows the head, so its facing is the
		// best line to leave.
		yaw := float64(f.Target.HeadYaw) * math.Pi / 180
		dir = mgl32.Vec2{float32(-math.Sin(yaw)), float32(math.Cos(yaw))}
	}
	perp := mgl32.Vec2{-dir.Y(), dir.X()}
	if perp.X()*(p.Feet.X()-f.Target.Pos.X())+perp.Y()*(p.Feet.Z()-f.Target.Pos.Z()) < 0 {
		perp = mgl32.Vec2{-perp.X(), -perp.Y()}
	}
	near := cellOf(p.Feet)
	for _, side := range [...]float32{1, -1} {
		for _, dist := range [...]float32{bruteStepAway, bruteStepAway * 0.6} {
			x := int32(math.Floor(float64(p.Feet.X() + perp.X()*side*dist)))
			z := int32(math.Floor(float64(p.Feet.Z() + perp.Y()*side*dist)))
			if c, ok := cellAt(l.world, x, z, near.Y()); ok {
				st.stepGoal, st.stepOK = c, true
				return c, true
			}
		}
	}
	return protocol.BlockPos{}, false
}

// bruteWalk steps a walk to goal, replacing a stale plan when its goal moved
// or the replan cadence came round. Every path through it sends one input.
func bruteWalk(ctx context.Context, f *Fight, st *bruteState, goal protocol.BlockPos) (bool, error) {
	l := f.Life
	p := f.Actuator.Pose()
	if st.move == nil || st.moveGoal != goal || l.tick-st.moveAt >= chaseEvery {
		st.move = skill.NewNavigate(l.world, l.self, goal, bruteGoalWithin)
		st.moveGoal, st.moveAt = goal, l.tick
	}
	switch status, err := st.move.Step(ctx, f.Actuator); status {
	case skill.Running:
		return true, err
	case skill.Failed:
		st.move = nil
		if err != nil {
			return true, err
		}
	default:
		st.move = nil
	}
	// A finished walk sent no input this tick; the unit still owes the
	// server one.
	return true, l.stillFacing(f.Actuator, p, f.Target)
}

// bruteMate is the nearest squad-mate the unit can pass a brute to: a player
// entity inside focus range, else the leader, whose position the unit knows
// even when no entity packet carries it.
func (l *Life) bruteMate(p act.Pose, within float64) (mgl32.Vec3, bool) {
	var (
		best  mgl32.Vec3
		bestD = math.MaxFloat64
		found bool
	)
	l.ents.Each(func(e belief.Entity) {
		// The unit's own player entity is in the tracker too; it cannot
		// take a hit for itself.
		if !e.Player || e.ID == l.selfID {
			return
		}
		d := horizontal(p.Feet, e.Pos)
		if d <= within && d < bestD {
			best, bestD, found = e.Pos, d, true
		}
	})
	if found {
		return best, true
	}
	// A leader on the unit's own spot is not between it and the brute.
	if anchor, ok := l.warrior.leader(p); ok && horizontal(p.Feet, anchor) > bruteGoalWithin &&
		horizontal(p.Feet, anchor) <= within {
		return anchor, true
	}
	return mgl32.Vec3{}, false
}

// brutePillarCell aims past the nearest mate, on the far side from the brute,
// so the brute has to cross the mate to reach the unit. A mate on the brute's
// other side is no help: running at it would mean running past the brute.
func (l *Life) brutePillarCell(p act.Pose, e belief.Entity, mate mgl32.Vec3) (protocol.BlockPos, bool) {
	dx := float64(mate.X() - e.Pos.X())
	dz := float64(mate.Z() - e.Pos.Z())
	d := math.Hypot(dx, dz)
	if d < 1e-3 {
		return protocol.BlockPos{}, false
	}
	ux := float64(p.Feet.X() - e.Pos.X())
	uz := float64(p.Feet.Z() - e.Pos.Z())
	if ux*dx+uz*dz < 0 {
		return protocol.BlockPos{}, false
	}
	ux, uz = dx/d, dz/d
	near := cellOf(p.Feet)
	for _, lead := range [...]float64{bruteMateLead, bruteMateLead + 1, bruteMateLead + 2} {
		x := int32(math.Floor(float64(e.Pos.X()) + ux*(d+lead)))
		z := int32(math.Floor(float64(e.Pos.Z()) + uz*(d+lead)))
		if c, ok := cellAt(l.world, x, z, near.Y()); ok {
			return c, true
		}
	}
	return protocol.BlockPos{}, false
}

func init() {
	RegisterTactic(bruteSidestep{})
	RegisterTactic(brutePillar{})
}
