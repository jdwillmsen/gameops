package path

import (
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

// The planner walks the same shapes the actuator collides with, so a half
// block is a walk, a fence a wall and a block and a half a jump too far.
const (
	// stepHeight is what a grounded unit rises onto without jumping.
	stepHeight = 0.6
	// jumpRise is what a jump clears; the real jump reaches higher, and the
	// margin keeps the plan off the edge of it.
	jumpRise = float32(maxStepUp)
	bodyH    = 1.8
	halfW    = 0.3
	// slack keeps boxes that merely touch from counting as overlapping.
	slack = 1e-3
	// samples per cell on a move. An eighth of a block is finer than any
	// shape's features, so every ledge on the way shows up as a step.
	samples = 8
)

// States is what a Blocks may also offer: a block's states, which decide its
// shape. Without it every block has its default shape.
type States interface {
	State(protocol.BlockPos) (name string, props map[string]any, known bool)
}

// Collider is the collision view of a block store. It is what the actuator
// predicts the unit's movement against.
type Collider struct{ b Blocks }

// NewSolid adapts a block store to the collision queries the actuator makes.
// Unknown blocks are solid cubes, matching the planner.
func NewSolid(b Blocks) Collider { return Collider{b} }

// Shape returns the collision boxes of the block at p.
func (c Collider) Shape(p protocol.BlockPos) []shape.Box {
	boxes, _ := shapeAt(c.b, p)
	return boxes
}

// Solid reports whether the block has any collision.
func (c Collider) Solid(p protocol.BlockPos) bool { return len(c.Shape(p)) > 0 }

// Climbable reports whether the unit climbs the block at p.
func (c Collider) Climbable(p protocol.BlockPos) bool {
	name, known := c.b.Block(p)
	return known && shape.Climbable(name)
}

func shapeAt(b Blocks, p protocol.BlockPos) (boxes []shape.Box, known bool) {
	var name string
	var props map[string]any
	if s, ok := b.(States); ok {
		name, props, known = s.State(p)
	} else {
		name, known = b.Block(p)
	}
	if !known {
		return []shape.Box{shape.Full()}, false
	}
	return shape.Boxes(name, props), true
}

// box is a world-space collision box.
type box struct{ lo, hi [3]float32 }

type cell struct {
	boxes []box
	known bool
}

// view reads cells once each; a search touches the same ones many times. It
// belongs to a single search and is not safe for concurrent use.
type view struct {
	b    Blocks
	memo map[protocol.BlockPos]cell
}

func newView(b Blocks) *view { return &view{b: b, memo: map[protocol.BlockPos]cell{}} }

func (v *view) cell(p protocol.BlockPos) cell {
	if c, ok := v.memo[p]; ok {
		return c
	}
	boxes, known := shapeAt(v.b, p)
	c := cell{known: known}
	o := [3]float32{float32(p.X()), float32(p.Y()), float32(p.Z())}
	for _, s := range boxes {
		var w box
		for a := 0; a < 3; a++ {
			w.lo[a], w.hi[a] = o[a]+s.Min[a], o[a]+s.Max[a]
		}
		c.boxes = append(c.boxes, w)
	}
	v.memo[p] = c
	return c
}

// under returns the boxes touching the unit's footprint at (x, z) in the
// cells from y0 to y1.
func (v *view) under(x, z float32, y0, y1 int32) []box {
	var out []box
	for cx := floor32(x - halfW + slack); cx <= floor32(x+halfW-slack); cx++ {
		for cz := floor32(z - halfW + slack); cz <= floor32(z+halfW-slack); cz++ {
			for cy := y0; cy <= y1; cy++ {
				for _, b := range v.cell(protocol.BlockPos{cx, cy, cz}).boxes {
					if b.lo[0] < x+halfW-slack && b.hi[0] > x-halfW+slack &&
						b.lo[2] < z+halfW-slack && b.hi[2] > z-halfW+slack {
						out = append(out, b)
					}
				}
			}
		}
	}
	return out
}

func floor32(f float32) int32 { return int32(math.Floor(float64(f))) }

// clear reports whether the body standing at feet height y over (x, z) fits:
// nothing but its footing reaches into the space above y. Reaching up to
// top lets a caller ask for the room a falling or jumping unit needs.
func (v *view) clear(x, z, y, top float32) bool {
	for _, b := range v.under(x, z, floor32(y)-1, floor32(top)) {
		if b.hi[1] > y+slack && b.lo[1] < top-slack {
			return false
		}
	}
	return true
}

// ground is the height of the highest surface under the footprint that is no
// more than rise above y, and no more than maxDrop below it.
func (v *view) ground(x, z, y, rise float32) (float32, bool) {
	best, found := float32(0), false
	for _, b := range v.under(x, z, floor32(y)-maxDrop-1, floor32(y+rise)) {
		if b.hi[1] <= y+rise+slack && (!found || b.hi[1] > best) {
			best, found = b.hi[1], true
		}
	}
	return best, found && best >= y-maxDrop-slack
}

// footing is the height of the surface the unit stands on in the feet cell c,
// measured at the cell's centre.
func (v *view) footing(c protocol.BlockPos) (float32, bool) {
	x, z := float32(c.X())+0.5, float32(c.Z())+0.5
	y := float32(c.Y())
	if !v.cell(c).known || !v.cell(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()}).known ||
		!v.cell(protocol.BlockPos{c.X(), c.Y() + 1, c.Z()}).known {
		return 0, false
	}
	best, found := float32(0), false
	for _, b := range v.under(x, z, c.Y()-1, c.Y()) {
		if t := b.hi[1]; t >= y-slack && (!found || t > best) {
			best, found = t, true
		}
	}
	// A surface reaching the top of the cell fills the unit's feet cell.
	if !found || best >= y+1-slack {
		return 0, false
	}
	if !v.clear(x, z, best, best+bodyH) {
		return 0, false
	}
	return best, true
}

// Footing is the height the unit's feet rest at when standing in cell c, and
// false if it cannot stand there.
func Footing(b Blocks, c protocol.BlockPos) (float32, bool) { return newView(b).footing(c) }

// edge walks the unit from the centre of cell from, standing at y, to the
// centre of the cell dx, dz away. It reports where it ends, and whether a
// jump was needed on the way. A move that fails is not possible.
func (v *view) edge(from protocol.BlockPos, y float32, dx, dz int32) (end float32, jump, ok bool) {
	end, ok = v.sweepEdge(from, y, dx, dz, stepHeight)
	if ok {
		return end, false, true
	}
	end, ok = v.sweepEdge(from, y, dx, dz, jumpRise)
	if !ok || end <= y+stepHeight+slack {
		// The jump variant only matters for rises the walk could not make.
		return 0, false, false
	}
	// The jump takes the unit up through the space above where it started.
	if !v.clear(float32(from.X())+0.5, float32(from.Z())+0.5, end, end+bodyH) {
		return 0, false, false
	}
	return end, true, true
}

func (v *view) sweepEdge(from protocol.BlockPos, y float32, dx, dz int32, rise float32) (float32, bool) {
	x, z := float32(from.X())+0.5, float32(from.Z())+0.5
	y0 := y
	for i := 0; i < samples; i++ {
		x += float32(dx) / samples
		z += float32(dz) / samples
		g, found := v.ground(x, z, y, rise)
		if !found || y0-g > maxDrop+slack {
			return 0, false
		}
		// A falling unit needs the whole column it falls through.
		if !v.clear(x, z, g, maxf(y, g)+bodyH) {
			return 0, false
		}
		y = g
	}
	return y, true
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
