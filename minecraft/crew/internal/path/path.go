// Package path plans walks over the blocks a crew unit knows. Anything the
// unit has not been told about is treated as solid, so a plan never leads into
// unloaded terrain.
package path

import (
	"container/heap"
	"errors"
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	defaultMaxNodes = 4096
	maxStepUp       = 1
	maxDrop         = 3
)

// ErrNoPath means no route exists within the search budget.
var ErrNoPath = errors.New("no path")

// Blocks is the part of the block store the planner reads. known is false for
// a block the unit was never told about.
type Blocks interface {
	Block(protocol.BlockPos) (name string, known bool)
}

// Standable reports whether the unit fits at feet: the cells it occupies are
// known, the surface under it holds it, and the space above that surface is
// clear. feet is the cell the feet are in, so a unit on a slab is in the cell
// of the slab.
func Standable(b Blocks, feet protocol.BlockPos) bool {
	_, ok := Footing(b, feet)
	return ok
}

type node struct {
	pos protocol.BlockPos
	// y is the height of the unit's feet, which a slab or stair leaves
	// between cell boundaries.
	y float32
	// x and z are where the unit stands; the cell's centre except for the
	// node a search starts from.
	x, z   float32
	g, f   float64
	parent *node
	index  int
	closed bool
}

type queue []*node

func (q queue) Len() int           { return len(q) }
func (q queue) Less(i, j int) bool { return q[i].f < q[j].f }
func (q queue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
}
func (q *queue) Push(x any) {
	n := x.(*node)
	n.index = len(*q)
	*q = append(*q, n)
}
func (q *queue) Pop() any {
	old := *q
	n := old[len(old)-1]
	*q = old[:len(old)-1]
	return n
}

// octile is admissible for 8-way movement with unit orthogonal and sqrt2
// diagonal costs; vertical moves cost the same as flat ones, so ignoring Y
// never overestimates.
func octile(a, b protocol.BlockPos) float64 {
	dx := math.Abs(float64(a.X() - b.X()))
	dz := math.Abs(float64(a.Z() - b.Z()))
	return math.Max(dx, dz) + (math.Sqrt2-1)*math.Min(dx, dz)
}

// Find returns the feet positions from from (excluded) to to (included).
// maxNodes bounds the nodes expanded; zero or less means the default.
//
// The unit is taken to stand wherever from lets it: on the ground of that cell,
// or, in mid-air, on what it falls onto.
func Find(b Blocks, from, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	v := newView(b)
	from, y := v.landing(from)
	return find(v, from, centre(from.X()), y, centre(from.Z()), to, maxNodes)
}

func centre(c int32) float32 { return float32(c) + 0.5 }

// FindFrom is Find for a unit known to stand with its feet at height y, as one
// does halfway up a stair, where the cell it is in holds no footing of its
// own.
func FindFrom(b Blocks, from protocol.BlockPos, y float32, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	return find(newView(b), from, centre(from.X()), y, centre(from.Z()), to, maxNodes)
}

// FindFromPos is FindFrom for a unit that is not at its cell's centre. The
// first move is swept from where the unit really is, so one that brushed a
// fence post or a gate, and stands partly in its cell, can still plan its way
// out. A unit in the air is planned from where it will land.
func FindFromPos(b Blocks, x, y, z float32, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	v := newView(b)
	y = v.settle(x, y, z)
	from := protocol.BlockPos{floor32(x), floor32(y + slack), floor32(z)}
	return find(v, from, x, y, z, to, maxNodes)
}

// Settle is the height a unit at (x, y, z) comes to rest at, so a caller that
// keeps the unit's height beside a plan agrees with where FindFromPos starts.
func Settle(b Blocks, x, y, z float32) float32 { return newView(b).settle(x, y, z) }

// settle is the height a unit at (x, y, z) comes to rest at: its own, if
// something holds it there, else the surface it would fall onto.
func (v *view) settle(x, y, z float32) float32 {
	bs := v.gather(1, x, z, x, z, floor32(y)-maxDrop-1, floor32(y))
	if g, ok := groundIn(bs, x, z, y, 0); ok {
		return g
	}
	return y
}

func find(v *view, from protocol.BlockPos, x, y, z float32, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	if from == to {
		return nil, nil
	}
	to, _ = v.landing(to)
	if _, ok := v.footing(to); !ok {
		return nil, ErrNoPath
	}
	if from == to {
		return nil, nil
	}
	start := &node{pos: from, y: y, x: x, z: z, f: octile(from, to)}
	nodes := map[protocol.BlockPos]*node{from: start}
	open := &queue{}
	heap.Push(open, start)

	for expanded := 0; open.Len() > 0; {
		cur := heap.Pop(open).(*node)
		if cur.pos == to {
			return unwind(cur), nil
		}
		if cur.closed {
			continue
		}
		cur.closed = true
		if expanded++; expanded > maxNodes {
			return nil, ErrNoPath
		}
		for _, m := range neighbours(v, cur) {
			n, seen := nodes[m.pos]
			if seen && n.closed {
				continue
			}
			g := cur.g + m.cost
			if seen && g >= n.g {
				continue
			}
			if !seen {
				n = &node{pos: m.pos, y: m.y, x: centre(m.pos.X()), z: centre(m.pos.Z())}
				nodes[m.pos] = n
			}
			n.g, n.f, n.parent = g, g+octile(m.pos, to), cur
			if seen {
				heap.Fix(open, n.index)
			} else {
				heap.Push(open, n)
			}
		}
	}
	return nil, ErrNoPath
}

func unwind(n *node) []protocol.BlockPos {
	var rev []protocol.BlockPos
	for ; n.parent != nil; n = n.parent {
		rev = append(rev, n.pos)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

type move struct {
	pos  protocol.BlockPos
	y    float32
	cost float64
}

var dirs = [8][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}}

// neighbours are the cells one move away: a walk, a step up or down a ledge
// the unit can step, or a jump or drop. Each is found by walking the unit's
// footprint across and noting where it ends.
func neighbours(v *view, at *node) []move {
	type sweep struct {
		y  float32
		ok bool
	}
	var swept [len(dirs)]sweep
	var out []move
	for i, d := range dirs {
		tx, tz := at.pos.X()+d[0], at.pos.Z()+d[1]
		// A diagonal cuts a corner, which the box does not survive if either
		// cell beside it stops the unit. The orthogonal moves come first in
		// dirs, so their sweeps are already known.
		if d[0] != 0 && d[1] != 0 && !(swept[dirIndex(d[0], 0)].ok && swept[dirIndex(0, d[1])].ok) {
			continue
		}
		y, _, ok := edge(v.around(at.x, at.z, at.y, tx, tz), at.x, at.z, at.y, tx, tz)
		swept[i] = sweep{y, ok}
		if !ok {
			continue
		}
		to := protocol.BlockPos{tx, floor32(y + slack), tz}
		if _, standable := v.footing(to); !standable {
			continue
		}
		cost := 1.0
		if d[0] != 0 && d[1] != 0 {
			cost = math.Sqrt2
		}
		out = append(out, move{to, y, cost})
	}
	return out
}

// dirIndex is the position of an orthogonal step in dirs.
func dirIndex(dx, dz int32) int {
	switch {
	case dx > 0:
		return 0
	case dx < 0:
		return 1
	case dz > 0:
		return 2
	}
	return 3
}

// landing is where a unit in cell c ends up standing: the cell itself, or the
// first one below it that holds the unit, since a unit in mid-air falls. A
// cell that is filled only above the unit's step, like a stair's, holds a unit
// standing on its lower half. A column with nothing to stand on within reach
// is taken at face value.
func (v *view) landing(c protocol.BlockPos) (protocol.BlockPos, float32) {
	for d := int32(0); d <= maxDrop; d++ {
		below := protocol.BlockPos{c.X(), c.Y() - d, c.Z()}
		if y, ok := v.footing(below); ok {
			return below, y
		}
	}
	x, z, y := centre(c.X()), centre(c.Z()), float32(c.Y())
	bs := v.gather(1, x, z, x, z, c.Y()-1, c.Y())
	if g, ok := groundIn(bs, x, z, y, stepHeight); ok && g >= y-slack {
		return c, g
	}
	return c, y
}

// Landing is the cell a unit asked to stand in c ends up in: c itself, or the
// floor it would fall to when c hangs over a drop. A cell with nothing to
// stand on within reach comes back unchanged, with the height it was given.
func Landing(b Blocks, c protocol.BlockPos) (protocol.BlockPos, float32) {
	return newView(b).landing(c)
}

// NeedsJump reports whether the move between two adjacent cells of a plan
// takes a jump, as opposed to a walk or a step up a half block. y is the
// height of the unit's feet in the first cell.
func NeedsJump(b Blocks, from protocol.BlockPos, y float32, to protocol.BlockPos) bool {
	return NeedsJumpFrom(b, centre(from.X()), y, centre(from.Z()), to)
}

// NeedsJumpFrom is NeedsJump for a unit at (x, y, z), which need not be at the
// centre of its cell.
func NeedsJumpFrom(b Blocks, x, y, z float32, to protocol.BlockPos) bool {
	v := newView(b)
	_, jump, ok := edge(v.around(x, z, y, to.X(), to.Z()), x, z, y, to.X(), to.Z())
	return ok && jump
}
