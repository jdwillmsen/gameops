package main

import "fmt"

// courseGrid is the course field west of the spawn hub, mirroring the grounds.
var courseGrid = grid{x0: -294, z0: -500, cell: 40, road: 6, n: 3}

// padSize is the side of a small blank pad; four sit in a cell.
const padSize = 16

// buildCourses: physics drops and gaps, physics hazards, shapes / pads 1-12
// / large pads 13-15.
func buildCourses(b *builder) {
	g := courseGrid
	g.base(b, [][]string{{"orange", "red", "cyan"}, {"white", "white", "white"}, {"light_gray", "light_gray", "light_gray"}})
	for _, z := range []int{-440, -402} { // paths from the hub's west gates
		b.fill(g.bounds().x1+1, ground, z, -141, ground, z+4, "grass_path")
		b.fill(g.bounds().x1+1, ground+1, z, -141, ground+4, z+4, "air")
	}
	drops(b, g, 0, 0)
	physicsHazards(b, g, 1, 0)
	shapes(b, g, 2, 0)
	n := 1
	for i := range 3 {
		x, z := g.cellAt(i, 1)
		for k := range 4 {
			pad(b, x+3+(k%2)*(padSize+2), z+3+(k/2)*(padSize+2), padSize, n)
			n++
		}
	}
	for i := range 3 {
		x, z := g.cellAt(i, 2)
		pad(b, x+1, z+1, g.cell-2, n)
		n++
	}
}

func coursesAreas() []box {
	r := courseGrid.bounds()
	return append(tickingAreas(box{r.x0, r.z0, r.x1, r.z1}), tickingAreas(box{r.x1 + 1, -448, -141, -392})...)
}

// drops: ledges 1-4 high to walk off, trenches 1-4 wide to cross, and jump
// steps rising one block at a time.
func drops(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	for h := 1; h <= 4; h++ {
		a := x + 3 + 6*(h-1)
		b.fill(a, ground+1, z+3, a+3, ground+h, z+9, "stone_bricks")
		b.fill(a, ground+1, z+10, a+3, ground+h, z+10, `ladder ["facing_direction"=3]`)
		heightMarker(b, a, z+2, h)
	}
	for w := 1; w <= 4; w++ { // trenches across a lane, two deep
		a := x + 3 + 7*(w-1)
		b.fill(a, ground-2, z+15, a+w-1, ground, z+19, "air")
		b.fill(a, ground-3, z+15, a+w-1, ground-3, z+19, "stone")
		heightMarker(b, a, z+14, w)
	}
	for k := range 6 { // jump steps: each one block higher, one block apart
		a := x + 3 + 3*k
		b.fill(a, ground+1, z+24, a+1, ground+1+k, z+26, "polished_andesite")
	}
	a := x + 3
	for _, gap := range []int{1, 2, 3, 4} { // raised gaps 1-4 at the same height
		b.fill(a, ground+1, z+31, a+2, ground+2, z+35, "stone_bricks")
		a += 3 + gap
	}
	b.fill(a, ground+1, z+31, a+2, ground+2, z+35, "stone_bricks")
}

// heightMarker sets n lime blocks in the floor so a camera can read a size.
func heightMarker(b *builder, x, z, n int) {
	b.fill(x, ground, z, x+n-1, ground, z, "lime_concrete")
}

// physicsHazards: a pit with a ladder out, low ceilings, a water pool and
// lava sealed behind glass.
func physicsHazards(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	b.fill(x+3, ground-5, z+3, x+9, ground, z+9, "stone") // pit 5x5, 5 deep
	b.fill(x+4, ground-4, z+4, x+8, ground, z+8, "air")
	b.fill(x+6, ground-4, z+4, x+6, ground, z+4, `ladder ["facing_direction"=3]`)
	// Tunnels: 3 clear, 2 clear, and 1.5 clear under top slabs (sneak height).
	for k, clear := range []int{3, 2, 1} {
		c := z + 3 + 5*k
		b.fill(x+14, ground+1, c, x+30, ground+1+clear, c+2, "glass")
		b.fill(x+14, ground+1, c+1, x+30, ground+clear, c+1, "air")
		if clear == 1 {
			b.fill(x+14, ground+2, c+1, x+30, ground+2, c+1, `stone_brick_slab ["minecraft:vertical_half"="top"]`)
		}
	}
	b.fill(x+3, ground-2, z+20, x+14, ground, z+30, "stone") // pool 10x9, 2 deep
	b.fill(x+4, ground-1, z+21, x+13, ground, z+29, "water")
	b.fill(x+20, ground-1, z+20, x+36, ground, z+30, "stone") // lava behind glass
	b.fill(x+21, ground, z+21, x+35, ground, z+29, "lava")
	b.fill(x+20, ground+1, z+20, x+36, ground+3, z+30, "glass")
	b.fill(x+21, ground+1, z+21, x+35, ground+2, z+29, "air")
	b.fill(x+3, ground+1, z+34, x+36, ground+1, z+36, "magma") // hot floor lane
}

// shapes: every partial-block shape a unit must path over or through.
func shapes(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	// Lane 1: slabs, bottom then top, then a double slab, repeating up a ramp.
	for k := range 9 {
		h := k / 3
		blk := []string{
			`stone_brick_slab ["minecraft:vertical_half"="bottom"]`,
			`stone_brick_slab ["minecraft:vertical_half"="top"]`,
			"stone_brick_double_slab",
		}[k%3]
		if h > 0 {
			b.fill(x+3+2*k, ground+1, z+3, x+4+2*k, ground+h, z+5, "stone_bricks")
		}
		b.fill(x+3+2*k, ground+1+h, z+3, x+4+2*k, ground+1+h, z+5, blk)
	}
	// Lane 2: stairs up and down in all four facings, plus upside-down.
	for d := range 4 {
		for s := range 4 {
			b.set(x+3+5*d, ground+1+s, z+9+s, fmt.Sprintf(`stone_brick_stairs ["weirdo_direction"=%d,"upside_down_bit"=false]`, d))
		}
		b.set(x+4+5*d, ground+3, z+9, fmt.Sprintf(`oak_stairs ["weirdo_direction"=%d,"upside_down_bit"=true]`, d))
	}
	b.fill(x+24, ground+1, z+9, x+28, ground+1, z+9, `oak_stairs ["weirdo_direction"=2,"upside_down_bit"=false]`)
	b.fill(x+24, ground+1, z+10, x+28, ground+1, z+12, "oak_planks")
	// Lane 3: fences and walls to walk around, gates to open.
	fences := []string{"oak_fence", "spruce_fence", "nether_brick_fence", "cobblestone_wall", "mossy_cobblestone_wall", "stone_brick_wall", "iron_bars", "glass_pane"}
	for k, f := range fences {
		b.fill(x+3+4*k, ground+1, z+16, x+5+4*k, ground+1, z+16, f)
	}
	for k := range 4 {
		b.set(x+3+4*k, ground+1, z+19, fmt.Sprintf(`fence_gate ["direction"=%d,"open_bit"=%t]`, k, k%2 == 1))
	}
	// Lane 4: ladders on each face of a pillar, vines, scaffolding.
	b.fill(x+22, ground+1, z+19, x+24, ground+6, z+21, "stone_bricks")
	for _, l := range []struct{ dx, dz, face int }{{1, -1, 2}, {1, 3, 3}, {-1, 1, 4}, {3, 1, 5}} {
		b.fill(x+22+l.dx, ground+1, z+19+l.dz, x+22+l.dx, ground+6, z+19+l.dz, fmt.Sprintf(`ladder ["facing_direction"=%d]`, l.face))
	}
	b.fill(x+30, ground+1, z+19, x+30, ground+6, z+19, "scaffolding")
	// Lane 5: doors and trapdoors, closed and open, in a wall to pass through.
	b.fill(x+3, ground+1, z+26, x+36, ground+3, z+26, "stone_bricks")
	doors := []string{"wooden_door", "spruce_door", "birch_door", "iron_door"}
	for k := range 8 {
		d := doors[k%4]
		a := x + 4 + 4*k
		open := k >= 4
		b.fill(a, ground+1, z+26, a, ground+2, z+26, "air")
		b.door(a, ground+1, z+26, fmt.Sprintf(`%s ["direction"=1,"upper_block_bit"=false,"open_bit"=%t]`, d, open))
	}
	for k := range 6 { // trapdoors in the floor: closed ones hold, open ones drop
		b.set(x+4+5*k, ground, z+30, fmt.Sprintf(`trapdoor ["direction"=0,"open_bit"=%t,"upside_down_bit"=false]`, k%2 == 1))
		b.set(x+4+5*k, ground-1, z+30, "air")
	}
	// Lane 6: thin floor blocks: carpet, snow layers, pressure plates, rails.
	thin := []string{"white_carpet", `snow_layer ["height"=0]`, `snow_layer ["height"=3]`, `snow_layer ["height"=6]`,
		"stone_pressure_plate", "rail", "moss_carpet", "daylight_detector", "lantern", "flower_pot", "candle", "chain"}
	for k, s := range thin {
		b.set(x+3+3*k, ground+1, z+34, s)
	}
}

// pad lays a flat numbered pad that a test may clear and rebuild every run.
// The number is drawn in the north-west corner in a 3x5 digit font.
func pad(b *builder, x, z, size, n int) {
	b.fill(x, ground+1, z, x+size-1, ground+16, z+size-1, "air")
	b.fill(x, ground, z, x+size-1, ground, z+size-1, "white_concrete")
	b.fill(x, ground, z, x+size-1, ground, z, "light_gray_concrete")
	b.fill(x, ground, z+size-1, x+size-1, ground, z+size-1, "light_gray_concrete")
	b.fill(x, ground, z, x, ground, z+size-1, "light_gray_concrete")
	b.fill(x+size-1, ground, z, x+size-1, ground, z+size-1, "light_gray_concrete")
	digits := fmt.Sprint(n)
	for k, r := range digits {
		glyph := font[r-'0']
		for row, line := range glyph {
			for col, c := range line {
				if c == '#' {
					b.set(x+2+4*k+col, ground, z+2+row, "black_concrete")
				}
			}
		}
	}
}

var font = [10][5]string{
	{"###", "#.#", "#.#", "#.#", "###"},
	{".#.", "##.", ".#.", ".#.", "###"},
	{"###", "..#", "###", "#..", "###"},
	{"###", "..#", "###", "..#", "###"},
	{"#.#", "#.#", "###", "..#", "..#"},
	{"###", "#..", "###", "..#", "###"},
	{"###", "#..", "###", "#.#", "###"},
	{"###", "..#", ".#.", ".#.", ".#."},
	{"###", "#.#", "###", "#.#", "###"},
	{"###", "#.#", "###", "..#", "###"},
}
