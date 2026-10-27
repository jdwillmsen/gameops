package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	// spiderClimbLift is how far above the unit's own feet a spider counts as
	// off the floor. Half a block clears a slab step and a walking spider's
	// bob without calling either a climb.
	spiderClimbLift = 0.5
	// spiderStepAhead is the one-cell lookahead both spider walks check: a
	// step onto ground the planner cannot stand on is refused before it is
	// taken, which keeps a standoff out of lava, the void and water.
	spiderStepAhead = 1.5
)

// spiderGround pulls a wall-climbing spider down where it can be fought. A
// spider on a wall can drop onto a unit that stands under it, so the tactic
// gives ground to a spot the spider must leave the wall to reach, then swings
// as it drops or leaps into range.
type spiderGround struct{}

func (spiderGround) Name() string { return "spider/ground" }

func (spiderGround) Family() tactics.Family { return tactics.FamilySpider }

func (spiderGround) Engage(_ context.Context, f *Fight) (bool, error) {
	if !spiderTacticAllowed(f) || name(f.Target.Type) != "spider" {
		return false, nil
	}
	if float64(f.Target.Pos.Y()-f.Pose.Feet.Y()) < spiderClimbLift {
		// On the floor the role's own melee closes better than a standoff.
		return false, nil
	}
	if inMeleeReach(f.Pose, f.Target) {
		// Dropping or mid-leap: this is the hit the standoff waited for.
		f.Life.lowerShield(f.Actuator)
		return true, f.Life.strike(f.Actuator, f.Pose, f.Target, f.Snapshot)
	}
	if horizontal(f.Pose.Feet, f.Target.Pos) >= f.Params.KiteBackoff ||
		!f.Life.spiderCanBackOff(f.Pose, f.Target) {
		// Already at the standoff, or no safe cell behind: make it come down
		// to this spot.
		return true, f.Life.stillFacing(f.Actuator, f.Pose, f.Target)
	}
	yaw, pitch := lookAt(f.Pose.Feet.Add(mgl32.Vec3{0, 1.62, 0}), f.Target.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}))
	return true, f.Actuator.Tick(act.Input{Yaw: yaw, Pitch: pitch, Forward: -1})
}

// spiderCanBackOff says whether the cell a step away from the spider is
// ground the unit may stand on. The ground tactic opens distance only over
// floor the pathing layer accepts.
func (l *Life) spiderCanBackOff(p act.Pose, e belief.Entity) bool {
	dir := away(p, e)
	x := int32(math.Floor(float64(p.Feet.X() + dir.X()*spiderStepAhead)))
	z := int32(math.Floor(float64(p.Feet.Z() + dir.Y()*spiderStepAhead)))
	_, ok := cellAt(l.world, x, z, cellOf(p.Feet).Y())
	return ok
}

func init() { RegisterTactic(spiderGround{}) }
