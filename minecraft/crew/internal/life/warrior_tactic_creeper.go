package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Creeper tactics: distances are in blocks and durations in ticks of 50 ms.
const (
	// blastReach is how far a normal creeper's blast still hurts: damage
	// falls off to nothing at twice the explosion's power, which is 3.
	blastReach = 6.0
	// chargedBlastReach is the same for a charged creeper, whose power is
	// doubled.
	chargedBlastReach = 12.0
	// defuseReach is how far a creeper's mark has to be before a burning
	// fuse winds back down instead of running out.
	defuseReach = 7.0
	// reachMargin keeps a retreat from ending on the edge of the blast.
	reachMargin = 0.5
	// watchRange is how far a creeper stays the target while a creeper
	// tactic is in charge: out past the fuse's reach, or the retreat would
	// end the tick the creeper left creeperRange, still inside its blast,
	// and a Ranger would have no band to shoot in.
	watchRange = defuseReach + reachMargin
	// chargedWatchRange is the same once a charged creeper is about, whose
	// blast reaches twice as far.
	chargedWatchRange = chargedBlastReach + reachMargin
	// igniteWithin is how close a creeper has to stand before a stop means a
	// burning fuse: it lights inside 3 blocks, and the half block over
	// covers a position update that lags a tick.
	igniteWithin = 3.5
	// lungeRange is how close an unlit creeper has to be before the unit
	// steps in to hit it: the walk to reach is a few ticks, well inside the
	// fuse, where a longer one would spend the fuse before the blow.
	lungeRange = 3.4
	// fuseTicks is a creeper's fuse, and fuseGrace the slack on top of it
	// for a position that was seen late.
	fuseTicks = 30
	fuseGrace = 10
	// stillTicks is how long a creeper inside igniteWithin has to stand
	// still to count as lit: a lit creeper halts where it is.
	stillTicks = 3
	// moveEps is how little a creeper may shift between updates and still
	// count as standing still.
	moveEps = 0.03
	// trackGap is how many ticks unseen make a creeper's last position too
	// old to tell stillness from: it was out of range, not standing.
	trackGap = 5
	// waitPatience is how long the unit waits on a creeper that does not
	// close before it leaves it to the role's own answer.
	waitPatience = 60
	// retreatSlack and minRetreat size a retreat: the gain still owed to
	// the safe distance, plus room for the walk to overshoot, and never so
	// short that the planner has nowhere to go.
	retreatSlack = 1.5
	minRetreat   = 3.0
)

// creeperTactic answers a creeper without the shared flee-and-forget: it
// lunges at one that has not lit its fuse, hits it once and backs out of the
// blast before the fuse runs down. Registered twice, as two variants the
// trainer can compare: the backoff variant gets clear of the blast, the
// ranged variant stays out of the fuse's reach altogether and lets a Ranger
// shoot what is not yet burning.
//
// The fuse is read from the actor's flags when the server has sent them.
// Until it has, and for a server that never does, it is read from behaviour:
// a creeper that lights its fuse stops where it stands. A stopped creeper
// close by is taken for a lit one; the mistake of backing off an idle one
// costs a few steps, the mistake the other way a blast.
type creeperTactic struct {
	name   string
	ranged bool
	// charged says a creeper is charged when its flags are not known, which
	// is how a test stands one in. A nil one means none is.
	charged func(belief.Entity) bool

	units unitScratch[creeperMemory]
}

// creeperMemory is what one unit has seen of the creepers around it.
type creeperMemory struct {
	tracks map[uint64]*creeperTrack
	// holdTicks counts the ticks spent waiting on a creeper that stands off.
	holdTicks int
}

// creeperTrack is one creeper as the unit has watched it.
type creeperTrack struct {
	pos     mgl32.Vec3
	movedAt uint64
	seenAt  uint64
	// moving says the creeper shifted since the last time it was seen.
	moving bool
	// litUntil is when the fuse believed burning is over, from a stop or
	// from this unit's own blow.
	litUntil uint64
}

func init() {
	RegisterTactic(newCreeperTactic("creeper/backoff", false))
	RegisterTactic(newCreeperTactic("creeper/ranged", true))
}

func newCreeperTactic(name string, ranged bool) *creeperTactic {
	return &creeperTactic{name: name, ranged: ranged, units: unitScratch[creeperMemory]{
		init: func(m *creeperMemory) { m.tracks = map[uint64]*creeperTrack{} },
	}}
}

func (c *creeperTactic) Name() string           { return c.name }
func (c *creeperTactic) Family() tactics.Family { return tactics.FamilyCreeper }

func (c *creeperTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	l := f.Life
	w := l.warrior
	if w == nil {
		return false, nil
	}
	// An order is the squad's to give and this tactic's to keep: backing
	// away from a creeper would break a hold, a regroup or a retreat.
	switch w.order().Kind {
	case OrderHold, OrderRegroup, OrderRetreat:
		return false, nil
	}
	if l.mode == modeRegroup {
		return false, nil
	}

	e := f.Target
	m := c.units.of(l)
	m.prune(l)
	t := m.observe(l.tick, e)
	d := horizontal(f.Pose.Feet, e.Pos)
	if !e.FlagsKnown && d <= igniteWithin && !t.moving && l.tick-t.movedAt >= stillTicks {
		t.litUntil = max(t.litUntil, t.movedAt+fuseTicks+fuseGrace)
	}
	lit := l.tick < t.litUntil || e.Ignited
	charged := c.isCharged(e)

	if c.ranged && w.role == RoleRanger && l.rangerArmed(f.Snapshot) {
		return c.shoot(ctx, f, m, d, lit, charged)
	}
	return c.melee(ctx, f, m, t, d, lit, charged)
}

// isCharged says whether a creeper's blast is the doubled one.
func (c *creeperTactic) isCharged(e belief.Entity) bool {
	if e.FlagsKnown {
		return e.Charged
	}
	return c.charged != nil && c.charged(e)
}

// melee lunges at a creeper that has not lit its fuse and backs out of the
// blast once it has, or once the blow is struck.
func (c *creeperTactic) melee(ctx context.Context, f *Fight, m *creeperMemory, t *creeperTrack, d float64, lit, charged bool) (bool, error) {
	l := f.Life
	e := f.Target
	a := f.Actuator
	if lit || charged {
		m.holdTicks = 0
		return c.retreatOrHold(ctx, f, d, charged)
	}
	if d > lungeRange {
		// The creeper comes to the unit. One that does not is left to the
		// role's own answer rather than stood before forever.
		if t.moving {
			m.holdTicks = 0
		} else if m.holdTicks++; m.holdTicks > waitPatience {
			return false, nil
		}
		l.cancelDraw(a)
		return true, l.stillFacing(a, f.Pose, e)
	}
	m.holdTicks = 0
	if !c.canLunge(f, e, d) {
		// Too close to a creeper this unit will not hit: back off, which
		// draws it after the unit and away from whoever stands behind it.
		return c.retreat(ctx, f, d, charged)
	}
	// The lunge is a melee blow, so a half-drawn bow is let go.
	l.cancelDraw(a)
	if !inMeleeReach(f.Pose, e) {
		l.lowerShield(a)
		return true, l.approach(ctx, a, f.Pose, e)
	}
	l.lowerShield(a)
	l.warrior.struck = e.ID
	err := l.strike(a, f.Pose, e, f.Snapshot)
	if l.lastAttack == l.tick {
		// The blow lands on a creeper close enough to light its fuse,
		// whether or not it had: the unit leaves as though it had.
		t.litUntil = l.tick + fuseTicks + fuseGrace
	}
	return true, err
}

// shoot is a Ranger's answer: loose arrows at a creeper that has not closed
// to its fuse, and run from one that has.
func (c *creeperTactic) shoot(ctx context.Context, f *Fight, m *creeperMemory, d float64, lit, charged bool) (bool, error) {
	l := f.Life
	w := l.warrior
	m.holdTicks = 0
	guard := math.Min(f.Params.KiteBackoff, igniteWithin)
	if lit || charged || d < guard {
		return c.retreatOrHold(ctx, f, d, charged)
	}
	if l.enemyNear(f.Pose.Feet, guard) {
		// Something else is in the unit's face: the role's own fight knows
		// how to give ground from it.
		return false, nil
	}
	// The Ranger's kiting radius is wider than the whole band a creeper is
	// ever a target in, which would leave it running and never shooting.
	saved := w.p
	w.p.KiteBackoff = guard
	defer func() { w.p = saved }()
	return true, l.rangerShoot(ctx, f.Actuator, f.Pose, f.Target, f.Snapshot)
}

// canLunge says whether this tick is one to step in and hit: the unit well
// enough to take a blast should the fuse win, the blow ready, and nobody
// standing where the knockback sends the creeper.
func (c *creeperTactic) canLunge(f *Fight, e belief.Entity, d float64) bool {
	l := f.Life
	s := f.Snapshot
	if !s.HealthKnown || float64(s.Health) <= f.Params.HealBelow {
		return false
	}
	if l.tick-l.lastAttack < attackEvery {
		return false
	}
	return !mateBeyond(l, f.Pose.Feet, e.Pos, blastReach)
}

// retreatOrHold backs away from a creeper to be feared, and stands facing it
// once clear: past the safe reach, more walking is only the creeper's
// chase being run from.
func (c *creeperTactic) retreatOrHold(ctx context.Context, f *Fight, d float64, charged bool) (bool, error) {
	if d >= c.safeReach(charged) {
		f.Life.cancelDraw(f.Actuator)
		f.Life.lowerShield(f.Actuator)
		return true, f.Life.stillFacing(f.Actuator, f.Pose, f.Target)
	}
	return c.retreat(ctx, f, d, charged)
}

// retreat walks the unit out of the blast, or out of the fuse's reach for
// the ranged variant, whichever it is cornered short of. Without a way out
// it hands the tick back, having sent nothing.
func (c *creeperTactic) retreat(ctx context.Context, f *Fight, d float64, charged bool) (bool, error) {
	l := f.Life
	far := math.Max(c.safeReach(charged)-d+retreatSlack, minRetreat)
	if l.flee == nil || l.tick-l.fleeAt >= fleeEvery {
		if _, ok := l.fleeGoal(f.Pose, f.Target, far); !ok {
			return false, nil
		}
	}
	l.cancelDraw(f.Actuator)
	l.lowerShield(f.Actuator)
	return true, l.retreat(ctx, f.Actuator, f.Pose, f.Target, far)
}

// safeReach is how far from a creeper the unit is clear of it.
func (c *creeperTactic) safeReach(charged bool) float64 {
	reach := blastReach
	if charged {
		reach = chargedBlastReach
	}
	if c.ranged {
		reach = math.Max(reach, defuseReach)
	}
	return reach + reachMargin
}

// mateBeyond says whether anyone besides the unit stands within reach of the
// creeper on the far side of it, where a blow from from would knock it. The
// entities are players and bots alike: neither is to be blasted. A player
// the PvP policy lets the unit fight is no one to protect.
func mateBeyond(l *Life, from, creeper mgl32.Vec3, reach float64) bool {
	ux, uz := creeper.X()-from.X(), creeper.Z()-from.Z()
	found := false
	l.ents.Each(func(e belief.Entity) {
		if found || !e.Player || l.mayFightPlayer(e) {
			return
		}
		if math.Abs(float64(e.Pos.Y()-creeper.Y())) > threatHeight || horizontal(e.Pos, creeper) > reach {
			return
		}
		if ux*(e.Pos.X()-creeper.X())+uz*(e.Pos.Z()-creeper.Z()) > 0 {
			found = true
		}
	})
	return found
}

// creeperWatch is how far a creeper is still the target. The default
// profile keeps the shipped range; one that names a creeper tactic is given
// the room that tactic needs to finish what it started.
func (l *Life) creeperWatch(pr tactics.Profile, p act.Pose) float64 {
	var ct *creeperTactic
	named := false
	for _, n := range pr.TacticsFor(tactics.FamilyCreeper) {
		t, ok := tacticNamed(n, tactics.FamilyCreeper)
		if !ok {
			continue
		}
		named = true
		if c, ok := t.(*creeperTactic); ok {
			ct = c
			break
		}
	}
	if !named {
		return creeperRange
	}
	if ct != nil {
		found := false
		l.ents.Each(func(e belief.Entity) {
			if !found && !e.Player && kindOf(e.Type) == flee && horizontal(p.Feet, e.Pos) <= chargedWatchRange && ct.isCharged(e) {
				found = true
			}
		})
		if found {
			return chargedWatchRange
		}
	}
	return watchRange
}

// prune forgets creepers that have left the world, as one does by blasting.
func (m *creeperMemory) prune(l *Life) {
	for id := range m.tracks {
		if _, ok := l.ents.Get(id); !ok {
			delete(m.tracks, id)
		}
	}
}

// observe notes where a creeper is this tick and whether it has moved.
func (m *creeperMemory) observe(tick uint64, e belief.Entity) *creeperTrack {
	t, ok := m.tracks[e.ID]
	if !ok || tick-t.seenAt > trackGap {
		// First sight, or too long since: stillness has to be seen, not
		// assumed from a position that may have been left behind.
		if !ok {
			t = &creeperTrack{}
			m.tracks[e.ID] = t
		}
		t.pos, t.movedAt, t.seenAt, t.moving = e.Pos, tick, tick, true
		return t
	}
	t.moving = e.Pos.Sub(t.pos).Len() > moveEps
	if t.moving {
		t.pos, t.movedAt = e.Pos, tick
	}
	t.seenAt = tick
	return t
}
