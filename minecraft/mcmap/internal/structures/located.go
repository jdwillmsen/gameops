package structures

import (
	"cmp"
	"context"
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Most kinds have no record of their own and are found all the same, with
// no seed, by what only they are generated with:
//
//   - a trial chamber, by its trial spawners and vaults, and a stronghold,
//     by the silverfish spawner of its portal room or the blocks of its end
//     portal once that is lit: blocks nobody in a survival world can pick
//     up and put somewhere else;
//   - an end gateway and the End's exit portal, by blocks nothing breaks;
//   - an end city, a bastion and a ruined portal, by the chests, barrels
//     and suspicious blocks that still carry the loot table the generator
//     gave them. The game drops the table the first time one is opened or
//     brushed, so a block that has one has not been touched, and one of
//     these kinds is found for as long as one such block is left in it.
//
// What is found is where those blocks are. The box is the box around them,
// which is less than the structure: a chamber's corridors run on past its
// last spawner, and a stronghold is found only by one room of it.
const (
	Stronghold   Kind = "stronghold"
	TrialChamber Kind = "trial_chamber"
	EndCity      Kind = "end_city"
	EndGateway   Kind = "end_gateway"
	ExitPortal   Kind = "exit_portal"
	Bastion      Kind = "bastion"
	RuinedPortal Kind = "ruined_portal"

	Mansion        Kind = "mansion"
	AncientCity    Kind = "ancient_city"
	Shipwreck      Kind = "shipwreck"
	OceanRuins     Kind = "ocean_ruins"
	BuriedTreasure Kind = "buried_treasure"
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
	in   chunks.Dimension
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
	// surround is how far past the box round what it was found by a
	// structure's contents are counted, in blocks each way and half as far
	// up and down.
	surround int32
	is       func(b savedBlock, mob string) bool
	// mob, if set, is a mob the kind is also found by: one that is
	// generated with it, does not despawn and does not wander.
	mob string
}

func from(origins ...origin) func(savedBlock, string) bool {
	return func(b savedBlock, _ string) bool { return slices.Contains(origins, b.origin) }
}

// An end city's sites are a grid like a chamber's: squares of cityGrid
// chunks, a city starting in the first nine of its square and reaching no
// more than six chunks back from its start and five on. Every chest,
// shulker, head and frame of the FWB world's 21 cities is in the square of
// its own city, with the squares moved cityReach back; moved a chunk less
// or two more, one city is found as two.
const (
	cityGrid  = 20
	cityReach = 6
)

var locatedKinds = []located{
	{kind: Stronghold, in: chunks.Overworld, join: 1, is: func(b savedBlock, mob string) bool {
		return b.sort == blockPortal || (b.sort == blockSpawner && mob == "silverfish")
	}},
	{kind: TrialChamber, in: chunks.Overworld, grid: chamberGrid, back: chamberReach, whole: wholeChamber, surround: chamberSurround, is: func(b savedBlock, _ string) bool {
		return b.sort == blockTrialSpawner || b.sort == blockVault
	}},
	// A shulker is a mob, and one carried off and kept elsewhere in the End
	// would read as a city found in part. It is counted because a city
	// whose chests are all opened is otherwise not found at all, and its
	// shulkers are what is left to go there for.
	{kind: EndCity, in: chunks.End, grid: cityGrid, back: cityReach, surround: 32, mob: "shulker", is: func(b savedBlock, _ string) bool {
		return b.origin == originEndCity || b.sort == blockDragonHead || b.sort == blockElytra
	}},
	{kind: EndGateway, in: chunks.End, is: func(b savedBlock, _ string) bool { return b.sort == blockGateway }},
	// The End's own portal blocks are the fountain every End has.
	{kind: ExitPortal, in: chunks.End, join: 1, is: func(b savedBlock, _ string) bool { return b.sort == blockPortal }},
	// A bastion's neighbours are never nearer than four chunks, and its
	// chests are all over it.
	{kind: Bastion, in: chunks.Nether, join: 3, surround: 32, is: func(b savedBlock, mob string) bool {
		return slices.Contains([]origin{originBastion, originBastionTreasure, originBastionStables, originBastionBridge}, b.origin) ||
			(b.sort == blockSpawner && mob == "magma_cube")
	}},
	{kind: RuinedPortal, in: chunks.Overworld, surround: 8, is: from(originRuinedPortal)},
	{kind: RuinedPortal, in: chunks.Nether, surround: 8, is: from(originRuinedPortal)},
	// A mansion and an ancient city are each hundreds of blocks across
	// with chests all through them; the next of either is a region away.
	{kind: Mansion, in: chunks.Overworld, join: 4, surround: 16, is: from(originMansion)},
	{kind: AncientCity, in: chunks.Overworld, join: 6, surround: 32, is: from(originAncientCity)},
	{kind: Shipwreck, in: chunks.Overworld, join: 2, surround: 8, is: from(originShipwreck)},
	// A ruin is a cluster of small buildings, known by their chests and
	// by the suspicious sand and gravel nobody has brushed.
	{kind: OceanRuins, in: chunks.Overworld, join: 2, surround: 8, is: from(originOceanRuins)},
	{kind: BuriedTreasure, in: chunks.Overworld, is: from(originBuriedTreasure)},
	// The kinds a newer game records, where an older one left only what
	// is in them. One found here that the world has also recorded is left
	// to its record.
	{kind: DesertPyramid, in: chunks.Overworld, join: 1, surround: 8, is: from(originDesertPyramid)},
	{kind: JungleTemple, in: chunks.Overworld, join: 1, surround: 8, is: from(originJungleTemple)},
	{kind: Igloo, in: chunks.Overworld, join: 1, surround: 8, is: from(originIgloo)},
	{kind: TrailRuins, in: chunks.Overworld, join: 3, surround: 16, is: from(originTrailRuins)},
}

// surroundOf is how far past the blocks a kind was found by its contents
// are counted.
func surroundOf(kind Kind) int32 {
	for _, k := range locatedKinds {
		if k.kind == kind {
			return k.surround
		}
	}
	return 0
}

// locateCheck is how many blocks or chunks are gone through between looks
// at whether there is still time.
const locateCheck = 8192

// locate finds the structures of the located kinds in one dimension. The
// work is a few map lookups for each block the dimension holds, and it
// stops when ctx does.
func (c *contents) locate(ctx context.Context, d chunks.Dimension) ([]Structure, error) {
	var out []Structure
	for _, k := range locatedKinds {
		if k.in != d {
			continue
		}
		// The blocks are joined by the chunk they are in, so that the work
		// is a few lookups a chunk however many blocks one chunk holds.
		type found struct {
			box Box
			n   int
		}
		byChunk := map[uint64]*found{}
		var order []uint64
		hold := func(x, y, z int32) {
			key, at := chunkKey(x>>4, z>>4), Box{x, y, z, x, y, z}
			f, seen := byChunk[key]
			if !seen {
				byChunk[key] = &found{at, 1}
				order = append(order, key)
				return
			}
			f.box, f.n = f.box.union(at), f.n+1
		}
		for i, b := range c.blocks[d] {
			if i%locateCheck == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if k.is(b, c.types.names[b.mob]) {
				hold(b.x, b.y, b.z)
			}
		}
		if k.mob != "" {
			for i, m := range c.mobs[d] {
				if i%locateCheck == 0 && ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if c.types.names[m.kind] == k.mob {
					hold(m.x, m.y, m.z)
				}
			}
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
		for i, key := range order {
			if i%locateCheck == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
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
	return out, ctx.Err()
}
