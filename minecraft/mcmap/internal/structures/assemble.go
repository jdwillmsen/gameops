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
}

// joinGap is how far apart, in blocks across the map, two areas of a kind
// may be and still be the same structure. A monument, an outpost or a hut
// is cut only by chunk edges, so its parts touch. A fortress is corridors
// and rooms recorded one by one with open ground between them, and its
// neighbours are never nearer than four chunks.
func joinGap(kind Kind) int32 {
	if kind == Fortress {
		return 32
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
			joined[root] = &Structure{Kind: p.kind, Box: p.box, Areas: 1}
			continue
		}
		s.Box = s.Box.union(p.box)
		s.Areas++
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
