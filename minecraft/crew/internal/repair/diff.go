package repair

import (
	"fmt"
	"sort"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// slabHeight bounds the dense grids held at once (about 800k cells for the
// Colosseum footprint).
const slabHeight = 16

// Rect is a box of cells that all must become one block.
type Rect struct {
	Box   Box
	Block string
}

// Damage is what Diff found: the rectangles to restore and the tallies the
// report is made from.
type Damage struct {
	Rects     []Rect
	Scanned   int
	Unscanned int
	Cells     int
	ByKind    map[string]int
	ByZone    map[string]int
	ByChange  map[string]int
	Footprint Box
	zoneOf    func(x, y, z int) string
	palette   *Palette
}

// Kinds of damage, from what the plan wants against what the scan found.
const (
	KindMissing  = "missing"  // planned a block, found air
	KindIntruder = "intruder" // planned air, found a block
	KindReplaced = "replaced" // planned one block, found another
)

func kindOf(want, got string) string {
	switch {
	case IsAir(got):
		return KindMissing
	case IsAir(want):
		return KindIntruder
	default:
		return KindReplaced
	}
}

type run struct {
	id     int32
	x0, x1 int
}

// Diff compares plan against what live reports, slab by slab, over footprint
// only. Cells the plan never mentions, or the scan cannot vouch for, are left
// out of the damage.
func Diff(plan []Op, live Reader, footprint Box, l arena.Layout) (*Damage, error) {
	if !valid(footprint) {
		return nil, fmt.Errorf("repair: footprint is inverted")
	}
	pal := NewPalette()
	for i := range plan {
		if !contains(footprint, plan[i].Box) {
			return nil, fmt.Errorf("repair: plan box %+v (%s) leaves the arena footprint %+v", plan[i].Box, plan[i].Block, footprint)
		}
	}
	d := &Damage{
		ByKind: map[string]int{}, ByZone: map[string]int{}, ByChange: map[string]int{},
		Footprint: footprint, zoneOf: zoner(l), palette: pal,
	}
	for y0 := footprint.Y0; y0 <= footprint.Y1; y0 += slabHeight {
		slab := footprint
		slab.Y0, slab.Y1 = y0, min(y0+slabHeight-1, footprint.Y1)
		want := NewGrid(slab)
		Paint(want, pal, plan)
		got, err := live.Read(slab, pal)
		if err != nil {
			return nil, fmt.Errorf("repair: scan y %d..%d: %w", slab.Y0, slab.Y1, err)
		}
		if got.Box != slab {
			return nil, fmt.Errorf("repair: scan returned box %+v for %+v", got.Box, slab)
		}
		d.compare(want, got)
	}
	sort.Slice(d.Rects, func(i, j int) bool { return rectLess(d.Rects[i], d.Rects[j]) })
	return d, nil
}

func rectLess(a, b Rect) bool {
	if a.Box.Y0 != b.Box.Y0 {
		return a.Box.Y0 < b.Box.Y0
	}
	if a.Box.Z0 != b.Box.Z0 {
		return a.Box.Z0 < b.Box.Z0
	}
	if a.Box.X0 != b.Box.X0 {
		return a.Box.X0 < b.Box.X0
	}
	return a.Block < b.Block
}

func (d *Damage) compare(want, got *Grid) {
	b := want.Box
	rows := map[[2]int][]run{} // (y, z) -> runs of damaged cells, x ascending
	for y := b.Y0; y <= b.Y1; y++ {
		for z := b.Z0; z <= b.Z1; z++ {
			key := [2]int{y, z}
			for x := b.X0; x <= b.X1; x++ {
				w, g := want.At(x, y, z), got.At(x, y, z)
				if w == Unknown {
					continue
				}
				if g == Unknown {
					d.Unscanned++
					continue
				}
				d.Scanned++
				if w == g {
					continue
				}
				wn, gn := d.palette.Name(w), d.palette.Name(g)
				d.Cells++
				d.ByKind[kindOf(wn, gn)]++
				d.ByZone[d.zoneOf(x, y, z)]++
				d.ByChange[gn+" -> "+wn]++
				if rs := rows[key]; len(rs) > 0 && rs[len(rs)-1].id == w && rs[len(rs)-1].x1 == x-1 {
					rs[len(rs)-1].x1 = x
					continue
				}
				rows[key] = append(rows[key], run{id: w, x0: x, x1: x})
			}
		}
	}
	d.merge(rows, b)
}

// merge joins equal runs on consecutive rows, then equal rectangles on
// consecutive layers, so a collapsed wall becomes a few fills, not thousands.
func (d *Damage) merge(rows map[[2]int][]run, b Box) {
	type xkey struct {
		id     int32
		x0, x1 int
	}
	type vkey struct {
		id             int32
		x0, x1, z0, z1 int
	}
	open := map[vkey]int{} // rectangle still growing upward -> index in d.Rects
	for y := b.Y0; y <= b.Y1; y++ {
		type span struct {
			k      xkey
			z0, z1 int
		}
		var layer []span
		zopen := map[xkey]int{} // index in layer of the span ending at z-1
		for z := b.Z0; z <= b.Z1; z++ {
			next := map[xkey]int{}
			for _, r := range rows[[2]int{y, z}] {
				k := xkey{r.id, r.x0, r.x1}
				if i, ok := zopen[k]; ok {
					layer[i].z1 = z
					next[k] = i
					continue
				}
				layer = append(layer, span{k: k, z0: z, z1: z})
				next[k] = len(layer) - 1
			}
			zopen = next
		}
		nextOpen := map[vkey]int{}
		for _, sp := range layer {
			vk := vkey{sp.k.id, sp.k.x0, sp.k.x1, sp.z0, sp.z1}
			if i, ok := open[vk]; ok {
				d.Rects[i].Box.Y1 = y
				nextOpen[vk] = i
				continue
			}
			d.Rects = append(d.Rects, Rect{
				Box:   Box{X0: sp.k.x0, Y0: y, Z0: sp.z0, X1: sp.k.x1, Y1: y, Z1: sp.z1},
				Block: d.palette.Name(sp.k.id),
			})
			nextOpen[vk] = len(d.Rects) - 1
		}
		open = nextOpen
	}
}
