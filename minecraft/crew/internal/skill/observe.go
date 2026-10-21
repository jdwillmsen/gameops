package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// observeWithin is how far the unit may be from the box it reads, in chunks:
// inside the server's view distance the box's sub-chunks have been streamed to
// the unit, so their cells read back instead of unknown. The view distance is
// set per deployment (MC_VIEW_DISTANCE), so this is a floor rather than
// following it: any setting of 4 or more streams the whole box, and a box up
// to about 128 cells long still reads whole from beside its middle.
const observeWithin = 4

// observeRange is that distance in blocks.
const observeRange = observeWithin * 16

// Observe walks near a box and reports every cell of it from the unit's own
// world view.
type Observe struct {
	world ChestWorld
	self  func() belief.Snapshot
	min   protocol.BlockPos
	max   protocol.BlockPos

	// candidates are the cells the box may be read from, in the order they
	// are tried, and at is how far through them the skill is.
	candidates []protocol.BlockPos
	at         int
	walk       *navigator
	// tooWide says a candidate could be stood in, but the box was further
	// from it than the server's view distance reaches.
	tooWide bool
	// unreachable is the last planner failure, kept to explain giving up.
	unreachable error

	blocks    Blocks
	hasBlocks bool
}

// NewObserve reads the inclusive box once the unit stands close enough for the
// server to have streamed it.
func NewObserve(world ChestWorld, self func() belief.Snapshot, min, max protocol.BlockPos) Skill {
	return &Observe{world: world, self: self, min: min, max: max}
}

func (o *Observe) Name() string { return "observe_blocks" }

// Blocks is the observation, once the box has been read.
func (o *Observe) Blocks() (Blocks, bool) { return o.blocks, o.hasBlocks }

func (o *Observe) Ready(s belief.Snapshot) error { return overworld(s) }

// Resume plans the walk again from wherever the unit now stands. A fight can
// set the walk back, but nothing has been read until the whole box has.
func (o *Observe) Resume() {
	if o.walk != nil {
		o.walk.resume()
	}
}

func (o *Observe) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(o.self()); err != nil {
		return Failed, err
	}
	if !o.inRange(a.Pose().Feet) {
		st, err := o.walkTo(a)
		switch {
		case st == Running:
			return Running, nil
		case st == Failed:
			return Failed, err
		}
	}
	b, err := observeBlocks(o.world, o.min, o.max)
	if err != nil {
		return Failed, err
	}
	o.blocks, o.hasBlocks = b, true
	return Done, nil
}

// inRange reports whether the whole box is within the server's view distance
// of where the unit stands, which is as near as it has to be to read it.
func (o *Observe) inRange(feet mgl32.Vec3) bool {
	return farthestCorner(feet, o.min, o.max) <= observeRange
}

// walkTo walks the unit to a cell it can read the whole box from. The cell is
// chosen when the walk starts, and again when the planner cannot reach the one
// it chose, because a box the unit cannot get close to is still a box it may
// read from further away.
//
// At most one candidate is planned per step: a box ringed by unreachable cells
// would otherwise run hundreds of path searches inside a single tick.
func (o *Observe) walkTo(a Actuator) (Status, error) {
	if o.walk == nil {
		c, reach, ok := o.vantage()
		if !ok {
			switch {
			case o.tooWide:
				return Failed, errors.New("the box is too big to read whole")
			case o.unreachable != nil:
				return Failed, fmt.Errorf("cannot reach a spot to read the box from: %w", o.unreachable)
			default:
				return Failed, errors.New("no standing spot near the box to read it from")
			}
		}
		o.walk = newNavigator(o.world, c, reach)
	}
	st, err := o.walk.step(a)
	switch {
	case st != Failed:
		return st, err
	case !errors.Is(err, path.ErrNoPath):
		return Failed, fmt.Errorf("walking to the box: %w", err)
	}
	o.unreachable, o.walk = err, nil
	return Running, nil
}

// vantage picks the next cell to read the box from: the first candidate the
// unit can stand on and from which the whole box is within the server's view
// distance. The reach is how near the unit must come to it for the box to be
// in range from where it stands.
func (o *Observe) vantage() (protocol.BlockPos, float64, bool) {
	if o.candidates == nil {
		o.candidates = observeVantages(o.min, o.max)
	}
	for o.at < len(o.candidates) {
		c := o.candidates[o.at]
		o.at++
		y, ok := path.Footing(o.world, c)
		if !ok {
			continue
		}
		reach, ok := observeReach(mgl32.Vec3{float32(c.X()) + 0.5, y, float32(c.Z()) + 0.5}, o.min, o.max)
		if !ok {
			o.tooWide = true
			continue
		}
		return c, float64(reach), true
	}
	return protocol.BlockPos{}, 0, false
}

// observeVantages lists the cells a unit may read a box from, in the order
// they are tried: the box's own centre, the cells beside it, the ring of
// cells around its footprint and the cells above its top face.
func observeVantages(min, max protocol.BlockPos) []protocol.BlockPos {
	cx, cy, cz := (min.X()+max.X())/2, (min.Y()+max.Y())/2, (min.Z()+max.Z())/2
	out := []protocol.BlockPos{{cx, cy, cz}}
	for _, d := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		out = append(out, protocol.BlockPos{cx + d[0], cy, cz + d[1]})
	}
	out = append(out, protocol.BlockPos{cx, cy + 1, cz}, protocol.BlockPos{cx, cy - 1, cz})
	for _, y := range []int32{min.Y(), cy, max.Y(), max.Y() + 1} {
		for x := min.X(); x <= max.X(); x++ {
			out = append(out, protocol.BlockPos{x, y, min.Z() - 1}, protocol.BlockPos{x, y, max.Z() + 1})
		}
		for z := min.Z(); z <= max.Z(); z++ {
			out = append(out, protocol.BlockPos{min.X() - 1, y, z}, protocol.BlockPos{max.X() + 1, y, z})
		}
	}
	for x := min.X(); x <= max.X(); x++ {
		for z := min.Z(); z <= max.Z(); z++ {
			out = append(out, protocol.BlockPos{x, max.Y() + 1, z})
		}
	}
	return out
}

// observeReach is how far the unit may be from a cell and still have the
// whole box within the server's view distance, and whether the box is that
// close at all: a box wider than the view distance cannot be read whole.
func observeReach(from mgl32.Vec3, min, max protocol.BlockPos) (float32, bool) {
	far := farthestCorner(from, min, max)
	if far > observeRange {
		return 0, false
	}
	return observeRange - far, true
}

// farthestCorner is the distance from a point to the corner of the box that
// is furthest from it.
func farthestCorner(from mgl32.Vec3, min, max protocol.BlockPos) float32 {
	far := float32(0)
	for _, x := range []int32{min.X(), max.X() + 1} {
		for _, y := range []int32{min.Y(), max.Y() + 1} {
			for _, z := range []int32{min.Z(), max.Z() + 1} {
				if d := (mgl32.Vec3{float32(x), float32(y), float32(z)}).Sub(from).Len(); d > far {
					far = d
				}
			}
		}
	}
	return far
}
