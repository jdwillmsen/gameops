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

// maxLocatedSpan is the widest box a located structure is drawn with. The
// widest chamber in the FWB world is found across 214 blocks. Blocks laid
// in a line join without end, and a box that wide is no one structure's:
// it is left out and counted.
const maxLocatedSpan = 512

// located is how one kind is found.
type located struct {
	kind Kind
	// join is how many chunks apart two chunks holding the kind's blocks
	// may be and still be one structure. A chamber's rooms are a corridor
	// apart; a portal room is one room.
	join int32
	is   func(b savedBlock, mob string) bool
}

var locatedKinds = []located{
	{Stronghold, 1, func(b savedBlock, mob string) bool {
		return b.sort == blockPortal || (b.sort == blockSpawner && mob == "silverfish")
	}},
	{TrialChamber, 5, func(b savedBlock, _ string) bool {
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
		parent := map[uint64]uint64{}
		var find func(uint64) uint64
		find = func(k uint64) uint64 {
			for parent[k] != k {
				parent[k] = parent[parent[k]]
				k = parent[k]
			}
			return k
		}
		for _, key := range order {
			parent[key] = key
		}
		for _, key := range order {
			x, z := int32(key>>32), int32(uint32(key))
			for dx := -k.join; dx <= k.join; dx++ {
				for dz := -k.join; dz <= k.join; dz++ {
					if other := chunkKey(x+dx, z+dz); other != key && byChunk[other] != nil {
						parent[find(key)] = find(other)
					}
				}
			}
		}
		joined := map[uint64]*Structure{}
		for _, key := range order {
			root, f := find(key), byChunk[key]
			s, seen := joined[root]
			if !seen {
				joined[root] = &Structure{Kind: k.kind, Box: f.box, Evidence: f.n}
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
