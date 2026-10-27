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

// What an enderman does that no other mob does, and what this file can see of
// it. It turns hostile when a player looks at its head, and teleports away
// from a fight or towards a target, so the role's own fight (face the body,
// chase the cell it stood in) both provokes it and loses it.
//
// What belief carries of an enderman is its position and head yaw; nothing
// reads the actor flags that mark it angry. "It is after us" is therefore an
// inference, never a reading: a blow that just landed, it standing in reach,
// or its head turned on us. Each inference errs towards the role's own fight,
// so a wrong guess costs a hit the default would have had too, never a unit
// standing idle in front of a mob that is killing it. The exact Bedrock
// provocation cone is not documented, so gaze is kept well clear of the head
// instead of just outside a figure.
const (
	// endermanEyeHeight is where the head is, a little under its 2.9 tall
	// body. Gaze held far below this stays off it whatever the exact figure.
	endermanEyeHeight = 2.55
	// endermanHeadHalfWidth is half the width of its 0.6 wide body: at close
	// range the head fills a wide angle of the view, at distance a thin one.
	endermanHeadHalfWidth = 0.3
	// enderGazeMarginDeg is the angle kept between the view and the head on
	// top of what the head subtends, since the real cone is not known.
	enderGazeMarginDeg = 10.0
	// enderLookDownPitch, in degrees below the horizon, puts the view on the
	// ground a couple of blocks ahead: every head is above the horizon from
	// there, so no distance puts it near the view.
	enderLookDownPitch = 40.0
	// enderEyeHeight is how far above the feet a standing unit's eyes are.
	enderEyeHeight = 1.62

	// enderFaceDeg is how far its head may be off the unit and still count as
	// turned on it. Wide, because a head tracking a moving mark lags, and a
	// false positive only hands the tick to the role's fight.
	enderFaceDeg = 20.0
	// enderFreshHit is how long after a blow it still counts as in the fight:
	// a second, the time it takes to teleport or walk out of reach and for
	// the head to settle on a new mark.
	enderFreshHit = 20

	// enderCoverReach is how far from the unit a low-ceiling cell is looked
	// for: further and the walk there costs more time under its attack than
	// the cover is worth.
	enderCoverReach = 4
	// enderCoverEvery spaces the cover search, which scans a block of cells,
	// so a fight that finds none does not pay for it every tick.
	enderCoverEvery = 8
	// enderCoverHeadroom is the clear height of a cover cell: the 1.8 tall
	// unit fits, the 2.9 tall enderman neither enters nor teleports into it.
	enderCoverHeadroom = 2
	// enderHoldWithin bounds how far it may stand while the unit waits in
	// cover for it; further and it is not coming, so the role's fight goes
	// after it.
	enderHoldWithin = 8.0
	// enderJump is how far from the cell the walk was planned for it may be
	// before it is taken to have teleported: replan now, not at chaseEvery.
	enderJump = 3.0
)

func init() {
	RegisterTactic(enderLowground{})
	RegisterTactic(enderIgnore{})
}

// enderLowground fights an angry enderman from where it cannot use its height
// and teleport: under a low ceiling it must walk to, approached with the
// unit's gaze off its head.
type enderLowground struct{}

func (enderLowground) Name() string           { return "enderman/lowground" }
func (enderLowground) Family() tactics.Family { return tactics.FamilyEnderman }

// Engage walks to a nearby low-ceiling cell and waits there, or closes in
// with the gaze down, replanning the moment it teleports. Within reach, or
// whenever the unit has other business, it hands the tick to the role.
func (enderLowground) Engage(ctx context.Context, f *Fight) (bool, error) {
	if !enderMayAct(f) {
		return false, nil
	}
	l, p, e := f.Life, f.Pose, f.Target
	if inMeleeReach(p, e) {
		l.chase = nil
		return false, nil
	}
	a := enderGaze{Actuator: f.Actuator, e: e}
	hold := math.Min(enderHoldWithin, f.Params.FocusRange)
	cell := cellOf(e.Pos)

	if enderLowCeiling(l.world, cellOf(p.Feet)) {
		l.chase = nil
		if horizontal(p.Feet, e.Pos) > hold {
			return false, nil
		}
		return true, a.Tick(enderLookDown(p, e))
	}

	walking := l.chase != nil && l.chaseGoal != cell && enderLowCeiling(l.world, l.chaseGoal)
	if !walking && l.tick%enderCoverEvery == 0 {
		if goal, ok := enderCover(f, hold); ok {
			l.chase = skill.NewNavigate(l.world, l.self, goal, 0.5)
			l.chaseGoal, l.chaseAt = goal, l.tick
			walking = true
		}
	}

	if !walking && (l.chase == nil || enderTeleported(l.chaseGoal, e) ||
		(l.tick-l.chaseAt >= chaseEvery && l.chaseGoal != cell)) {
		goal, ok := l.standNear(cell)
		if !ok {
			return false, nil
		}
		l.chase = skill.NewNavigate(l.world, l.self, goal, chaseWithin)
		l.chaseGoal, l.chaseAt = cell, l.tick
	}

	l.lowerShield(f.Actuator)
	switch st, _ := l.chase.Step(ctx, a); st {
	case skill.Running:
		return true, nil
	case skill.Failed:
		// The role's approach gives the mob up with its own reason.
		l.chase = nil
		return false, nil
	}
	return true, a.Tick(enderLookDown(p, e))
}

// enderIgnore leaves an enderman alone once it has stopped coming for the
// unit. It only ever sees one that hurt the unit lately, and a chase after
// one that has since calmed down is what turns it hostile again.
type enderIgnore struct{}

func (enderIgnore) Name() string           { return "enderman/ignore" }
func (enderIgnore) Family() tactics.Family { return tactics.FamilyEnderman }

// Engage hands the tick on while the enderman is after the unit. Otherwise it
// forgets the grudge, so the squad's focus drops it next tick, and looks at
// the ground for the one tick it holds.
func (enderIgnore) Engage(_ context.Context, f *Fight) (bool, error) {
	if !enderMayAct(f) || enderTargetsUs(f) {
		return false, nil
	}
	f.Life.attackerUntil = f.Life.tick
	return true, f.Actuator.Tick(act.Input{Yaw: f.Pose.Yaw, Pitch: enderLookDownPitch})
}

// enderMayAct says whether this tick is one an enderman tactic may take at
// all. Orders, the meal, the shield and the role's own job outrank it, and so
// does any other mob on the unit: those are the role's to answer.
func enderMayAct(f *Fight) bool {
	l := f.Life
	w := l.warrior
	if w == nil || l.mode != modeFight || w.healWant || w.hold || w.drawing || w.boss.active ||
		w.role == RoleRanger {
		return false
	}
	switch w.order().Kind {
	case OrderRegroup, OrderRetreat:
		return false
	}
	if l.arrowIncoming(f.Pose) {
		return false
	}
	return !enderOthersNear(f)
}

// enderOthersNear says whether a mob other than the target is within swing
// range of the unit.
func enderOthersNear(f *Fight) bool {
	l := f.Life
	found := false
	l.ents.Each(func(o belief.Entity) {
		if found || o.Player || o.ID == f.Target.ID {
			return
		}
		k := kindOf(o.Type)
		if k == other || (k == provoked && !l.isSuspect(o.ID)) {
			return
		}
		if math.Abs(float64(o.Pos.Y()-f.Pose.Feet.Y())) > threatHeight {
			return
		}
		found = horizontal(f.Pose.Feet, o.Pos) <= meleeReach
	})
	return found
}

// enderTargetsUs infers that the target is still after the unit: it is in
// reach, it landed a blow a moment ago, or its head is on the unit.
func enderTargetsUs(f *Fight) bool {
	l, p, e := f.Life, f.Pose, f.Target
	if inMeleeReach(p, e) {
		return true
	}
	if l.attackerUntil >= attackerTicks && l.tick-(l.attackerUntil-attackerTicks) < enderFreshHit {
		return true
	}
	bearing, _ := lookAt(e.Pos, p.Feet)
	return math.Abs(enderAngleDiff(float64(e.HeadYaw), float64(bearing))) <= enderFaceDeg
}

// enderAngleDiff is the signed difference of two headings in degrees, in
// [-180, 180].
func enderAngleDiff(a, b float64) float64 {
	d := math.Mod(a-b, 360)
	switch {
	case d > 180:
		d -= 360
	case d < -180:
		d += 360
	}
	return d
}

// enderGazeOnHead says whether a view from eye along yaw and pitch is on or
// near the enderman's head.
func enderGazeOnHead(eye mgl32.Vec3, yaw, pitch float32, e belief.Entity) bool {
	to := e.Pos.Add(mgl32.Vec3{0, endermanEyeHeight, 0}).Sub(eye)
	d := float64(to.Len())
	if d < 1e-3 {
		return true
	}
	yr, pr := float64(yaw)*math.Pi/180, float64(pitch)*math.Pi/180
	view := mgl32.Vec3{
		float32(-math.Sin(yr) * math.Cos(pr)),
		float32(-math.Sin(pr)),
		float32(math.Cos(yr) * math.Cos(pr)),
	}
	cos := math.Max(-1, math.Min(1, float64(view.Dot(to))/d))
	limit := enderGazeMarginDeg + math.Atan(endermanHeadHalfWidth/d)*180/math.Pi
	return math.Acos(cos)*180/math.Pi < limit
}

// enderGaze is the actuator a walk to an enderman is stepped with: a tick
// whose view would land on its head has the view lowered, everything else
// passes through. Navigation does not know the mob is one to look away from.
type enderGaze struct {
	skill.Actuator
	e belief.Entity
}

func (g enderGaze) Tick(in act.Input) error {
	eye := g.Pose().Feet.Add(mgl32.Vec3{0, enderEyeHeight, 0})
	if enderGazeOnHead(eye, in.Yaw, in.Pitch, g.e) {
		in.Pitch = enderLookDownPitch
	}
	return g.Actuator.Tick(in)
}

// enderLookDown is a tick facing the enderman's way with the view on the
// ground.
func enderLookDown(p act.Pose, e belief.Entity) act.Input {
	yaw, _ := lookAt(p.Feet.Add(mgl32.Vec3{0, enderEyeHeight, 0}), e.Pos)
	return act.Input{Yaw: yaw, Pitch: enderLookDownPitch}
}

// enderLowCeiling says whether a unit standing in the cell has a solid ceiling
// just above its head. Liquid and hurting blocks are no ceiling.
func enderLowCeiling(w path.Blocks, feet protocol.BlockPos) bool {
	if !safeStand(w, feet) {
		return false
	}
	n, known := w.Block(protocol.BlockPos{feet.X(), feet.Y() + enderCoverHeadroom, feet.Z()})
	return known && !path.Passable(n) && safeFloor(n)
}

// enderTeleported says whether the enderman stands too far from the cell the
// current walk was planned for to be the mob that walk was planned against.
func enderTeleported(goal protocol.BlockPos, e belief.Entity) bool {
	at := mgl32.Vec3{float32(goal.X()) + 0.5, e.Pos.Y(), float32(goal.Z()) + 0.5}
	return horizontal(at, e.Pos) > enderJump
}

// enderCover is the nearest low-ceiling cell within reach of the unit that is
// still on the leader's side of the fight and not so far from the enderman
// that it will never walk to it.
func enderCover(f *Fight, hold float64) (protocol.BlockPos, bool) {
	l, p, e := f.Life, f.Pose, f.Target
	anchor, leashed := l.warrior.leader(p)
	here := cellOf(p.Feet)
	var (
		best  protocol.BlockPos
		bestD = math.MaxFloat64
	)
	for dx := int32(-enderCoverReach); dx <= enderCoverReach; dx++ {
		for dz := int32(-enderCoverReach); dz <= enderCoverReach; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			for _, dy := range [...]int32{0, 1, -1} {
				c := protocol.BlockPos{here.X() + dx, here.Y() + dy, here.Z() + dz}
				if !enderLowCeiling(l.world, c) {
					continue
				}
				centre := mgl32.Vec3{float32(c.X()) + 0.5, float32(c.Y()), float32(c.Z()) + 0.5}
				d := horizontal(p.Feet, centre)
				if d > enderCoverReach || d >= bestD {
					break
				}
				if horizontal(centre, e.Pos) > hold || (leashed && horizontal(centre, anchor) > f.Params.FocusRange) {
					break
				}
				best, bestD = c, d
				break
			}
		}
	}
	return best, bestD < math.MaxFloat64
}
