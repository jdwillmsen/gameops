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
	// unitHeight is the unit's height in whole blocks: it needs a feet and a
	// head cell clear.
	unitHeight = 2
)

// ErrNoPath means no route exists within the search budget.
var ErrNoPath = errors.New("no path")

// Blocks is the part of the block store the planner reads. known is false for
// a block the unit was never told about.
type Blocks interface {
	Block(protocol.BlockPos) (name string, known bool)
}

func passableAt(b Blocks, p protocol.BlockPos) bool {
	name, known := b.Block(p)
	return known && Passable(name)
}

// Standable reports whether the unit fits at feet: both cells it occupies are
// clear and the block under it is known and solid.
func Standable(b Blocks, feet protocol.BlockPos) bool {
	x, y, z := feet.X(), feet.Y(), feet.Z()
	if !passableAt(b, feet) || !passableAt(b, protocol.BlockPos{x, y + 1, z}) {
		return false
	}
	name, known := b.Block(protocol.BlockPos{x, y - 1, z})
	return known && !Passable(name)
}

type solid struct{ b Blocks }

// NewSolid adapts a block store to the collision query the actuator makes.
// Unknown blocks are solid, matching the planner.
func NewSolid(b Blocks) interface{ Solid(protocol.BlockPos) bool } { return solid{b} }

func (s solid) Solid(p protocol.BlockPos) bool { return !passableAt(s.b, p) }

type node struct {
	pos    protocol.BlockPos
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
func Find(b Blocks, from, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error) {
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	if from == to {
		return nil, nil
	}
	if !Standable(b, to) {
		return nil, ErrNoPath
	}
	start := &node{pos: from, f: octile(from, to)}
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
		for _, m := range neighbours(b, cur.pos) {
			n, seen := nodes[m.pos]
			if seen && n.closed {
				continue
			}
			g := cur.g + m.cost
			if seen && g >= n.g {
				continue
			}
			if !seen {
				n = &node{pos: m.pos}
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
	cost float64
}

var dirs = [8][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}}

func neighbours(b Blocks, at protocol.BlockPos) []move {
	var out []move
	x, y, z := at.X(), at.Y(), at.Z()
	for _, d := range dirs {
		diagonal := d[0] != 0 && d[1] != 0
		if diagonal &&
			!Standable(b, protocol.BlockPos{x + d[0], y, z}) ||
			diagonal && !Standable(b, protocol.BlockPos{x, y, z + d[1]}) {
			continue
		}
		tx, tz := x+d[0], z+d[1]
		for dy := int32(maxStepUp); dy >= -maxDrop; dy-- {
			to := protocol.BlockPos{tx, y + dy, tz}
			if !Standable(b, to) || !fits(b, at, to) {
				continue
			}
			cost := 1.0
			if diagonal {
				cost = math.Sqrt2
			}
			out = append(out, move{to, cost})
			break
		}
	}
	return out
}

// fits checks the cells the unit sweeps through that Standable at the
// destination does not cover: the column it falls through when stepping down,
// or the headroom it needs at the origin to jump up.
func fits(b Blocks, from, to protocol.BlockPos) bool {
	if to.Y() > from.Y() {
		return passableAt(b, protocol.BlockPos{from.X(), from.Y() + unitHeight, from.Z()})
	}
	for y := to.Y(); y < from.Y()+unitHeight; y++ {
		if !passableAt(b, protocol.BlockPos{to.X(), y, to.Z()}) {
			return false
		}
	}
	return true
}
