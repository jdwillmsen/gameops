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
	y      float32
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
	return find(v, from, y, to, maxNodes)
}

// FindFrom is Find for a unit known to stand with its feet at height y, as one
// does halfway up a stair, where the cell it is in holds no footing of its
// own.
func FindFrom(b Blocks, from protocol.BlockPos, y float32, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	return find(newView(b), from, y, to, maxNodes)
}

func find(v *view, from protocol.BlockPos, y float32, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	if from == to {
		return nil, nil
	}
	if _, ok := v.footing(to); !ok {
		return nil, ErrNoPath
	}
	start := &node{pos: from, y: y, f: octile(from, to)}
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
				n = &node{pos: m.pos, y: m.y}
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
	var out []move
	for _, d := range dirs {
		y, _, ok := v.edge(at.pos, at.y, d[0], d[1])
		if !ok {
			continue
		}
		to := protocol.BlockPos{at.pos.X() + d[0], floor32(y + slack), at.pos.Z() + d[1]}
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

// landing is where a unit in cell c ends up standing: the cell itself, or the
// first one below it that holds the unit, since a unit in mid-air falls. A
// column with nothing to stand on within reach is taken at face value.
func (v *view) landing(c protocol.BlockPos) (protocol.BlockPos, float32) {
	for d := int32(0); d <= maxDrop; d++ {
		below := protocol.BlockPos{c.X(), c.Y() - d, c.Z()}
		if y, ok := v.footing(below); ok {
			return below, y
		}
	}
	return c, float32(c.Y())
}

// NeedsJump reports whether the move between two adjacent cells of a plan
// takes a jump, as opposed to a walk or a step up a half block. y is the
// height of the unit's feet in the first cell.
func NeedsJump(b Blocks, from protocol.BlockPos, y float32, to protocol.BlockPos) bool {
	_, jump, ok := newView(b).edge(from, y, to.X()-from.X(), to.Z()-from.Z())
	return ok && jump
}
