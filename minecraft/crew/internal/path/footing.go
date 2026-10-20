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
	b      Blocks
	chunks map[[3]int32]*chunk
	// recent is a small direct-mapped cache in front of chunks: a search
	// keeps returning to the same few columns, and hashing the map key is
	// most of what a lookup costs.
	recent  [64]recentChunk
	footed  map[protocol.BlockPos]footed
	scratch [2][]box
}

type recentChunk struct {
	key [3]int32
	ch  *chunk
}

// chunk is 16 cells high, so a column is a few map lookups, not one per cell.
type chunk struct {
	cells [16]cell
	read  [16]bool
}

type footed struct {
	y  float32
	ok bool
}

func newView(b Blocks) *view {
	return &view{b: b, chunks: map[[3]int32]*chunk{}, footed: map[protocol.BlockPos]footed{}}
}

func (v *view) cell(p protocol.BlockPos) cell {
	key := [3]int32{p.X(), p.Y() >> 4, p.Z()}
	h := uint32(key[0])*73856093 ^ uint32(key[1])*83492791 ^ uint32(key[2])*19349663
	r := &v.recent[h&uint32(len(v.recent)-1)]
	if r.ch == nil || r.key != key {
		ch := v.chunks[key]
		if ch == nil {
			ch = &chunk{}
			v.chunks[key] = ch
		}
		r.key, r.ch = key, ch
	}
	slot := p.Y() & 15
	if r.ch.read[slot] {
		return r.ch.cells[slot]
	}
	c := v.load(p)
	r.ch.cells[slot], r.ch.read[slot] = c, true
	return c
}

func (v *view) load(p protocol.BlockPos) cell {
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
	return c
}

func floor32(f float32) int32 { return int32(math.Floor(float64(f))) }

// gather lists the boxes in the cells from y0 to y1 under the area the
// footprint sweeps from (x0, z0) to (x1, z1). The list lives in one of the
// view's two scratch buffers, slot, and is good until the next call with it.
func (v *view) gather(slot int, x0, z0, x1, z1 float32, y0, y1 int32) []box {
	out := v.scratch[slot][:0]
	lox, hix := minf(x0, x1)-halfW+slack, maxf(x0, x1)+halfW-slack
	loz, hiz := minf(z0, z1)-halfW+slack, maxf(z0, z1)+halfW-slack
	for cx := floor32(lox); cx <= floor32(hix); cx++ {
		for cz := floor32(loz); cz <= floor32(hiz); cz++ {
			for cy := y0; cy <= y1; cy++ {
				out = append(out, v.cell(protocol.BlockPos{cx, cy, cz}).boxes...)
			}
		}
	}
	v.scratch[slot] = out
	return out
}

// touching reports whether a box overlaps the footprint centred on (x, z).
func touching(b box, x, z float32) bool {
	return b.lo[0] < x+halfW-slack && b.hi[0] > x-halfW+slack &&
		b.lo[2] < z+halfW-slack && b.hi[2] > z-halfW+slack
}

// clearIn reports whether the body standing at feet height y over (x, z) fits
// among the boxes: nothing but its footing reaches into the space above y.
// Reaching up to top lets a caller ask for the room a falling or jumping unit
// needs.
func clearIn(bs []box, x, z, y, top float32) bool {
	for _, b := range bs {
		if touching(b, x, z) && b.hi[1] > y+slack && b.lo[1] < top-slack {
			return false
		}
	}
	return true
}

// groundIn is the height of the highest surface under the footprint that is no
// more than rise above y, and no more than maxDrop below it.
func groundIn(bs []box, x, z, y, rise float32) (float32, bool) {
	best, found := float32(0), false
	for _, b := range bs {
		if touching(b, x, z) && b.hi[1] <= y+rise+slack && (!found || b.hi[1] > best) {
			best, found = b.hi[1], true
		}
	}
	return best, found && best >= y-maxDrop-slack
}

// footing is the height of the surface the unit stands on in the feet cell c,
// measured at the cell's centre.
func (v *view) footing(c protocol.BlockPos) (float32, bool) {
	if f, ok := v.footed[c]; ok {
		return f.y, f.ok
	}
	y, ok := v.footingOf(c)
	v.footed[c] = footed{y, ok}
	return y, ok
}

func (v *view) footingOf(c protocol.BlockPos) (float32, bool) {
	x, z := float32(c.X())+0.5, float32(c.Z())+0.5
	y := float32(c.Y())
	if !v.cell(c).known || !v.cell(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()}).known ||
		!v.cell(protocol.BlockPos{c.X(), c.Y() + 1, c.Z()}).known {
		return 0, false
	}
	bs := v.gather(1, x, z, x, z, c.Y()-1, c.Y()+2)
	best, found := float32(0), false
	for _, b := range bs {
		if t := b.hi[1]; t >= y-slack && t < y+1 && touching(b, x, z) && (!found || t > best) {
			best, found = t, true
		}
	}
	// A surface reaching the top of the cell fills the unit's feet cell.
	for _, b := range bs {
		if touching(b, x, z) && b.hi[1] >= y+1-slack && b.lo[1] < y+1 && b.lo[1] <= y+slack {
			return 0, false
		}
	}
	if !found {
		return 0, false
	}
	if !clearIn(bs, x, z, best, best+bodyH) {
		return 0, false
	}
	return best, true
}

// Footing is the height the unit's feet rest at when standing in cell c, and
// false if it cannot stand there.
func Footing(b Blocks, c protocol.BlockPos) (float32, bool) { return newView(b).footing(c) }

// around lists the boxes a unit at (x, z), standing at feet height y, can meet
// walking to the centre of cell (tx, tz): everything in reach of the cells it
// crosses, from the deepest drop to the highest ledge and the room a jump
// needs.
func (v *view) around(x, z, y float32, tx, tz int32) []box {
	return v.gather(0, x, z, float32(tx)+0.5, float32(tz)+0.5, floor32(y)-maxDrop-1, floor32(y)+3)
}

// edge walks the unit from (x0, z0), standing at feet height y, to the centre
// of cell (tx, tz), among the boxes bs that around lists. It reports where it
// ends, and whether a jump was needed on the way. A move that fails is not
// possible.
func edge(bs []box, x0, z0, y float32, tx, tz int32) (end float32, jump, ok bool) {
	x1, z1 := float32(tx)+0.5, float32(tz)+0.5
	end, _, ok = sweepEdge(bs, x0, z0, x1, z1, y, stepHeight)
	if ok {
		return end, false, true
	}
	end, lowest, ok := sweepEdge(bs, x0, z0, x1, z1, y, jumpRise)
	// A jump has to end higher than the lowest point the move reaches, which
	// is where the unit will be standing when it takes off: the navigator
	// jumps at the foot of a rise. A move that only touches a ledge on the way
	// and comes back down has no such foot, and nothing would jump it.
	if !ok || end-lowest <= stepHeight+slack {
		return 0, false, false
	}
	// The jump takes the unit up through the space above where it started.
	if !clearIn(bs, x0, z0, end, end+bodyH) {
		return 0, false, false
	}
	return end, true, true
}

// sweepEdge slides the footprint from (x0, z0) to (x1, z1) in eighths and
// returns the height it ends at and the lowest it reaches on the way, so a
// dip followed by a climb out of it shows, which comparing only the ends
// would miss.
func sweepEdge(bs []box, x0, z0, x1, z1, y, rise float32) (end, lowest float32, ok bool) {
	start := y
	lowest = y
	x, z := x0, z0
	for i := 0; i < samples; i++ {
		x += (x1 - x0) / samples
		z += (z1 - z0) / samples
		g, found := groundIn(bs, x, z, y, rise)
		if !found || start-g > maxDrop+slack {
			return 0, 0, false
		}
		// A falling unit needs the whole column it falls through.
		if !clearIn(bs, x, z, g, maxf(y, g)+bodyH) {
			return 0, 0, false
		}
		y = g
		if y < lowest {
			lowest = y
		}
	}
	return y, lowest, true
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
