package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Kiting fights a slow melee mob by hit and run for every role: strike while
// it is still outside its own swing, back off along a path the planner
// accepts before it can answer, let it come back, strike again. The mob's
// pace and blow rhythm are read off the fight itself, so the one
// implementation serves every family it is registered for. A Ranger with a
// bow keeps its own bow logic.
const (
	// kiteUnitSpeed is a walking unit's steady pace in blocks per tick, from
	// the movement model's step over its ground carry. A mob faster than
	// this cannot be backed away from, only fought.
	kiteUnitSpeed = 0.216
	// kiteOutpace is how much faster than the unit a mob may be before
	// backing off stops paying: smoothing and a knocked-back mob's re-close
	// make the measured pace noisy.
	kiteOutpace = 1.1
	// kiteMobCooldown is the blow interval assumed before one has been
	// seen: Bedrock's default melee cooldown, a second. A shorter interval
	// seen in the fight replaces it, never a longer, since a mob that missed
	// for want of reach would otherwise look slower than it is.
	kiteMobCooldown = 20
	// kiteMinCooldown floors the learned interval so a double hit from two
	// mobs cannot read as one that swings every tick.
	kiteMinCooldown = 10
	// kiteLead is how many ticks ahead of a blow the unit starts backing:
	// a walk from rest takes about three ticks to reach pace, doubled for
	// the turn the strike before it costs.
	kiteLead = 6
	// kiteStrikeSafe is how long the mob must be unable to swing for a
	// strike to be free: this tick's swing and the mob's step after it.
	kiteStrikeSafe = 2
	// kiteMinSpeed separates a mob that is coming from one that is
	// standing; a standing mob is walked up to by the role, not awaited.
	kiteMinSpeed = 0.03
	// kiteSmooth weights the newest step in the pace estimate, so one
	// stutter in the mob's updates does not read as a sprint.
	kiteSmooth = 0.5
	// kiteStall is how many ticks an awaited mob may fail to close before
	// the role walks up to it instead.
	kiteStall = 8
	// kiteBackMax is how many backing ticks are judged together, and
	// kiteMinGain the distance they must open: a mob that keeps pace is
	// being run from for nothing, and a wall behind looks the same.
	kiteBackMax = 24
	kiteMinGain = 0.3
	// kiteGiveUp is how long the role fights instead after backing off
	// stopped paying, before kiting is tried again.
	kiteGiveUp = 60
)

func init() {
	for _, k := range []struct {
		name   string
		family tactics.Family
	}{
		{"zombie/kite", tactics.FamilyZombie},
		{"spider/kite", tactics.FamilySpider},
		{"brute/kite", tactics.FamilyBrute},
	} {
		RegisterTactic(kiteTactic{name: k.name, family: k.family})
	}
}

type kiteTactic struct {
	name   string
	family tactics.Family
}

func (t kiteTactic) Name() string           { return t.name }
func (t kiteTactic) Family() tactics.Family { return t.family }

// kiteState is what one unit's kiting remembers between ticks. A tactic is
// shared by every unit, so it is kept per Life.
type kiteState struct {
	// tick and target say which fight the rest belongs to: any gap in
	// either starts it over.
	tick   uint64
	target uint64

	mobAt  mgl32.Vec3
	lastD  float64
	speed  float64
	health float32
	seen   bool

	// lastHit is when the mob last landed a blow, 0 for none yet, and
	// hitEvery the interval between blows to expect.
	lastHit  uint64
	hitEvery uint64

	// holding says the unit is waiting for the mob to come, with stall the
	// ticks it has not closed.
	holding bool
	stall   int

	// backing is a backoff under way: from where it began, for how long,
	// and the walk that carries it.
	backing   bool
	backFrom  float64
	backTicks int
	walk      skill.Skill
	walkAt    uint64

	// offUntil is when kiting is tried again after it stopped paying.
	offUntil uint64
}

var kiteStates unitScratch[kiteState]

func kiteStateOf(l *Life, f *Fight) *kiteState {
	st := kiteStates.of(l)
	if st.target != f.Target.ID || st.tick+1 != l.tick {
		*st = kiteState{target: f.Target.ID}
	}
	return st
}

// observe takes this tick's reading of the mob: its pace, and a blow if the
// unit's health fell with the mob at arm's length.
func (st *kiteState) observe(l *Life, f *Fight) {
	e, p, s := f.Target, f.Pose, f.Snapshot
	if st.seen {
		step := horizontal(st.mobAt, e.Pos)
		st.speed += kiteSmooth * (step - st.speed)
	}
	st.mobAt = e.Pos
	if st.hitEvery == 0 {
		st.hitEvery = kiteMobCooldown
	}
	if s.HealthKnown {
		if st.seen && s.Health < st.health && horizontal(p.Feet, e.Pos) <= meleeReach+1 {
			if st.lastHit != 0 {
				if gap := l.tick - st.lastHit; gap < st.hitEvery {
					st.hitEvery = max(gap, kiteMinCooldown)
				}
			}
			st.lastHit = l.tick
		}
		st.health = s.Health
	}
	st.seen, st.tick = true, l.tick
}

// ready says whether the mob can swing at this tick.
func (st *kiteState) ready(now uint64) bool {
	return st.lastHit == 0 || now >= st.lastHit+st.hitEvery
}

// untilBlow is how many ticks until the mob can land a blow on a unit at d:
// the longer of its cooldown still running and the walk to its swing range.
func (st *kiteState) untilBlow(now uint64, d, swing float64) float64 {
	cool := 0.0
	if !st.ready(now) {
		cool = float64(st.lastHit + st.hitEvery - now)
	}
	walk := 0.0
	if d > swing {
		if st.speed < kiteMinSpeed {
			return math.Inf(1)
		}
		walk = (d - swing) / st.speed
	}
	return math.Max(cool, walk)
}

func (t kiteTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	l := f.Life
	w := l.warrior
	if w == nil {
		return false, nil
	}
	st := kiteStateOf(l, f)
	d := horizontal(f.Pose.Feet, f.Target.Pos)
	closing := st.seen && d < st.lastD-0.01
	st.observe(l, f)
	st.lastD = d
	if !t.eligible(f, st) {
		st.holding, st.backing, st.walk = false, false, nil
		return false, nil
	}

	p, e, s, a := f.Pose, f.Target, f.Snapshot, f.Actuator
	swing := math.Max(f.Params.PressRange, f.Params.OrbitMin)
	until := st.untilBlow(l.tick, d, swing)
	inReach := inMeleeReach(p, e)
	jumping := w.role == RoleStriker && !p.OnGround && l.tick < w.critUntil

	// The swing comes first: while the mob cannot answer it, hitting is
	// better than any step.
	if jumping || (inReach && l.tick-l.lastAttack >= attackEvery && until >= kiteStrikeSafe) {
		l.chase = nil
		st.holding = false
		return true, t.strike(a, f, p, e, s, until)
	}

	if until < kiteLead {
		if st.speed > kiteUnitSpeed*kiteOutpace || l.tick < st.offUntil {
			return false, nil
		}
		return t.backOff(ctx, f, st, d)
	}
	st.backing = false

	if inReach || st.holding || (closing && st.speed >= kiteMinSpeed && d <= f.Params.KiteBackoff) {
		if !closing {
			st.stall++
		} else {
			st.stall = 0
		}
		if st.stall > kiteStall {
			st.holding, st.stall = false, 0
			return false, nil
		}
		st.holding = true
		l.chase = nil
		return true, t.guard(a, f, p, e, s)
	}
	return false, nil
}

// eligible says whether kiting has anything to say this tick: a plain
// fight, not an order to hold, a meal, a boss, a Ranger with its bow, or
// another mob already inside the unit's reach to be backed into.
func (t kiteTactic) eligible(f *Fight, st *kiteState) bool {
	l, w := f.Life, f.Life.warrior
	if l.mode != modeFight || w.hold || w.healWant || w.healing || w.boss.active {
		return false
	}
	if w.role == RoleRanger && l.rangerArmed(f.Snapshot) {
		return false
	}
	crowded := false
	l.ents.Each(func(e belief.Entity) {
		if crowded || e.Player || e.ID == f.Target.ID {
			return
		}
		if k := kindOf(e.Type); k == other || (k == provoked && !l.isSuspect(e.ID)) {
			return
		}
		crowded = inMeleeReachAt(f.Pose.Feet, e.Pos)
	})
	return !crowded
}

// strike lands the blow. A Striker with time in hand jumps first and swings
// from the fall, as in the role's own fight; the window bounds the jump.
func (t kiteTactic) strike(a skill.Actuator, f *Fight, p act.Pose, e belief.Entity, s belief.Snapshot, until float64) error {
	l, w := f.Life, f.Life.warrior
	l.lowerShield(a)
	window := ticksOf(f.Params.CritWindow)
	if w.role == RoleStriker && p.OnGround && l.tick >= w.critUntil && !l.arrowIncoming(p) && until >= float64(window) {
		w.critUntil = l.tick + window
		in := l.face(p, e)
		in.Jump = true
		return a.Tick(in)
	}
	if p.OnGround || p.Rise() < 0 || l.tick >= w.critUntil {
		w.struck = e.ID
		return l.strike(a, p, e, s)
	}
	return a.Tick(l.face(p, e))
}

// guard waits for the mob facing it, behind the shield when there is one: a
// Vanguard always, the others when a blow or an arrow is coming.
func (t kiteTactic) guard(a skill.Actuator, f *Fight, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	l, w := f.Life, f.Life.warrior
	if _, ok := shieldAt(s); ok && (w.role == RoleVanguard || l.shieldUp(p, s)) {
		return l.raiseShield(a, p, s)
	}
	l.lowerShield(a)
	return l.stillFacing(a, p, e)
}

// backOff walks away from the mob over ground the planner accepts. With
// nowhere to go, or no distance gained by going, it hands the fight back to
// the role for a while.
func (t kiteTactic) backOff(ctx context.Context, f *Fight, st *kiteState, d float64) (bool, error) {
	l, a, p, e := f.Life, f.Actuator, f.Pose, f.Target
	if !st.backing {
		st.backing, st.backFrom, st.backTicks = true, d, 0
	}
	if st.backTicks++; st.backTicks > kiteBackMax {
		if d < st.backFrom+kiteMinGain {
			st.offUntil = l.tick + kiteGiveUp
			st.backing, st.walk = false, nil
			return false, nil
		}
		st.backFrom, st.backTicks = d, 0
	}
	if st.walk == nil || l.tick-st.walkAt >= fleeEvery {
		goal, ok := l.fleeGoal(p, e, f.Params.KiteBackoff)
		if !ok {
			st.offUntil = l.tick + kiteGiveUp
			st.backing, st.walk = false, nil
			return false, nil
		}
		st.walk = skill.NewNavigate(l.world, l.self, goal, 1)
		st.walkAt = l.tick
	}
	l.chase, st.holding = nil, false
	l.lowerShield(a)
	if status, _ := st.walk.Step(ctx, a); status == skill.Running {
		return true, nil
	}
	st.walk = nil
	return true, l.stillFacing(a, p, e)
}
