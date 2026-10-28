package life

import (
	"context"
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
	// spiderStepAhead is the lookahead both spider walks sweep: a step over
	// or onto ground the planner cannot stand on is refused before it is
	// taken, which keeps a standoff out of lava, the void and water.
	spiderStepAhead = 1.5
	// spiderWallCap is how many ticks the standoff may last against a spider
	// that stays on the wall. A spider only drops when it has a reason, and
	// one hanging out of reach gives the unit none, so an uncapped standoff
	// lets it hold the fight forever while its allies are fought alone.
	spiderWallCap = 200
	// spiderWallCooldown is how long the role's own fight keeps the spider
	// after the cap, long enough for its chase to change the geometry before
	// the standoff is tried again.
	spiderWallCooldown = 100
	// spiderWallGap is the longest break in wall ticks that still counts as
	// one standoff: a meal or a shared order interrupts the tactic without
	// the spider having come down.
	spiderWallGap = 20
)

// spiderGroundState is what one unit remembers of its standoff.
type spiderGroundState struct {
	target uint64
	// since is when this wall standoff began and last when it was last held,
	// 0 for none.
	since, last uint64
	// coolUntil is when the standoff may be tried again after the cap.
	coolUntil uint64
}

var spiderGroundStates unitScratch[spiderGroundState]

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
	st := spiderGroundStates.of(f.Life)
	if float64(f.Target.Pos.Y()-f.Pose.Feet.Y()) < spiderClimbLift {
		// On the floor the role's own melee closes better than a standoff.
		st.since = 0
		return false, nil
	}
	if now := f.Life.tick; now < st.coolUntil {
		return false, nil
	} else if st.since == 0 || st.target != f.Target.ID || now-st.last > spiderWallGap {
		st.target, st.since = f.Target.ID, now
	} else if now-st.since >= spiderWallCap {
		st.coolUntil, st.since = now+spiderWallCooldown, 0
		return false, nil
	}
	st.last = f.Life.tick
	// The standoff is melee by design, so a half-drawn bow is let go.
	f.Life.cancelDraw(f.Actuator)
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

// spiderCanBackOff says whether the step away from the spider stays on
// ground the unit may stand on. The ground tactic opens distance only over
// floor the pathing layer accepts, every cell of the way.
func (l *Life) spiderCanBackOff(p act.Pose, e belief.Entity) bool {
	return sweepClear(l.world, p.Feet, away(p, e), spiderStepAhead)
}

func init() { RegisterTactic(spiderGround{}) }
