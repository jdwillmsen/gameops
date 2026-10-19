package skill

import (
	"context"
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// off the planned segment by this much means the server moved the unit.
	offCourse = 0.5
	// close enough to a waypoint to aim at the next.
	waypointReach = 0.5
	// A step-up must be started this far from the step's centre: the spike's
	// takeoffs landed cleanly from there, and a takeoff after the wall has
	// already stopped the unit was corrected.
	jumpReach = 1.5
	// Braking this far from the last waypoint leaves the unit's leftover
	// momentum to carry it onto the waypoint.
	brakeDistance = 0.4
	// A level mismatch tolerated at a waypoint, in blocks.
	levelSlack  = 0.3
	maxNoGain   = 3
	gainEpsilon = 0.25
)

// navigator walks a planned path. Mine and deposit embed one to get to their
// work.
type navigator struct {
	world  path.Blocks
	goal   protocol.BlockPos
	within float64

	plan      []protocol.BlockPos
	idx       int
	segStart  mgl32.Vec2
	onWaypt   int
	replans   int
	lastDist  float64
	noGain    int
	planCount int
}

func newNavigator(world path.Blocks, goal protocol.BlockPos, within float64) *navigator {
	return &navigator{world: world, goal: goal, within: within}
}

func (n *navigator) distance(p act.Pose) float64 {
	return float64(p.Feet.Sub(mgl32.Vec3{float32(n.goal.X()) + 0.5, float32(n.goal.Y()), float32(n.goal.Z()) + 0.5}).Len())
}

func (n *navigator) step(a Actuator) (Status, error) {
	p := a.Pose()
	if n.distance(p) <= n.within {
		return Done, nil
	}
	if n.plan == nil || n.onWaypt >= waitTicks || (p.OnGround && n.straying(p)) {
		if err := n.replan(p); err != nil {
			return Failed, err
		}
	}

	wp := n.plan[n.idx]
	c := centre(wp)
	dx, dz := float64(c.X()-p.Feet.X()), float64(c.Z()-p.Feet.Z())
	dist := math.Hypot(dx, dz)
	last := n.idx == len(n.plan)-1

	if !last && dist <= waypointReach {
		if math.Abs(float64(p.Feet.Y())-float64(wp.Y())) > levelSlack {
			if p.OnGround {
				// Landed on another level than planned.
				n.plan = nil
				return n.tick(a, act.Input{Yaw: p.Yaw})
			}
		} else {
			n.segStart = mgl32.Vec2{c.X(), c.Z()}
			n.idx++
			n.onWaypt = 0
			wp = n.plan[n.idx]
			c = centre(wp)
			dx, dz = float64(c.X()-p.Feet.X()), float64(c.Z()-p.Feet.Z())
			dist = math.Hypot(dx, dz)
			last = n.idx == len(n.plan)-1
		}
	}

	in := act.Input{Forward: 1, Yaw: p.Yaw}
	if dist > 1e-3 {
		in.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	}
	if last && dist < brakeDistance {
		in.Forward = 0
	}
	in.Jump = p.OnGround && wp.Y() > cellOf(p.Feet).Y() && dist <= jumpReach
	n.onWaypt++
	return n.tick(a, in)
}

// tickError marks a failure to send an input, as opposed to a failure to plan.
// The first ends the skill; the second may only rule out one target.
type tickError struct{ err error }

func (e tickError) Error() string { return e.err.Error() }
func (e tickError) Unwrap() error { return e.err }

func (n *navigator) tick(a Actuator, in act.Input) (Status, error) {
	if err := a.Tick(in); err != nil {
		return Failed, tickError{err}
	}
	return Running, nil
}

// straying reports whether the unit is off the line it was planned along, as
// after a server correction.
func (n *navigator) straying(p act.Pose) bool {
	c := centre(n.plan[n.idx])
	a, b := n.segStart, mgl32.Vec2{c.X(), c.Z()}
	pt := mgl32.Vec2{p.Feet.X(), p.Feet.Z()}
	ab := b.Sub(a)
	t := float32(0)
	if l := ab.Dot(ab); l > 0 {
		t = pt.Sub(a).Dot(ab) / l
		t = float32(math.Max(0, math.Min(1, float64(t))))
	}
	return pt.Sub(a.Add(ab.Mul(t))).Len() > offCourse
}

// resume drops the plan, which the unit has left; the next step plans afresh
// and the lost-progress count starts over. The next replan also sets a new
// baseline, so the knockback that ended the fight is not counted as lost
// progress.
func (n *navigator) resume() { n.plan, n.noGain, n.planCount = nil, 0, 0 }

// replan plans from the unit's cell. A replan that leaves the unit no nearer
// the goal than the last one counts against it, so a unit the server keeps
// pushing back gives up instead of walking forever.
func (n *navigator) replan(p act.Pose) error {
	d := n.distance(p)
	if n.planCount > 0 {
		n.replans++
		if d >= n.lastDist-gainEpsilon {
			n.noGain++
		} else {
			n.noGain = 0
		}
		if n.noGain >= maxNoGain {
			return fmt.Errorf("no progress after %d replans", maxNoGain)
		}
	}
	n.lastDist = d
	n.planCount++

	plan, err := path.Find(n.world, cellOf(p.Feet), n.goal, 0)
	if err != nil {
		return fmt.Errorf("walking to the goal: %w", err)
	}
	if len(plan) == 0 {
		plan = []protocol.BlockPos{n.goal}
	}
	n.plan, n.idx, n.onWaypt = plan, 0, 0
	n.segStart = mgl32.Vec2{p.Feet.X(), p.Feet.Z()}
	return nil
}

// Navigate walks to within a distance of a goal cell.
type Navigate struct {
	self func() belief.Snapshot
	nav  *navigator
}

// NewNavigate is done when the feet are within the given distance of the goal
// cell's floor centre.
func NewNavigate(world path.Blocks, self func() belief.Snapshot, goal protocol.BlockPos, within float64) Skill {
	return &Navigate{self: self, nav: newNavigator(world, goal, within)}
}

func (n *Navigate) Name() string { return "navigate" }

// Resume plans again from wherever the unit now stands.
func (n *Navigate) Resume() { n.nav.resume() }

func (n *Navigate) Ready(s belief.Snapshot) error { return overworld(s) }

func (n *Navigate) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(n.self()); err != nil {
		return Failed, err
	}
	return n.nav.step(a)
}
