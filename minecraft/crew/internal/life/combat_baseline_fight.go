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
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	// baselineRetreat is how far a retreat from an ordinary mob walks before
	// it replans, the reactive defence's own figure.
	baselineRetreat = 8.0
	// coverMinGap keeps a cover cell from being the archer's own doorstep.
	coverMinGap = 2.0
)

// coverSteps are the distances tried for a cell out of an archer's sight,
// nearest first: a corner close by beats a run across open ground.
var coverSteps = [...]float64{3, 5, 7}

// baselineState is what the baseline remembers of one unit between ticks. It
// lives in a per-unit table, not in Life, so the reactive defence's own
// struct is left alone.
type baselineState struct {
	// fleeing is the health latch, held from the flee bar to ResumeHealth.
	fleeing bool

	// The decision of the tick that last ran Engaged, for Fight to carry out.
	decidedAt uint64
	target    uint64
	stance    stance
	in        situation
	// track is the creeper's record, kept so Fight can mark a blow on it.
	track *creeperTrack

	shield     bool
	shieldItem belief.Item

	arrows   map[uint64]arrowSample
	creepers creeperMemory
	weave    skelState
}

var baselineUnits = unitScratch[baselineState]{init: func(s *baselineState) {
	s.arrows = map[uint64]arrowSample{}
	s.creepers.tracks = map[uint64]*creeperTrack{}
}}

func (l *Life) baselineOn() bool { return l.cfg.Combat != nil && l.warrior == nil }

// baselineReset forgets the latch and the shield, as after a death: the
// server drops a held use with the body.
func (l *Life) baselineReset() {
	if st, ok := baselineUnits.has(l); ok {
		st.fleeing, st.shield, st.shieldItem = false, false, belief.Item{}
		st.decidedAt, st.target, st.stance, st.track = 0, 0, stanceFight, nil
		clear(st.arrows)
		clear(st.creepers.tracks)
		st.weave = skelState{}
	}
}

// baselineNote advances the flee latch by one tick, whether or not anything
// is near: health regained while out of a fight clears it.
func (l *Life) baselineNote(s belief.Snapshot) {
	if !l.baselineOn() {
		return
	}
	c, st := l.cfg.Combat, baselineUnits.of(l)
	night, under := isNight(s), c.underGeared(s)
	was := st.fleeing
	st.fleeing = c.latch(was, s.Health, s.HealthKnown, night, under)
	switch {
	case st.fleeing && !was:
		l.logEvent("combat_flee", map[string]any{"health": s.Health, "night": night, "under_geared": under})
	case !st.fleeing && was:
		l.logEvent("combat_resume", map[string]any{"health": s.Health})
	}
}

// creeperLimit is how far off a creeper is still a threat: a charged or lit
// one reaches farther than the plain range.
func (l *Life) creeperLimit(e belief.Entity) float64 {
	if !l.baselineOn() {
		return creeperRange
	}
	switch {
	case e.FlagsKnown && e.Charged:
		return chargedWatchRange
	case e.Ignited:
		return watchRange
	}
	return creeperRange
}

// threatRank orders candidate targets, lowest first: distance, except that a
// creeper with its fuse lit comes before everything else.
func (l *Life) threatRank(e belief.Entity, d float64) float64 {
	if l.baselineOn() && kindOf(e.Type) == flee && e.Ignited {
		return d - nearbyRange*4
	}
	return d
}

// baselineMode turns the baseline's verdict on a target into the mode Engaged
// adopts, and keeps the verdict for Fight.
func (l *Life) baselineMode(p act.Pose, s belief.Snapshot, e belief.Entity, m mode) mode {
	if !l.baselineOn() || e.ID == 0 {
		return m
	}
	c, st := l.cfg.Combat, baselineUnits.of(l)
	in := l.situationOf(st, p, s, e)
	sn := c.decide(in)
	st.decidedAt, st.target, st.stance, st.in = l.tick, e.ID, sn, in
	if sn == stanceRetreat || sn == stanceCover {
		return modeRetreat
	}
	return modeFight
}

// situationOf reads the world into the facts decide works from. It runs once
// a tick, from Engaged, because noting a creeper twice in one tick would
// read as it standing still.
func (l *Life) situationOf(st *baselineState, p act.Pose, s belief.Snapshot, e belief.Entity) situation {
	c := l.cfg.Combat
	d := horizontal(p.Feet, e.Pos)
	in := situation{
		Dist:     d,
		InReach:  inMeleeReach(p, e),
		Cornered: l.retreating >= retreatLimit,
		Fleeing:  st.fleeing,
		Night:    isNight(s),
		Under:    c.underGeared(s),
	}
	switch {
	case kindOf(e.Type) == flee:
		in.Threat = threatCreeper
		st.creepers.prune(l)
		st.track, in.Lit = st.creepers.look(l.tick, e, d)
		in.Charged = e.FlagsKnown && e.Charged
	case bowSkeletons[name(e.Type)] && math.Abs(float64(e.Pos.Y()-p.Feet.Y())) <= 1 && d <= shooterRange:
		in.Threat = threatArcher
		_, has := shieldAt(s)
		in.Shield = c.Shield && has
		if in.Shield {
			in.ArrowIn = scanArrowSamples(l.ents, st.arrows, l.tick, p.Feet, tactics.Defaults().ArrowNear*1.5)
			in.Aimed = skeletonAimed(e, p)
			hold := ticksOf(tactics.Defaults().MeleeShieldTick)
			in.GuardTurn = l.tick%(hold+skelAdvance) < hold
		}
		in.Stalled = st.stalled(l.tick, e.ID, d)
	}
	return in
}

// stalled says the weave has stopped closing on the archer, as at a wall the
// one-step lookahead cannot see round, and then holds that for skelYield
// ticks so the pathing layer gets the walk.
func (st *baselineState) stalled(tick, id uint64, d float64) bool {
	w := &st.weave
	if tick < w.yieldUntil {
		return true
	}
	if w.target != id || d < w.best-skelProgress {
		w.target, w.best, w.bestAt = id, d, tick
		return false
	}
	if tick-w.bestAt > skelStall {
		w.yieldUntil, w.best, w.bestAt = tick+skelYield, d, tick
		return true
	}
	return false
}

// baselineFight carries out this tick's verdict when it is not the plain
// fight or the plain retreat, which Fight does itself. It reports whether it
// sent the tick's input.
func (l *Life) baselineFight(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) (bool, error) {
	if !l.baselineOn() {
		return false, nil
	}
	st := baselineUnits.of(l)
	if st.decidedAt != l.tick || st.target != e.ID {
		l.baselineLowerShield(a)
		return false, nil
	}
	if st.stance != stanceGuard {
		l.baselineLowerShield(a)
	}
	switch st.stance {
	case stanceGuard:
		return true, l.baselineRaiseShield(a, p, s, e)
	case stanceHold:
		return true, l.stillFacing(a, p, e)
	case stanceWeave:
		return l.baselineWeave(a, p, e)
	case stanceCover:
		return l.baselineCover(ctx, a, p, e)
	case stanceRetreat:
		if st.in.Threat == threatCreeper {
			return true, l.retreat(ctx, a, p, e, creeperFar(st.in))
		}
	case stanceFight:
		if st.in.Threat == threatCreeper {
			return true, l.baselineStrikeCreeper(a, p, e, s, st)
		}
	}
	return false, nil
}

// creeperFar is how far a retreat from a creeper walks: out of its blast, and
// no less than a plain retreat.
func creeperFar(in situation) float64 {
	reach := blastReach
	if in.Charged {
		reach = chargedBlastReach
	}
	return math.Max(baselineRetreat, reach+reachMargin-in.Dist+retreatSlack)
}

// baselineStrikeCreeper is the one blow at a creeper the baseline allows: it
// is cornered, in reach, and the fuse is not lit. The creeper then counts as
// lit, as the warrior's lunge does, so the next tick backs off or holds.
func (l *Life) baselineStrikeCreeper(a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot, st *baselineState) error {
	err := l.strike(a, p, e, s)
	if l.lastAttack == l.tick && st.track != nil {
		st.track.litUntil = l.tick + fuseTicks + fuseGrace
	}
	return err
}

// baselineWeave closes on an archer along the zigzag. It hands the tick back
// when no diagonal or straight step is safe.
func (l *Life) baselineWeave(a skill.Actuator, p act.Pose, e belief.Entity) (bool, error) {
	side := 1
	if (l.tick/skelLeg)%2 == 1 {
		side = -1
	}
	angle := skelAngle
	if horizontal(p.Feet, e.Pos) <= skelBreakOff {
		angle = 0
	}
	yaw, ok := l.skeletonHeading(p, e, side, angle)
	if !ok {
		return false, nil
	}
	in := l.face(p, e)
	in.Yaw, in.Forward = float32(yaw), 1
	return true, a.Tick(in)
}

// baselineCover walks to a cell the archer cannot see. With none within
// reach it hands the tick back to the plain retreat.
func (l *Life) baselineCover(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity) (bool, error) {
	if l.flee == nil || l.tick-l.fleeAt >= fleeEvery {
		goal, ok := l.coverGoal(p, e)
		if !ok {
			return false, nil
		}
		l.flee = skill.NewNavigate(l.world, l.self, goal, 1)
		l.fleeAt = l.tick
	}
	st, _ := l.flee.Step(ctx, a)
	if st == skill.Running {
		return true, nil
	}
	l.flee = nil
	l.fleeAt = l.tick
	return true, l.stillFacing(a, p, e)
}

// coverGoal finds the nearest reachable standing cell with a solid block on
// the line from the archer to it, searched away from the archer first.
func (l *Life) coverGoal(p act.Pose, e belief.Entity) (protocol.BlockPos, bool) {
	dir := away(p, e)
	from := cellOf(p.Feet)
	for _, dist := range coverSteps {
		for _, turn := range [...]float64{0, 45, -45, 90, -90, 135, -135, 180} {
			r := turn * math.Pi / 180
			c, sn := float32(math.Cos(r)), float32(math.Sin(r))
			d := mgl32.Vec2{dir.X()*c - dir.Y()*sn, dir.X()*sn + dir.Y()*c}
			x := int32(math.Floor(float64(p.Feet.X()) + float64(d.X())*dist))
			z := int32(math.Floor(float64(p.Feet.Z()) + float64(d.Y())*dist))
			goal, ok := cellAt(l.world, x, z, from.Y())
			if !ok {
				continue
			}
			gp := mgl32.Vec3{float32(goal.X()) + 0.5, float32(goal.Y()), float32(goal.Z()) + 0.5}
			if horizontal(gp, e.Pos) < coverMinGap {
				continue
			}
			// bowLine reads the line from a pose's eye to an entity's body,
			// so it is asked from the archer toward the cell.
			if l.bowLine(act.Pose{Feet: e.Pos}, belief.Entity{Pos: gp}) {
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

// baselineRaiseShield holds the shield out toward the archer. The use starts
// once and stands until released, as a meal does.
func (l *Life) baselineRaiseShield(a skill.Actuator, p act.Pose, s belief.Snapshot, e belief.Entity) error {
	st := baselineUnits.of(l)
	in := l.face(p, e)
	if !st.shield {
		shield, ok := shieldAt(s)
		if !ok {
			return a.Tick(in)
		}
		if err := a.SelectHotbar(shield.Slot, shield); err != nil {
			return err
		}
		if err := a.UseItem(shield.Slot, shield); err != nil {
			return err
		}
		st.shield, st.shieldItem = true, shield
		in.StartUsing = true
	}
	return a.Tick(in)
}

// baselineLowerShield lets the shield down, as a swing, a walk or a meal
// needs the hand. It sends no input; the caller's own tick does.
func (l *Life) baselineLowerShield(a skill.Actuator) {
	if !l.baselineOn() {
		return
	}
	st, ok := baselineUnits.has(l)
	if !ok || !st.shield {
		return
	}
	_ = a.ReleaseItem(st.shieldItem.Slot, st.shieldItem)
	st.shield = false
}
