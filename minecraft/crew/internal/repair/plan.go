package repair

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// maxFill is Bedrock's per-command block limit for /fill.
const maxFill = 32768

// maxTickingAreas is the Bedrock cap on ticking areas in a world.
const maxTickingAreas = 10

// tileChunks is the ticking-area side in chunks; 10 x 10 is the 100-chunk cap.
const tileChunks = 10

// tickingAreaPrefix names the areas this tool adds.
const tickingAreaPrefix = "crew-repair-"

// lateMarkers name blocks that need a neighbour to hold them (attachables,
// fluids, powered parts). They are placed after every solid block, as the
// generator does, or the server would pop them off.
var lateMarkers = []string{
	"lantern", "end_rod", "water", "lava", "redstone_wire", "redstone_lamp", "carpet",
	"web", "torch", "lever", "button", "door", "ladder", "banner", "sign", "repeater",
	"comparator", "piston", "pressure_plate", "rail", "vine", "rod", "chain",
}

// IsLate reports whether a block must follow the solid ones.
func IsLate(block string) bool {
	// Block states can contain marker words ("open_bit", "vine_..."), so
	// match the name alone.
	if i := strings.IndexAny(block, " ["); i >= 0 {
		block = block[:i]
	}
	for _, m := range lateMarkers {
		if strings.Contains(block, m) {
			return true
		}
	}
	return false
}

// Plan is the ordered command list for one repair.
type Plan struct {
	Setup    []string // ticking areas so the chunks are loaded
	Repairs  []string // idempotent: absolute fills and setblocks
	Teardown []string // removes the ticking areas
}

// Commands lists every command in run order.
func (p Plan) Commands() []string {
	out := append([]string{}, p.Setup...)
	out = append(out, p.Repairs...)
	return append(out, p.Teardown...)
}

// BuildPlan orders rects and writes their commands. Order: first the blocks
// that must go (planned air, so debris and intruders are cleared before
// anything is set), then solids bottom-up so each rests on what is below, then
// the late blocks bottom-up. Every command is checked against the footprint.
func BuildPlan(rects []Rect, footprint Box) (Plan, error) {
	stage := func(r Rect) int {
		switch {
		case IsAir(r.Block):
			return 0
		case IsLate(r.Block):
			return 2
		default:
			return 1
		}
	}
	rs := append([]Rect{}, rects...)
	sort.SliceStable(rs, func(i, j int) bool {
		if si, sj := stage(rs[i]), stage(rs[j]); si != sj {
			return si < sj
		}
		return rectLess(rs[i], rs[j])
	})
	var p Plan
	for _, r := range rs {
		if !contains(footprint, r.Box) {
			return Plan{}, fmt.Errorf("repair: %s at %+v leaves the arena footprint %+v", r.Block, r.Box, footprint)
		}
		for _, piece := range split(r.Box) {
			p.Repairs = append(p.Repairs, command(piece, r.Block))
		}
	}
	var err error
	if p.Setup, p.Teardown, err = tickingTiles(rs); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func command(b Box, block string) string {
	if b.X0 == b.X1 && b.Y0 == b.Y1 && b.Z0 == b.Z1 {
		return fmt.Sprintf("setblock %d %d %d %s", b.X0, b.Y0, b.Z0, block)
	}
	return fmt.Sprintf("fill %d %d %d %d %d %d %s", b.X0, b.Y0, b.Z0, b.X1, b.Y1, b.Z1, block)
}

// split cuts a box into layers that stay under the fill limit.
func split(b Box) []Box {
	dx, _, dz := dims(b)
	step := max(1, maxFill/(dx*dz))
	var out []Box
	for y := b.Y0; y <= b.Y1; y += step {
		c := b
		c.Y0, c.Y1 = y, min(y+step-1, b.Y1)
		out = append(out, c)
	}
	// A single layer can still be too big: halve it along x until it fits.
	var fit []Box
	for _, c := range out {
		fit = append(fit, halve(c)...)
	}
	return fit
}

func halve(b Box) []Box {
	dx, dy, dz := dims(b)
	if dx*dy*dz <= maxFill {
		return []Box{b}
	}
	if dx >= dz {
		m := b.X0 + dx/2 - 1
		l, r := b, b
		l.X1, r.X0 = m, m+1
		return append(halve(l), halve(r)...)
	}
	m := b.Z0 + dz/2 - 1
	l, r := b, b
	l.Z1, r.Z0 = m, m+1
	return append(halve(l), halve(r)...)
}

// tickingTiles adds a chunk-aligned ticking area for each 10 x 10 chunk tile
// that holds a repair, so only the damaged part of the arena is loaded.
func tickingTiles(rects []Rect) (setup, teardown []string, err error) {
	const side = tileChunks * 16
	type tile struct{ x, z int }
	seen := map[tile]bool{}
	for _, r := range rects {
		for x := floorDiv(r.Box.X0, side); x <= floorDiv(r.Box.X1, side); x++ {
			for z := floorDiv(r.Box.Z0, side); z <= floorDiv(r.Box.Z1, side); z++ {
				seen[tile{x, z}] = true
			}
		}
	}
	if len(seen) > maxTickingAreas {
		return nil, nil, fmt.Errorf("repair: damage spans %d ticking tiles, the server allows %d areas", len(seen), maxTickingAreas)
	}
	tiles := make([]tile, 0, len(seen))
	for t := range seen {
		tiles = append(tiles, t)
	}
	sort.Slice(tiles, func(i, j int) bool {
		if tiles[i].x != tiles[j].x {
			return tiles[i].x < tiles[j].x
		}
		return tiles[i].z < tiles[j].z
	})
	for i, t := range tiles {
		name := fmt.Sprintf("%s%d", tickingAreaPrefix, i)
		setup = append(setup, fmt.Sprintf("tickingarea add %d 0 %d %d 0 %d %s",
			t.x*side, t.z*side, t.x*side+side-1, t.z*side+side-1, name))
		teardown = append(teardown, "tickingarea remove "+name)
	}
	return setup, teardown, nil
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Compute diffs the plan against a scan and builds the repair in one step.
func Compute(plan []Op, live Reader, l arena.Layout) (*Damage, Plan, Report, error) {
	fp := Footprint(l)
	d, err := Diff(plan, live, fp, l)
	if err != nil {
		return nil, Plan{}, Report{}, err
	}
	p, err := BuildPlan(d.Rects, fp)
	if err != nil {
		return nil, Plan{}, Report{}, err
	}
	return d, p, NewReport(d, p), nil
}
