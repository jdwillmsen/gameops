package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// Cutter is a skill that can say which blocks it broke.
type Cutter interface {
	Cut() []protocol.BlockPos
}

// Replant sets a sapling back where a tree was felled, so a grove cut for
// wood grows again. It plants at the foot of every trunk the skill it follows
// cut: the cells that held a log with dirt or grass under them. It uses the
// saplings the unit carries and ends done when they run out, so a unit with
// none costs nothing.
type Replant struct {
	world path.Blocks
	self  func() belief.Snapshot
	src   Cutter
	item  string
	opts  []Option

	cells   []protocol.BlockPos
	built   bool
	idx     int
	cur     Skill
	planted int
	// aside walks the unit out of the cell it is about to fill: the server
	// refuses a block placed into the placer's own body, and a unit that has
	// just felled the tree is usually standing where the trunk was.
	aside *navigator
}

// NewReplant plants item at the foot of each tree src felled.
func NewReplant(world path.Blocks, self func() belief.Snapshot, src Cutter, item string, opts ...Option) Skill {
	return &Replant{world: world, self: self, src: src, item: item, opts: opts}
}

func (r *Replant) Name() string { return "replant" }

func (r *Replant) Ready(s belief.Snapshot) error { return overworld(s) }

// Progress counts saplings planted, which the inventory cannot show.
func (r *Replant) Progress() int { return r.planted }

// Report says how many saplings went back, when any did.
func (r *Replant) Report() (string, bool) {
	if r.planted == 0 {
		return "", false
	}
	return fmt.Sprintf("replanted %d %s", r.planted, r.item), true
}

// Resume drops the placement in flight; its cell is judged afresh.
func (r *Replant) Resume() {
	r.aside = nil
	if r.cur != nil {
		r.cur = nil
		r.idx--
	}
}

func (r *Replant) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	snap := r.self()
	if err := overworld(snap); err != nil {
		return Failed, err
	}
	if !r.built {
		r.built, r.cells = true, r.bases(cellOf(a.Pose().Feet))
	}
	for {
		if r.cur == nil {
			if !snap.Creative && count(snap, r.item) < 1 {
				return Done, nil
			}
			target, ok := r.nextTarget()
			if !ok {
				return Done, nil
			}
			r.cur = NewPlaceBlock(r.world, r.self, target, r.item, r.opts...)
			if pose := a.Pose(); inBody(pose.Feet[0], pose.Feet[1], pose.Feet[2], target) {
				stand, found := standNear(r.world, target, pose, 2)
				if !found {
					// Nowhere to step to: the placement fails on its own.
					r.aside = nil
				} else {
					r.aside = newNavigator(r.world, stand, arriveSlack)
				}
			}
		}
		if r.aside != nil {
			st, err := r.aside.step(a)
			if st == Running {
				return Running, nil
			}
			r.aside = nil
			var te tickError
			if errors.As(err, &te) {
				return Failed, err
			}
		}
		st, err := r.cur.Step(ctx, a)
		switch st {
		case Running:
			return Running, nil
		case Done:
			r.planted++
		default:
			// One cell that cannot be reached must not end the replanting; a
			// dead connection or a cancelled run must.
			var ce cellError
			if ctx.Err() != nil || !errors.As(err, &ce) {
				return Failed, err
			}
		}
		r.cur = nil
		snap = r.self()
	}
}

// inBody says whether a unit with its feet at (x, y, z) fills any of the cell:
// the box it stands in is 0.6 wide and 1.8 high.
func inBody(x, y, z float32, cell protocol.BlockPos) bool {
	const reach = 0.5 + 0.3
	return math.Abs(float64(x)-float64(cell.X())-0.5) < reach &&
		math.Abs(float64(z)-float64(cell.Z())-0.5) < reach &&
		float64(y) < float64(cell.Y())+1 && float64(y)+1.8 > float64(cell.Y())
}

func (r *Replant) nextTarget() (protocol.BlockPos, bool) {
	for r.idx < len(r.cells) {
		c := r.cells[r.idx]
		r.idx++
		if r.plantable(c) {
			return c, true
		}
	}
	return protocol.BlockPos{}, false
}

// bases lists the cells a trunk stood in at ground level, each once, nearest
// the unit first by a greedy walk so the unit does not criss-cross the grove.
// Logs cut from a pillar have more log below them, not ground, and drop out.
func (r *Replant) bases(from protocol.BlockPos) []protocol.BlockPos {
	seen := map[protocol.BlockPos]bool{}
	var left []protocol.BlockPos
	for _, c := range r.src.Cut() {
		if seen[c] {
			continue
		}
		seen[c] = true
		if r.plantable(c) {
			left = append(left, c)
		}
	}
	out := make([]protocol.BlockPos, 0, len(left))
	for len(left) > 0 {
		best := 0
		for i, c := range left {
			if sqDist(from, c) < sqDist(from, left[best]) {
				best = i
			}
		}
		from = left[best]
		out = append(out, from)
		left = append(left[:best], left[best+1:]...)
	}
	return out
}

// plantable says whether a sapling can go in the cell now: it is open, there
// is room above it for the first block of growth, and it stands on grass or
// dirt.
func (r *Replant) plantable(c protocol.BlockPos) bool {
	here, known := r.world.Block(c)
	if !known || !path.Passable(here) || strings.HasSuffix(here, "_sapling") {
		return false
	}
	below, known := r.world.Block(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()})
	if !known || (below != "grass_block" && below != "dirt") {
		return false
	}
	above, known := r.world.Block(protocol.BlockPos{c.X(), c.Y() + 1, c.Z()})
	return known && path.Passable(above)
}
