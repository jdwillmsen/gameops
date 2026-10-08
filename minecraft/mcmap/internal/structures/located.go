package structures

import (
	"cmp"
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Two kinds have no record of their own and are found all the same, with
// no seed, by block entities only they are generated with and that nobody
// in a survival world can pick up and put somewhere else:
//
//   - a trial chamber, by its trial spawners and vaults;
//   - a stronghold, by the silverfish spawner of its portal room, or the
//     blocks of its end portal once that is lit.
//
// What is found is where those blocks are. The box is the box around them,
// which is less than the structure: a chamber's corridors run on past its
// last spawner, and a stronghold is found only by one room of it.
const (
	Stronghold   Kind = "stronghold"
	TrialChamber Kind = "trial_chamber"
)

// maxLocatedSpan is the widest box a located structure is drawn with: one
// square of the grid below, which is as far as a chamber's blocks can be
// apart. The widest chamber in the FWB world is found across 214 blocks.
// A box wider is no one structure's: it is left out and counted.
const maxLocatedSpan = chamberGrid * 16

// The generator gives trial chambers a grid: the world is cut into squares
// of chamberGrid chunks, and each square has at most one chamber. A chamber
// starts in the first 22 chunks of its square and reaches a few chunks
// either way, so its blocks lie from five chunks before the square to 26
// into it, and the two chunks after that hold no chamber's. All 6,282
// trial spawners and vaults of the FWB world bear this out: none is in
// those two chunks, on either axis.
//
// So which chamber a block belongs to needs no seed and no guess at how
// far apart its rooms may be: it is the square the block is in, with the
// squares moved chamberReach chunks back to take in what reaches before
// them. Parts of one chamber cut apart by chunks the world has not
// generated yet are one chamber still, and two chambers are never one.
const (
	chamberGrid  = 34
	chamberReach = 7
	// wholeChamber is the fewest trial spawners and vaults a chamber the
	// world has finished is found by. The FWB world's chambers fall in two
	// groups with nothing between: 164 found by 21 to 73, and 55 by fewer
	// than 20, each of those at the edge of what is generated. One found
	// by fewer is said to be there in part.
	wholeChamber = 20
)

// located is how one kind is found.
type located struct {
	kind Kind
	// grid, if set, is the side in chunks of the squares that each hold
	// one structure of the kind, and back how far they are moved back.
	// Chunks holding the kind's blocks are then one structure where they
	// are in one square, however far apart.
	grid, back int32
	// join, for a kind with no grid, is how many chunks apart two chunks
	// holding its blocks may be and still be one structure. A portal room
	// is one room.
	join int32
	// whole is the least evidence a finished structure of the kind is
	// found by, where that is known; one found by less is Partial.
	whole int
	is    func(b savedBlock, mob string) bool
}

var locatedKinds = []located{
	{kind: Stronghold, join: 1, is: func(b savedBlock, mob string) bool {
		return b.sort == blockPortal || (b.sort == blockSpawner && mob == "silverfish")
	}},
	{kind: TrialChamber, grid: chamberGrid, back: chamberReach, whole: wholeChamber, is: func(b savedBlock, _ string) bool {
		return b.sort == blockTrialSpawner || b.sort == blockVault
	}},
}

// locate finds the structures of the located kinds. Both are the
// overworld's: the End has portal blocks of its own, in the fountain every
// End has.
func (c *contents) locate() []Structure {
	var out []Structure
	for _, k := range locatedKinds {
		// The blocks are joined by the chunk they are in, so that the work
		// is a few lookups a chunk however many blocks one chunk holds.
		type found struct {
			box Box
			n   int
		}
		byChunk := map[uint64]*found{}
		var order []uint64
		for _, b := range c.blocks[chunks.Overworld] {
			if !k.is(b, c.types.names[b.mob]) {
				continue
			}
			key, at := chunkKey(b.x>>4, b.z>>4), Box{b.x, b.y, b.z, b.x, b.y, b.z}
			f, seen := byChunk[key]
			if !seen {
				byChunk[key] = &found{at, 1}
				order = append(order, key)
				continue
			}
			f.box, f.n = f.box.union(at), f.n+1
		}
		// root is the structure a chunk's blocks are part of: the square
		// it is in, or the chunks it has been joined to.
		parent := map[uint64]uint64{}
		var root func(uint64) uint64
		root = func(k uint64) uint64 {
			for parent[k] != k {
				parent[k] = parent[parent[k]]
				k = parent[k]
			}
			return k
		}
		for _, key := range order {
			parent[key] = key
		}
		square := func(key uint64) uint64 {
			x, z := int32(key>>32), int32(uint32(key))
			return chunkKey(floorDiv(x+k.back, k.grid), floorDiv(z+k.back, k.grid))
		}
		first := map[uint64]uint64{}
		for _, key := range order {
			if k.grid > 0 {
				sq := square(key)
				if other, seen := first[sq]; seen {
					parent[root(key)] = root(other)
				} else {
					first[sq] = key
				}
				continue
			}
			x, z := int32(key>>32), int32(uint32(key))
			for dx := -k.join; dx <= k.join; dx++ {
				for dz := -k.join; dz <= k.join; dz++ {
					if other := chunkKey(x+dx, z+dz); other != key && byChunk[other] != nil {
						parent[root(key)] = root(other)
					}
				}
			}
		}
		joined := map[uint64]*Structure{}
		for _, key := range order {
			at, f := root(key), byChunk[key]
			s, seen := joined[at]
			if !seen {
				joined[at] = &Structure{Kind: k.kind, Box: f.box, Evidence: f.n}
				continue
			}
			s.Box, s.Evidence = s.Box.union(f.box), s.Evidence+f.n
		}
		at := len(out)
		for _, s := range joined {
			if s.MaxX-s.MinX > maxLocatedSpan || s.MaxZ-s.MinZ > maxLocatedSpan {
				c.stats.Skipped++
				continue
			}
			s.Partial = s.Evidence < k.whole
			out = append(out, *s)
		}
		// The most certain first, so that a list cut short keeps them;
		// then by position, so the order is the same every time.
		slices.SortFunc(out[at:], func(a, b Structure) int {
			return cmp.Or(cmp.Compare(b.Evidence, a.Evidence), cmp.Compare(a.MinX, b.MinX), cmp.Compare(a.MinZ, b.MinZ), cmp.Compare(a.MinY, b.MinY))
		})
	}
	return out
}
