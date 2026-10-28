package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// witch/rush closes on a witch through zigzag legs and kills it on the
// fullest cadence. The witch throws splash potions that poison, slow and
// weaken while it fights, and it drinks to heal when hurt, so a fight that
// walks the straight line drinks a potion on the way in and a fight that
// cannot finish feeds the witch's next one. The tactic is hands off wherever
// it has no better idea: a hold order, a retreat or regroup mode, ground the
// pathing layer refuses and a witch beyond a walking band all leave the
// role's own fight in charge.

// The two numbers this tactic adds. witchZigTicks makes a leg a few ticks
// long, well under the flight time of a splash potion crossing the band a
// witch throws from, so by the time a throw arrives the unit no longer
// holds the heading it was thrown at; witchZigYaw is the width of each leg
// off the direct line, wide enough to stand outside where a splash lands
// and shallow enough that the legs still close the range.
const (
	witchZigTicks = 6
	witchZigYaw   = math.Pi / 4
	// witchStepAhead is how far along a leg the march is checked before it
	// is walked.
	witchStepAhead = 1.25
)

type witchRush struct{}

func init() { RegisterTactic(&witchRush{}) }

func (*witchRush) Name() string           { return "witch/rush" }
func (*witchRush) Family() tactics.Family { return tactics.FamilyWitch }

func (t *witchRush) Engage(_ context.Context, f *Fight) (bool, error) {
	l := f.Life
	// The hold order and the modes the shared decisions have already taken
	// outrank the rush; the role's own fight carries them, and so does a
	// meal the unit wants or has started.
	if w := l.warrior; w == nil || l.mode != modeFight || w.hold || w.healWant || w.healing {
		return false, nil
	}
	p := f.Pose
	e := f.Target
	// A witch more than a floor's band above or below cannot be closed on
	// by marching: leave that to the role's own approach, which plans for
	// it.
	if math.Abs(float64(e.Pos.Y()-p.Feet.Y())) > threatHeight {
		return false, nil
	}
	if inMeleeReach(p, e) {
		return true, t.swing(f)
	}
	to := mgl32.Vec2{e.Pos.X() - p.Feet.X(), e.Pos.Z() - p.Feet.Z()}
	if to.Len() < 1e-3 {
		l.cancelDraw(f.Actuator)
		return true, l.stillFacing(f.Actuator, p, e)
	}
	direct := to.Normalize()
	leg := witchLeg(direct, witchSideOf(l.tick))
	switch {
	case witchStepClears(f, leg):
		l.cancelDraw(f.Actuator)
		l.lowerShield(f.Actuator)
		return true, f.Actuator.Tick(act.Input{Yaw: float32(yawOf(leg)), Forward: 1})
	case witchStepClears(f, direct):
		// The leg's lane is blocked: close straight for this leg's span,
		// and the next leg takes the other lane.
		l.cancelDraw(f.Actuator)
		l.lowerShield(f.Actuator)
		return true, f.Actuator.Tick(act.Input{Yaw: float32(yawOf(direct)), Forward: 1})
	}
	// No step forward on ground the raw march may cross: the role's own
	// approach walks it with the pathing layer.
	return false, nil
}

// swing keeps the hand free and hits on the cadence. No shield: raising one
// pushes the swings back, and the fight is a race to kill the witch before
// it drinks the hurt away.
func (t *witchRush) swing(f *Fight) error {
	l := f.Life
	l.cancelDraw(f.Actuator)
	l.lowerShield(f.Actuator)
	return l.strike(f.Actuator, f.Pose, f.Target, f.Snapshot)
}

// witchSideOf is which way the current leg veers, off the unit's own clock,
// so legs swap on their own and two units fighting the same witch do not
// hold the same lane.
func witchSideOf(tick uint64) int8 {
	if (tick/witchZigTicks)%2 == 0 {
		return 1
	}
	return -1
}

// witchLeg turns the direct heading to the witch by witchZigYaw to one side.
func witchLeg(direct mgl32.Vec2, side int8) mgl32.Vec2 {
	c := float32(math.Cos(witchZigYaw))
	s := float32(math.Sin(witchZigYaw))
	if side < 0 {
		s = -s
	}
	return mgl32.Vec2{direct.X()*c - direct.Y()*s, direct.X()*s + direct.Y()*c}
}

// witchStepClears asks whether one step of the raw march — which bypasses
// the pathfinding skill — stays on ground that layer would have walked: every
// cell the step crosses, on the level of the feet or one step down, must sit
// on a safe floor, so water, lava and ledges are never entered, however
// narrow. The sweep runs a stride and a bit past the feet so it sees the
// cell the leg enters, not the one the unit is still inside.
func witchStepClears(f *Fight, dir mgl32.Vec2) bool {
	return sweepClear(f.Life.world, f.Pose.Feet, dir, witchStepAhead)
}
