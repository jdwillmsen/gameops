package main

import "math/rand"

// treesGrid is the tree field west of the course field.
var treesGrid = grid{x0: -444, z0: -500, cell: 40, road: 6, n: 3}

// treeClear is how high above the ground a rebuild clears: giant jungle and
// spruce trees grow past the 32 blocks the other fields clear.
const treeClear = 48

// buildTrees: fixed oaks, natural species, giants / dense oak forest,
// terraces, mangrove swamp / nursery, lumber yard, nether grove.
//
// Only the fixed oaks and the terraces are hand-built and identical on every
// rebuild; the rest are the game's own tree features, which grow a different
// shape each time, like trees in a real world.
func buildTrees(b *builder) {
	g := treesGrid
	r := g.bounds()
	b.fill(r.x0, ground+33, r.z0, r.x1, ground+treeClear, r.z1, "air")
	g.base(b, [][]string{{"brown", "green", "lime"}, {"green", "yellow", "cyan"}, {"white", "orange", "red"}})
	for _, z := range []int{-440, -402} { // paths on to the course field
		b.fill(r.x1+1, ground, z, courseGrid.x0-1, ground, z+4, "grass_path")
		b.fill(r.x1+1, ground+1, z, courseGrid.x0-1, ground+4, z+4, "air")
	}
	for j := range 3 {
		for i := range 3 {
			soil(b, g, i, j)
		}
	}
	fixedOaks(b, g, 0, 0)
	arboretum(b, g, 1, 0)
	giants(b, g, 2, 0)
	denseOaks(b, g, 0, 1)
	terraces(b, g, 1, 1)
	swamp(b, g, 2, 1)
	nursery(b, g, 0, 2)
	lumberYard(b, g, 1, 2)
	netherGrove(b, g, 2, 2)
	for j := range 3 {
		for i := range 3 {
			lightsAround(b, g, i, j)
		}
	}
}

func treesAreas() []box {
	r := treesGrid.bounds()
	return append(tickingAreas(r), tickingAreas(box{r.x1 + 1, -448, courseGrid.x0 - 1, -392})...)
}

// soil resets a cell's ground to dirt under grass: felled trees, roots and
// dug pits all change it.
func soil(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	b.fill(x+1, ground-8, z+1, x+g.cell-2, ground-1, z+g.cell-2, "dirt")
	g.floor(b, i, j, "grass")
}

// lightsAround fills the air just above head height with invisible light,
// after the trees grow so it never displaces a leaf or log: canopies shade
// the ground enough for hostile mobs to spawn under them.
func lightsAround(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	for a := x + 3; a < x+g.cell-2; a += 7 {
		for c := z + 3; c < z+g.cell-2; c += 7 {
			b.fill(a, ground+3, c, a, ground+3, c, "light_block_15", "keep")
		}
	}
}

// feature grows one of the game's tree features rooted at x, ground+1, z.
func feature(b *builder, name string, x, z int) {
	b.raw("place feature minecraft:%s %d %d %d", name, x, ground+1, z)
}

// oak builds an oak with a trunk of the given height on top of y and a
// round canopy around its top, the shape of a small natural oak.
func oak(b *builder, x, y, z, height int) {
	top := y + height
	leaves := "oak_leaves"
	b.fill(x-2, top-2, z-2, x+2, top-1, z+2, leaves)
	for _, d := range [][2]int{{-2, -2}, {-2, 2}, {2, -2}, {2, 2}} {
		b.set(x+d[0], top-1, z+d[1], "air")
	}
	b.fill(x-1, top, z-1, x+1, top+1, z+1, leaves)
	for _, d := range [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}} {
		b.set(x+d[0], top+1, z+d[1], "air")
	}
	b.fill(x, y+1, z, x, top, z, "oak_log")
	b.set(x, y, z, "dirt")
}

// fixedOaks: nine hand-built oaks, each harder to fell than the last, the
// same blocks on every rebuild so a felling test can count its logs.
//
//	1 trunk 4     2 trunk 6        3 trunk 12, beyond reach without a pillar
//	4 branches    5 2x2 trunk 10   6 against a wall
//	7 on a ledge  8 in a pit       9 in shallow water
func fixedOaks(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	slot := func(k int) (int, int) { return x + 8 + 12*(k%3), z + 8 + 12*(k/3) }
	for k := range 9 {
		a, c := slot(k)
		heightMarker(b, a-2, c-4, k+1)
	}

	a, c := slot(0)
	oak(b, a, ground, c, 4)
	a, c = slot(1)
	oak(b, a, ground, c, 6)
	a, c = slot(2)
	oak(b, a, ground, c, 12)

	a, c = slot(3)
	oak(b, a, ground, c, 8)
	b.fill(a+1, ground+5, c, a+3, ground+5, c, `oak_log ["pillar_axis"="x"]`)
	b.fill(a, ground+6, c-3, a, ground+6, c-1, `oak_log ["pillar_axis"="z"]`)
	b.fill(a+2, ground+6, c-1, a+4, ground+6, c+1, "oak_leaves")
	b.fill(a-1, ground+7, c-4, a+1, ground+7, c-2, "oak_leaves")

	a, c = slot(4)
	b.fill(a-2, ground+9, c-2, a+3, ground+10, c+3, "oak_leaves")
	b.fill(a-1, ground+11, c-1, a+2, ground+12, c+2, "oak_leaves")
	b.fill(a, ground+1, c, a+1, ground+10, c+1, "oak_log")
	b.fill(a, ground, c, a+1, ground, c+1, "dirt")

	a, c = slot(5)
	oak(b, a, ground, c-1, 6)
	b.fill(a-2, ground+1, c-3, a+2, ground+8, c-3, "stone_bricks") // cuts the canopy

	a, c = slot(6)
	b.fill(a-2, ground+1, c-2, a+2, ground+3, c+2, "stone_bricks")
	b.fill(a-2, ground+1, c+3, a-2, ground+3, c+3, `ladder ["facing_direction"=3]`)
	oak(b, a, ground+3, c, 5)

	a, c = slot(7)
	b.fill(a-2, ground-3, c-2, a+2, ground, c+2, "air")
	b.fill(a-2, ground-4, c-2, a+2, ground-4, c+2, "grass")
	b.fill(a+2, ground-3, c-2, a+2, ground, c-2, `ladder ["facing_direction"=2]`)
	oak(b, a, ground-4, c, 7)

	a, c = slot(8)
	b.fill(a-2, ground, c-2, a+2, ground, c+2, "water")
	b.fill(a-2, ground-1, c-2, a+2, ground-1, c+2, "dirt")
	oak(b, a, ground, c, 5)
}

// arboretum: one natural tree of each ordinary species, four rows of four.
func arboretum(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	species := []string{
		"oak_tree_feature", "fancy_oak_tree_feature", "birch_tree_feature", "super_birch_tree_feature",
		"spruce_tree_feature", "pine_tree_feature", "jungle_tree_feature", "jungle_bush_feature",
		"cherry_tree_feature", "azalea_tree_feature", "swamp_tree_feature", "savanna_tree_feature",
		"oak_tree_with_vines_feature", "pale_oak_tree_feature", "mangrove_tree_feature", "fallen_oak_tree_feature",
	}
	for k, s := range species {
		feature(b, s, x+6+9*(k%4), z+6+9*(k/4))
	}
}

// giants: the two-by-two trees, spaced so their canopies barely touch.
func giants(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	species := []string{
		"mega_jungle_tree_feature", "mega_spruce_tree_feature", "mega_pine_tree_feature",
		"roofed_tree_feature", "tall_mangrove_tree_feature", "roofed_tree_feature",
		"mega_pine_tree_feature", "pale_oak_tree_feature", "mega_jungle_tree_feature",
	}
	for k, s := range species {
		a, c := x+7+12*(k%3), z+7+12*(k/3)
		b.fill(a-1, ground, c-1, a+2, ground, c+2, "podzol")
		feature(b, s, a, c)
	}
}

// denseOaks: a close-packed oak wood, natural oaks four to six blocks apart,
// to fell and walk through in clutter. A chest at the gate takes the logs.
func denseOaks(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	rng := rand.New(rand.NewSource(11))
	var placed [][2]int
	for range 400 {
		a, c := x+4+rng.Intn(g.cell-8), z+6+rng.Intn(g.cell-10)
		ok := true
		for _, p := range placed {
			if dx, dz := p[0]-a, p[1]-c; dx*dx+dz*dz < 16 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		placed = append(placed, [2]int{a, c})
		s := "oak_tree_feature"
		if rng.Intn(5) == 0 {
			s = "fancy_oak_tree_feature"
		}
		feature(b, s, a, c)
	}
	b.set(x+2, ground+1, z+2, `chest ["minecraft:cardinal_direction"="south"]`)
}

// terraces: five grass steps rising one block each toward the east, joined
// by stairs, with two fixed oaks on every step.
func terraces(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	for s := 1; s <= 5; s++ {
		a := x + 3 + 7*(s-1)
		b.fill(a, ground+1, z+2, a+6, ground+s, z+g.cell-3, "dirt")
		b.fill(a, ground+s, z+2, a+6, ground+s, z+g.cell-3, "grass")
		b.fill(a, ground+s, z+18, a, ground+s, z+20, `oak_stairs ["weirdo_direction"=0,"upside_down_bit"=false]`)
		oak(b, a+3, ground+s, z+9, 5)
		oak(b, a+3, ground+s, z+30, 5)
	}
}

// swamp: a shallow mangrove swamp of mud islands in water, the hardest
// footing in the field.
func swamp(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	e := g.cell - 2
	b.fill(x+1, ground-1, z+1, x+e, ground-1, z+e, "mud")
	b.fill(x+1, ground, z+1, x+e, ground, z+e, "water")
	rng := rand.New(rand.NewSource(5))
	for a := x + 5; a < x+e-3; a += 9 {
		for c := z + 5; c < z+e-3; c += 9 {
			b.fill(a-1, ground, c-1, a+1, ground, c+1, "mud")
			b.set(a, ground, c, "grass")
			if rng.Intn(3) > 0 {
				feature(b, "mangrove_tree_feature", a, c)
			}
		}
	}
	for range 40 {
		a, c := x+2+rng.Intn(e-2), z+2+rng.Intn(e-2)
		b.fill(a, ground+1, c, a, ground+1, c, "waterlily", "keep")
	}
}

// nursery: a row of saplings of each species, two-by-two squares for the
// big trees, water to till by, and a chest of bone meal and saplings to
// test replanting.
func nursery(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	saplings := []string{"oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "cherry", "pale_oak"}
	for k, s := range saplings {
		c := z + 4 + 3*k
		for a := x + 4; a < x+26; a += 3 {
			b.set(a, ground+1, c, s+"_sapling")
		}
	}
	b.fill(x+3, ground, z+29, x+g.cell-4, ground, z+29, "water")
	for k, s := range []string{"dark_oak", "spruce", "jungle", "pale_oak"} {
		a := x + 29
		c := z + 4 + 6*k
		b.fill(a, ground, c, a+1, ground, c+1, "podzol")
		b.fill(a, ground+1, c, a+1, ground+1, c+1, s+"_sapling")
	}
	chest := [2]int{x + 4, z + 33}
	b.set(chest[0], ground+1, chest[1], `chest ["minecraft:cardinal_direction"="north"]`)
	for slot, it := range []string{"bone_meal 64", "oak_sapling 64", "spruce_sapling 64", "birch_sapling 64", "iron_hoe 1", "water_bucket 1"} {
		b.raw("replaceitem block %d %d %d slot.container %d %s", chest[0], ground+1, chest[1], slot, it)
	}
}

// lumberYard: where felled logs go — deposit chests on coloured pads, a
// stocked chest of axes, workstations, a log pile to break, fallen trees.
func lumberYard(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	south := `["minecraft:cardinal_direction"="south"]`
	for k, c := range []string{"white", "orange", "lime", "light_blue"} {
		a := x + 4 + 4*k
		b.set(a, ground, z+4, c+"_concrete")
		b.set(a, ground+1, z+4, "chest "+south)
	}
	stocked := [2]int{x + 22, z + 4}
	b.set(stocked[0], ground+1, stocked[1], "chest "+south)
	for slot, it := range []string{"iron_axe", "iron_axe", "stone_axe", "stone_axe", "shears"} {
		b.raw("replaceitem block %d %d %d slot.container %d %s 1", stocked[0], ground+1, stocked[1], slot, it)
	}
	for k, s := range []string{"crafting_table", "furnace " + south, "smoker " + south, "stonecutter_block"} {
		b.set(x+26+2*k, ground+1, z+4, s)
	}
	axes := []string{`oak_log ["pillar_axis"="y"]`, `oak_log ["pillar_axis"="x"]`, `oak_log ["pillar_axis"="z"]`}
	for k := range 3 { // a stepped log pile, every face of a log exposed somewhere
		b.fill(x+4, ground+1+k, z+10+k, x+15, ground+1+k, z+15, axes[k])
	}
	b.fill(x+20, ground+1, z+10, x+20, ground+4, z+10, "stripped_oak_log")
	b.fill(x+22, ground+1, z+10, x+22, ground+4, z+10, "birch_log")
	b.fill(x+24, ground+1, z+10, x+24, ground+4, z+10, "spruce_log")
	for k, s := range []string{"fallen_oak_tree_feature", "fallen_birch_tree_feature", "fallen_spruce_tree_feature", "fallen_jungle_tree_feature"} {
		feature(b, s, x+6+8*k, z+28)
	}
}

// netherGrove: huge crimson and warped fungi on their own nylium, the stems
// a unit must not mistake for logs. They are hand-built: the game's fungus
// feature only places the small plant.
func netherGrove(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	h := g.cell / 2
	b.fill(x+1, ground-2, z+1, x+g.cell-2, ground-1, z+g.cell-2, "netherrack")
	b.fill(x+1, ground, z+1, x+h-1, ground, z+g.cell-2, "crimson_nylium")
	b.fill(x+h, ground, z+1, x+g.cell-2, ground, z+g.cell-2, "warped_nylium")
	n := 0
	for a := x + 5; a < x+g.cell-3; a += 9 {
		for c := z + 5; c < z+g.cell-3; c += 9 {
			kind, wart := "crimson", "nether_wart_block"
			if a >= x+h {
				kind, wart = "warped", "warped_wart_block"
			}
			hugeFungus(b, a, c, 5+n%4, kind+"_stem", wart)
			n++
		}
	}
}

// hugeFungus: a stem of the given height under a wart cap that hangs down
// round its edge, lit by shroomlights inside.
func hugeFungus(b *builder, x, z, height int, stem, wart string) {
	top := ground + height
	b.fill(x-2, top-2, z-2, x+2, top, z+2, wart)
	b.fill(x-1, top-2, z-1, x+1, top-1, z+1, "air")
	b.fill(x-1, top+1, z-1, x+1, top+1, z+1, wart)
	b.set(x+1, top-1, z, "shroomlight")
	b.set(x-1, top-1, z, "shroomlight")
	b.fill(x, ground+1, z, x, top, z, stem)
}
