package life

import (
	"context"
	"math"
	"runtime"
	"sync"
	"weak"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	// skelAngle is how far off the bearing to the skeleton each leg of the
	// approach runs: wide enough that one leg slides the unit sideways by
	// more than an arrow's width within a draw, narrow enough that the
	// closing speed stays about three quarters of a straight walk and the
	// skeleton stays inside the shield's front arc.
	skelAngle = 40.0
	// skelLeg is how many ticks one leg lasts. At a player's walking pace
	// of about 0.2 blocks a tick that is a sideways shift past a body width
	// per leg, and the heading reverses before the track becomes a line an
	// archer can lead.
	skelLeg = 8
	// skelBreakOff is how close the dodge is given up for a straight run:
	// this near, the ticks spent weaving are ticks the archer keeps its
	// point-blank shots.
	skelBreakOff = 3.0
	// skelAdvance is how many ticks of walking follow each guarded pause. A
	// skeleton that never stops watching the unit would otherwise keep the
	// shield up for the whole approach.
	skelAdvance = 16
	// skelAim is how far off the unit a skeleton's head may point and still
	// count as drawing on it. Bedrock shows no draw state to the client, so
	// the head is the only tell there is.
	skelAim = 20.0
	// skelStall and skelProgress say the weave is going nowhere: the unit has
	// not closed by skelProgress blocks in skelStall ticks, as at a wall the
	// one-step lookahead cannot see round, so the walk is handed to the
	// pathing layer for skelYield ticks.
	skelStall    = 60
	skelProgress = 1.0
	skelYield    = 40
)

// bowSkeletons are the skeletons that shoot. A wither skeleton carries a
// sword and fights as a melee mob, so a dodge and a shield raised against
// arrows that never come would only slow the unit.
var bowSkeletons = map[string]bool{"skeleton": true, "stray": true, "bogged": true}

func init() {
	RegisterTactic(skeletonZigzag{})
	RegisterTactic(skeletonFlank{})
}

// skeletonZigzag closes on an archer along a diagonal that swaps sides every
// few ticks, raises the shield while the archer's head is on the unit, and
// lets it down to swing once adjacent. Where a side has cover from the
// archer's line it takes that side.
type skeletonZigzag struct{}

func (skeletonZigzag) Name() string           { return "skeleton/zigzag" }
func (skeletonZigzag) Family() tactics.Family { return tactics.FamilySkeleton }

func (skeletonZigzag) Engage(_ context.Context, f *Fight) (bool, error) {
	d, ok := skeletonDuel(f)
	if !ok {
		return false, nil
	}
	l, p, e, s := f.Life, f.Pose, f.Target, f.Snapshot
	_, hasShield := shieldAt(s)

	if inMeleeReach(p, e) {
		// A swing is the role fight's: it lowers the shield, jumps for the
		// critical hit and circles. Only the wait between swings is ours.
		if !hasShield || l.tick-l.lastAttack >= attackEvery || !(l.arrowIncoming(p) || skeletonAimed(e, p)) {
			return false, nil
		}
		return true, l.raiseShield(f.Actuator, p, s)
	}

	st := skelStateOf(l)
	if l.tick < st.yieldUntil {
		return false, nil
	}
	if st.target != e.ID || d < st.best-skelProgress {
		st.target, st.best, st.bestAt = e.ID, d, l.tick
	} else if l.tick-st.bestAt > skelStall {
		st.yieldUntil, st.best, st.bestAt = l.tick+skelYield, d, l.tick
		return false, nil
	}

	if hasShield && skeletonGuard(f, f.Params.MeleeShieldTick) {
		return true, l.raiseShield(f.Actuator, p, s)
	}

	angle := skelAngle
	if d <= skelBreakOff {
		angle = 0
	}
	side := 1
	if (l.tick/skelLeg)%2 == 1 {
		side = -1
	}
	yaw, ok := l.skeletonHeading(p, e, side, angle)
	if !ok {
		return false, nil
	}
	l.lowerShield(f.Actuator)
	in := l.face(p, e)
	in.Yaw, in.Forward = float32(yaw), 1
	return true, f.Actuator.Tick(in)
}

// skeletonFlank walks the approach to the flank cell wide of the leader's
// line on the archer, the squad's usual flank, but keeps the shield up while
// an arrow is closing or the archer is drawing, where the role's own walk
// goes bare until the swing. Only a Striker with a leader has a flank cell;
// every other unit gets the role fight.
type skeletonFlank struct{}

func (skeletonFlank) Name() string           { return "skeleton/flank" }
func (skeletonFlank) Family() tactics.Family { return tactics.FamilySkeleton }

func (skeletonFlank) Engage(ctx context.Context, f *Fight) (bool, error) {
	if _, ok := skeletonDuel(f); !ok {
		return false, nil
	}
	l, p, e, s := f.Life, f.Pose, f.Target, f.Snapshot
	if inMeleeReach(p, e) {
		return false, nil
	}
	if _, ok := l.flankCell(p, e); !ok {
		return false, nil
	}
	if _, ok := shieldAt(s); ok && skeletonGuard(f, f.Params.MeleeShieldTick) {
		return true, l.raiseShield(f.Actuator, p, s)
	}
	l.lowerShield(f.Actuator)
	return true, l.warriorApproach(ctx, f.Actuator, p, e)
}

// skeletonDuel says whether a skeleton tactic may take the tick, and how far
// the archer is. It declines anything the role fight owns: an order that
// keeps the unit in place or moves it off the fight, a meal, a bow in hand,
// the Vanguard's post, a mob that does not shoot, and one out of bow range or
// on another floor, where only the pathing layer knows the way.
func skeletonDuel(f *Fight) (float64, bool) {
	l := f.Life
	w := l.warrior
	if w == nil || l.mode != modeFight || w.hold || w.healWant || w.healing || w.drawing {
		return 0, false
	}
	if w.role == RoleVanguard || (w.role == RoleRanger && l.rangerArmed(f.Snapshot)) {
		return 0, false
	}
	e := f.Target
	if !bowSkeletons[name(e.Type)] || math.Abs(float64(e.Pos.Y()-f.Pose.Feet.Y())) > 1 {
		return 0, false
	}
	d := horizontal(f.Pose.Feet, e.Pos)
	return d, d <= shooterRange
}

// skeletonGuard says whether this tick is one to stand behind the shield: an
// arrow is closing, or the archer is drawing on the unit and the unit is in
// the guarded part of its cycle. hold is how many ticks each guarded pause
// lasts; the walk gets skelAdvance ticks after it.
func skeletonGuard(f *Fight, hold float64) bool {
	l, p := f.Life, f.Pose
	if l.arrowIncoming(p) {
		return true
	}
	period := ticksOf(hold) + skelAdvance
	return skeletonAimed(f.Target, p) && l.tick%period < ticksOf(hold)
}

// skeletonAimed says whether the archer's head points at the unit.
func skeletonAimed(e belief.Entity, p act.Pose) bool {
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
	return math.Abs(off) <= skelAim
}

// skeletonHeading is the yaw of this tick's step: skelAngle off the bearing to
// the archer on the leg's side, else the other side, else straight at it. A
// step is only taken on to a safe floor of the unit's own height, since a
// weave must never choose what the pathing layer would refuse. Of the
// diagonals, one out of the archer's line wins over one in it.
func (l *Life) skeletonHeading(p act.Pose, e belief.Entity, side int, angle float64) (float64, bool) {
	to := mgl32.Vec2{e.Pos.X() - p.Feet.X(), e.Pos.Z() - p.Feet.Z()}
	if to.Len() < 1e-3 {
		return 0, false
	}
	bearing := yawOf(to)
	offs := []float64{0}
	if angle != 0 {
		offs = []float64{float64(side) * angle, -float64(side) * angle, 0}
	}
	feetY := cellOf(p.Feet).Y()
	var (
		first   float64
		found   bool
		covered float64
		hidden  bool
	)
	for _, off := range offs {
		yaw := bearing + off
		rad := yaw * math.Pi / 180
		next := p.Feet.Add(mgl32.Vec3{float32(-math.Sin(rad)), 0, float32(math.Cos(rad))})
		cell, ok := cellAt(l.world, int32(math.Floor(float64(next.X()))), int32(math.Floor(float64(next.Z()))), feetY)
		if !ok || cell.Y() != feetY {
			continue
		}
		if !found {
			first, found = yaw, true
		}
		if off != 0 && !hidden && !l.bowLine(act.Pose{Feet: next}, e) {
			covered, hidden = yaw, true
		}
	}
	if hidden {
		return covered, true
	}
	return first, found
}

// skelState is what the weave remembers of one unit between ticks.
type skelState struct {
	target     uint64
	best       float64
	bestAt     uint64
	yieldUntil uint64
}

// skelUnits holds the state per unit. A tactic is shared by every unit in the
// process, so the state cannot live in it; the key is weak so a unit that is
// gone takes its entry with it.
var skelUnits = struct {
	sync.Mutex
	by map[weak.Pointer[Life]]*skelState
}{by: map[weak.Pointer[Life]]*skelState{}}

func skelStateOf(l *Life) *skelState {
	key := weak.Make(l)
	skelUnits.Lock()
	defer skelUnits.Unlock()
	if st, ok := skelUnits.by[key]; ok {
		return st
	}
	st := &skelState{}
	skelUnits.by[key] = st
	runtime.AddCleanup(l, func(k weak.Pointer[Life]) {
		skelUnits.Lock()
		delete(skelUnits.by, k)
		skelUnits.Unlock()
	}, key)
	return st
}
