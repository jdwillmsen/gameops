package main

import (
	"fmt"
	"strings"
)

// maxFill is Bedrock's per-command block limit for /fill.
const maxFill = 32768

// maxAreaChunks is Bedrock's chunk limit for one ticking area.
const maxAreaChunks = 100

// ground is the grass level of the lab's flat square; everything stands on it.
const ground = 63

// builder collects console commands in the order the server must run them.
type builder struct {
	cmds []string
}

func (b *builder) raw(format string, args ...any) {
	b.cmds = append(b.cmds, fmt.Sprintf(format, args...))
}

// set places one block as a one-block fill: setblock onto an identical block
// fails, which would turn every rebuild into a wall of errors, while the fill
// just reports "0 blocks filled".
func (b *builder) set(x, y, z int, block string) {
	b.raw("fill %d %d %d %d %d %d %s", x, y, z, x, y, z, block)
}

// door uses setblock, which also places the upper half; a fill would leave
// a lone lower half.
func (b *builder) door(x, y, z int, block string) {
	b.raw("setblock %d %d %d %s", x, y, z, block)
}

// fill accepts corners in any order and splits boxes over the block limit
// into horizontal slabs, so callers can describe shapes naturally.
func (b *builder) fill(x0, y0, z0, x1, y1, z1 int, block string, mode ...string) {
	x0, x1 = order(x0, x1)
	y0, y1 = order(y0, y1)
	z0, z1 = order(z0, z1)
	layer := (x1 - x0 + 1) * (z1 - z0 + 1)
	if layer > maxFill {
		mx := (x0 + x1) / 2
		b.fill(x0, y0, z0, mx, y1, z1, block, mode...)
		b.fill(mx+1, y0, z0, x1, y1, z1, block, mode...)
		return
	}
	step := maxFill / layer
	suffix := ""
	if len(mode) > 0 {
		suffix = " " + strings.Join(mode, " ")
	}
	for y := y0; y <= y1; y += step {
		top := min(y+step-1, y1)
		b.raw("fill %d %d %d %d %d %d %s%s", x0, y, z0, x1, top, z1, block, suffix)
	}
}

func order(a, b int) (int, int) {
	if a > b {
		return b, a
	}
	return a, b
}

// box is an x/z rectangle, inclusive on both ends.
type box struct{ x0, z0, x1, z1 int }

// tickingAreas covers r with rectangles of whole chunks, each within the
// 100-chunk limit, so every command in r lands in a loaded chunk.
func tickingAreas(r box) []box {
	cx0, cx1 := r.x0>>4, r.x1>>4
	cz0, cz1 := r.z0>>4, r.z1>>4
	var out []box
	w := min(cx1-cx0+1, maxAreaChunks)
	rows := maxAreaChunks / w
	for cx := cx0; cx <= cx1; cx += w {
		for cz := cz0; cz <= cz1; cz += rows {
			out = append(out, box{cx * 16, cz * 16, min(cx+w-1, cx1)*16 + 15, min(cz+rows-1, cz1)*16 + 15})
		}
	}
	return out
}

// grid lays out a 3x3 block of square cells separated by roads, the shared
// shape of the test grounds and the course field.
type grid struct {
	x0, z0, cell, road, n int
}

func (g grid) cellAt(i, j int) (int, int) {
	return g.x0 + g.road + i*(g.cell+g.road), g.z0 + g.road + j*(g.cell+g.road)
}

func (g grid) bounds() box {
	span := g.n*(g.cell+g.road) + g.road - 1
	return box{g.x0, g.z0, g.x0 + span, g.z0 + span}
}

// base clears the air above the grid, lays roads with sea lanterns, and
// outlines each cell in its colour.
func (g grid) base(b *builder, colors [][]string) {
	r := g.bounds()
	b.fill(r.x0, ground+1, r.z0, r.x1, ground+32, r.z1, "air")
	for k := 0; k <= g.n; k++ {
		a := g.x0 + k*(g.cell+g.road)
		b.fill(a, ground, r.z0, a+g.road-1, ground, r.z1, "polished_andesite")
		c := g.z0 + k*(g.cell+g.road)
		b.fill(r.x0, ground, c, r.x1, ground, c+g.road-1, "polished_andesite")
	}
	for k := 0; k <= g.n; k++ {
		a := g.x0 + k*(g.cell+g.road) + g.road/2
		for z := r.z0 + 2; z < r.z1; z += 8 {
			b.set(a, ground, z, "sea_lantern")
		}
		c := g.z0 + k*(g.cell+g.road) + g.road/2
		for x := r.x0 + 2; x < r.x1; x += 8 {
			b.set(x, ground, c, "sea_lantern")
		}
	}
	for j, row := range colors {
		for i, color := range row {
			x, z := g.cellAt(i, j)
			e := g.cell - 1
			blk := color + "_concrete"
			b.fill(x, ground, z, x+e, ground, z, blk)
			b.fill(x, ground, z+e, x+e, ground, z+e, blk)
			b.fill(x, ground, z, x, ground, z+e, blk)
			b.fill(x+e, ground, z, x+e, ground, z+e, blk)
		}
	}
}

// floor paves a cell inside its border.
func (g grid) floor(b *builder, i, j int, block string) {
	x, z := g.cellAt(i, j)
	b.fill(x+1, ground, z+1, x+g.cell-2, ground, z+g.cell-2, block)
}

// lights hangs invisible light blocks over a cell: grass cells otherwise
// spawn hostile mobs at night, which kill crew units mid-test.
func (g grid) lights(b *builder, i, j, step, y int) {
	x, z := g.cellAt(i, j)
	for a := x + 4; a < x+g.cell-2; a += step {
		for c := z + 4; c < z+g.cell-2; c += step {
			b.set(a, y, c, "light_block_15")
		}
	}
}
