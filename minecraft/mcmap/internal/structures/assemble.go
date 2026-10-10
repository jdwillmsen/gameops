package structures

import "slices"

// Structure is one the world has recorded: every area of one kind that
// belongs together, as a single box.
type Structure struct {
	Kind Kind `json:"kind"`
	Box
	// Areas is how many recorded areas the box was put together from; a
	// village is not put together from any.
	Areas int `json:"areas,omitempty"`
	// Village is set for a village and nothing else.
	Village *VillageFacts `json:"village,omitempty"`
	// Evidence is set for a kind the world keeps no record of, which is
	// found by the block entities only that kind is generated with: it is
	// how many of them there are. The box is then the box around those,
	// and the structure itself reaches further than it.
	Evidence int `json:"evidence,omitempty"`
	// Partial is set for one found by less than a finished structure of
	// its kind ever is: the rest of it is most likely in chunks the world
	// has not generated yet.
	Partial bool `json:"partial,omitempty"`
	// Variant is which of several the structure is, for a kind the world
	// records by variant: the biome an abandoned camp was built for.
	Variant string `json:"variant,omitempty"`
}

// joinGap is how far apart, in blocks across the map, two areas of a kind
// may be and still be the same structure. A monument, a hut or a pyramid
// is cut only by chunk edges, so its parts touch. A fortress is corridors
// and rooms recorded one by one with open ground between them, and its
// neighbours are never nearer than four chunks. An outpost's tents and
// cages stand apart from its tower, and the next outpost is 24 chunks off.
func joinGap(kind Kind) int32 {
	switch kind {
	case Fortress:
		return 32
	case Outpost:
		return 48
	}
	return 0
}

func gap(a, b Box) int32 {
	dx := max(a.MinX-b.MaxX, b.MinX-a.MaxX) - 1
	dz := max(a.MinZ-b.MaxZ, b.MinZ-a.MaxZ) - 1
	return max(dx, dz, 0)
}

// assemble joins the areas of each kind into structures. Areas are bucketed
// by the chunk they lie in, which each one does entirely, so an area is only
// compared with those near enough to join rather than with all of them.
func assemble(pieces []piece) []Structure {
	type cell struct {
		kind Kind
		x, z int32
	}
	parent := make([]int, len(pieces))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	cells := map[cell][]int{}
	for i, p := range pieces {
		c := cell{p.kind, p.box.MinX >> 4, p.box.MinZ >> 4}
		cells[c] = append(cells[c], i)
	}
	for i, p := range pieces {
		reach := joinGap(p.kind)/16 + 1
		home := cell{p.kind, p.box.MinX >> 4, p.box.MinZ >> 4}
		for dx := -reach; dx <= reach; dx++ {
			for dz := -reach; dz <= reach; dz++ {
				for _, j := range cells[cell{p.kind, home.x + dx, home.z + dz}] {
					if j > i && gap(p.box, pieces[j].box) <= joinGap(p.kind) {
						parent[find(i)] = find(j)
					}
				}
			}
		}
	}
	joined := map[int]*Structure{}
	for i, p := range pieces {
		root := find(i)
		s, ok := joined[root]
		if !ok {
			joined[root] = &Structure{Kind: p.kind, Box: p.box, Areas: 1, Variant: p.variant}
			continue
		}
		s.Box = s.Box.union(p.box)
		s.Areas++
		// The same every time, whichever piece was come to first.
		if p.variant != "" && (s.Variant == "" || p.variant < s.Variant) {
			s.Variant = p.variant
		}
	}
	out := make([]Structure, 0, len(joined))
	for _, s := range joined {
		out = append(out, *s)
	}
	slices.SortFunc(out, compareStructures)
	return out
}

// Largest first, so that a list cut short keeps whole structures and drops
// stray fragments; then by position, so the order is the same every time.
func compareStructures(a, b Structure) int {
	if a.Areas != b.Areas {
		return b.Areas - a.Areas
	}
	for _, d := range []int32{a.MinX - b.MinX, a.MinZ - b.MinZ, a.MinY - b.MinY} {
		if d != 0 {
			return int(d)
		}
	}
	return slices.Index(Kinds, a.Kind) - slices.Index(Kinds, b.Kind)
}
