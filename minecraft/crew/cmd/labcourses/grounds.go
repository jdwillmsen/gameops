package main

import (
	"math/rand"
	"strings"
)

// groundsGrid is the work-yard field east of the spawn hub.
var groundsGrid = grid{x0: 150, z0: -500, cell: 40, road: 6, n: 3}

var dyeColors = []string{"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black"}

// buildGrounds: tree grove, ore quarry, hazards / storage, plaza, redstone /
// farm, animal pens, pathfinding course.
func buildGrounds(b *builder) {
	g := groundsGrid
	g.base(b, [][]string{{"green", "gray", "blue"}, {"yellow", "white", "red"}, {"lime", "brown", "purple"}})
	for _, z := range []int{-440, -402} { // paths from the hub's east gates
		b.fill(141, ground, z, g.x0-1, ground, z+4, "grass_path")
		b.fill(141, ground+1, z, g.x0-1, ground+4, z+4, "air")
	}
	grove(b, g, 0, 0)
	quarry(b, g, 1, 0)
	hazards(b, g, 2, 0)
	storage(b, g, 0, 1)
	plaza(b, g, 1, 1)
	redstone(b, g, 2, 1)
	farm(b, g, 0, 2)
	pens(b, g, 1, 2)
	pathfinding(b, g, 2, 2)
}

func groundsAreas() []box {
	return append(tickingAreas(box{144, -500, 293, -357}), tickingAreas(box{128, -448, 143, -392})...)
}

func grove(b *builder, g grid, i, j int) {
	trees := []string{"oak", "birch", "spruce", "jungle", "acacia", "dark_oak", "cherry", "mangrove", "pale_oak"}
	x, z := g.cellAt(i, j)
	g.lights(b, i, j, 9, ground+12)
	n := 0
	for a := x + 4; a < x+g.cell-3; a += 8 {
		for c := z + 4; c < z+g.cell-3; c += 8 {
			t := trees[n%len(trees)]
			n++
			leaves := t + "_leaves"
			b.fill(a-2, ground+4, c-2, a+2, ground+5, c+2, leaves)
			for _, d := range [][2]int{{-2, -2}, {-2, 2}, {2, -2}, {2, 2}} {
				b.set(a+d[0], ground+5, c+d[1], "air")
			}
			b.fill(a-1, ground+6, c-1, a+1, ground+7, c+1, leaves)
			b.fill(a, ground+1, c, a, ground+6, c, t+"_log")
			b.set(a, ground, c, "dirt")
		}
	}
}

func quarry(b *builder, g grid, i, j int) {
	ores := []string{"coal_ore", "iron_ore", "copper_ore", "gold_ore", "redstone_ore", "lapis_ore", "diamond_ore", "emerald_ore"}
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "gravel")
	g.lights(b, i, j, 9, ground+18)
	qx, qz := x+8, z+8 // 24x24x16 block, deepslate in the bottom six layers
	b.fill(qx, ground+1, qz, qx+23, ground+16, qz+23, "stone")
	b.fill(qx, ground+1, qz, qx+23, ground+6, qz+23, "deepslate")
	for k, s := range []string{"granite", "diorite", "andesite", "tuff", "calcite", "dripstone_block"} {
		b.fill(qx+4*k, ground+12, qz, qx+4*k+3, ground+16, qz+23, s)
	}
	rnd := rand.New(rand.NewSource(7)) // fixed seed: rebuilds give the same veins
	for range 140 {
		ore := ores[rnd.Intn(len(ores))]
		a, c, y := qx+rnd.Intn(23), qz+rnd.Intn(23), ground+1+rnd.Intn(11)
		host := "stone"
		if y <= ground+6 {
			ore, host = "deepslate_"+ore, "deepslate"
		}
		b.fill(a, y, c, a+rnd.Intn(2), y+rnd.Intn(2), c+rnd.Intn(2), ore, "replace", host)
	}
	samples := append([]string{}, ores...)
	for _, o := range ores {
		samples = append(samples, "deepslate_"+o)
	}
	samples = append(samples, "nether_gold_ore", "quartz_ore", "ancient_debris", "raw_iron_block", "raw_copper_block", "raw_gold_block")
	for k, s := range samples {
		b.set(x+3+2*(k%17), ground+1, z+g.cell-4-2*(k/17), s)
	}
	for k := range 16 { // steps up the north face
		b.fill(qx+k, ground+1, qz-1, qx+k, ground+1+k, qz-1, "cobblestone")
	}
}

func hazards(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.lights(b, i, j, 9, ground+4)
	b.fill(x+3, ground-4, z+3, x+14, ground, z+14, "stone") // pool
	b.fill(x+4, ground-3, z+4, x+13, ground, z+13, "water")
	b.fill(x+4, ground-4, z+4, x+13, ground-4, z+13, "sand")
	b.fill(x+20, ground-1, z+3, x+29, ground, z+10, "stone") // lava basin, glass rim
	b.fill(x+21, ground, z+4, x+28, ground, z+9, "lava")
	b.fill(x+20, ground+1, z+3, x+29, ground+1, z+10, "glass")
	b.fill(x+21, ground+1, z+4, x+28, ground+1, z+9, "air")
	b.fill(x+34, ground+1, z+3, x+36, ground+8, z+5, "stone_bricks") // waterfall tower
	b.fill(x+33, ground, z+2, x+37, ground, z+6, "stone_bricks")
	b.set(x+35, ground+9, z+4, "water")
	for k, base := range []string{"soul_sand", "magma"} { // bubble columns, up and down
		a := x + 5 + 5*k
		b.fill(a-1, ground+1, z+20, a+1, ground+10, z+22, "glass")
		b.fill(a, ground+1, z+21, a, ground+10, z+21, "water")
		b.set(a, ground, z+21, base)
	}
	pads := []string{"web", "powder_snow", "magma", "ice", "packed_ice", "blue_ice", "slime", "honey_block", "soul_sand", "mud", "snow", "sculk"}
	for k, s := range pads {
		a, c := x+15+6*(k%4), z+16+6*(k/4)
		y := ground
		if s == "web" {
			y = ground + 1
		}
		b.fill(a, y, c, a+3, y, c+3, s)
	}
	b.fill(x+3, ground, z+33, x+12, ground, z+37, "sand")
	for a := x + 4; a < x+12; a += 3 {
		b.fill(a, ground+1, z+34, a, ground+2, z+34, "cactus")
		b.set(a, ground+1, z+36, "deadbush")
	}
	for a := x + 16; a < x+30; a += 2 {
		b.set(a, ground+1, z+35, `sweet_berry_bush ["growth"=3]`)
	}
}

func storage(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	for row := range 4 {
		blk := `chest ["minecraft:cardinal_direction"="south"]`
		if row == 3 {
			blk = `barrel ["facing_direction"=1]`
		}
		for k := range 12 {
			b.set(x+4+2*k, ground+1, z+4+4*row, blk)
		}
	}
	south := `["minecraft:cardinal_direction"="south"]`
	stations := []string{"crafting_table", "furnace " + south, "blast_furnace " + south, "smoker " + south,
		"smithing_table", "anvil", "grindstone", "stonecutter_block", "loom", "cartography_table", "fletching_table",
		"enchanting_table", "brewing_stand", "cauldron", "composter", "lectern", "ender_chest", "undyed_shulker_box",
		"crafter", "beehive", "bell", "jukebox"}
	for k, s := range stations {
		b.set(x+4+2*(k%12), ground+1, z+22+3*(k/12), s)
	}
	b.set(x+4, ground+1, z+32, "chest") // hopper line feeding a chest
	for k := 1; k <= 5; k++ {
		b.set(x+4+k, ground+1, z+32, `hopper ["facing_direction"=4]`)
	}
	b.fill(x+30, ground+1, z+30, x+35, ground+3, z+35, "hay_block")
	stock := [][]string{
		{"iron_pickaxe", "iron_axe", "iron_shovel", "iron_hoe", "shears", "bucket", "flint_and_steel"},
		{"oak_log 64", "oak_planks 64", "cobblestone 64", "torch 64", "stick 64", "glass 64"},
		{"bread 64", "cooked_beef 64", "apple 64", "carrot 64", "wheat_seeds 64", "bone_meal 64"},
		{"coal 64", "raw_iron 64", "raw_copper 64", "raw_gold 32", "diamond 16", "redstone 64"},
	}
	for k, items := range stock {
		for slot, it := range items {
			f := append(strings.Fields(it), "1")
			b.raw("replaceitem block %d %d %d slot.container %d %s %s", x+4+2*k, ground+1, z+4, slot, f[0], f[1])
		}
	}
}

func plaza(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "polished_diorite")
	cx, cz := x+19, z+19
	b.fill(cx-1, ground, cz-1, cx+1, ground, cz+1, "iron_block")
	b.set(cx, ground+1, cz, "beacon")
	b.set(cx+3, ground+1, cz, "lodestone")
	for _, d := range [][2]int{{-4, -4}, {-4, 4}, {4, -4}, {4, 4}} {
		b.set(cx+d[0], ground, cz+d[1], "sea_lantern")
	}
	var gallery []string
	// Concrete powder is left out: fill turns it into concrete on placement.
	for _, kind := range []string{"wool", "concrete", "terracotta", "stained_glass", "shulker_box", "glazed_terracotta"} {
		for _, c := range dyeColors {
			if c == "light_gray" && kind == "glazed_terracotta" {
				gallery = append(gallery, "silver_glazed_terracotta")
				continue
			}
			gallery = append(gallery, c+"_"+kind)
		}
	}
	for _, w := range []string{"oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "mangrove", "cherry", "pale_oak", "bamboo", "crimson", "warped"} {
		gallery = append(gallery, w+"_planks")
	}
	gallery = append(gallery, "stone", "cobblestone", "mossy_cobblestone", "stone_bricks", "mossy_stone_bricks",
		"cracked_stone_bricks", "chiseled_stone_bricks", "smooth_stone", "brick_block", "sandstone", "red_sandstone",
		"prismarine", "dark_prismarine", "nether_brick", "red_nether_brick", "end_bricks", "quartz_block", "purpur_block",
		"blackstone", "polished_blackstone_bricks", "mud_bricks", "deepslate_bricks", "deepslate_tiles", "tuff_bricks",
		"copper_block", "exposed_copper", "weathered_copper", "oxidized_copper", "amethyst_block", "obsidian",
		"crying_obsidian", "netherrack", "end_stone", "glowstone", "sea_lantern", "shroomlight", "ochre_froglight",
		"gold_block", "iron_block", "diamond_block", "emerald_block", "lapis_block", "redstone_block", "coal_block",
		"bone_block", "packed_mud", "moss_block", "resin_bricks", "glass", "tinted_glass", "clay", "snow", "bookshelf",
		"honeycomb_block")
	for k, p := range galleryRing(g.cell, len(gallery)) {
		b.fill(x+p[0], ground+1, z+p[1], x+p[0], ground+3, z+p[1], gallery[k])
	}
}

// galleryRing returns pillar spots on three nested square rings, two blocks
// apart, enough for n pillars.
func galleryRing(cell, n int) [][2]int {
	var ring [][2]int
	for _, inset := range []int{2, 6, 10} {
		lo, hi := inset, cell-1-inset
		for t := lo; t < hi; t += 2 {
			ring = append(ring, [2]int{t, lo})
		}
		for t := lo; t < hi; t += 2 {
			ring = append(ring, [2]int{hi, t})
		}
		for t := hi; t > lo; t -= 2 {
			ring = append(ring, [2]int{t, hi})
		}
		for t := hi; t > lo; t -= 2 {
			ring = append(ring, [2]int{lo, t})
		}
	}
	return ring[:min(n, len(ring))]
}

func redstone(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	lever := `lever ["lever_direction"="up_north_south"]`
	for k := range 8 {
		a := x + 4 + 3*k
		b.set(a, ground+1, z+4, "redstone_lamp") // lever on lamp
		b.set(a, ground+2, z+4, lever)
		b.set(a, ground+1, z+8, lever) // lever -> 10 wire -> lamp
		b.fill(a, ground+1, z+9, a, ground+1, z+18, "redstone_wire")
		b.set(a, ground+1, z+19, "redstone_lamp")
	}
	for k := range 4 { // pressure-plate iron doors in a wall
		a := x + 4 + 6*k
		b.fill(a-1, ground+1, z+24, a+1, ground+3, z+24, "stone_bricks")
		b.fill(a, ground+1, z+24, a, ground+2, z+24, "air")
		b.door(a, ground+1, z+24, `iron_door ["direction"=1,"upper_block_bit"=false]`)
		b.set(a, ground+1, z+23, "stone_pressure_plate")
		b.set(a, ground+1, z+25, "stone_pressure_plate")
	}
	parts := []string{"daylight_detector", "target", "observer", "piston", "sticky_piston", "noteblock", "dispenser",
		"dropper", "redstone_torch", "unpowered_repeater", "unpowered_comparator", "redstone_block", "lightning_rod",
		"sculk_sensor", "calibrated_sculk_sensor", "copper_bulb"}
	for k, s := range parts {
		a, c := x+4+4*(k%8), z+30+4*(k/8)
		b.set(a, ground+1, c, "redstone_lamp")
		b.set(a+1, ground+1, c, s)
	}
}

func farm(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.lights(b, i, j, 9, ground+4)
	for k, crop := range []string{"wheat", "carrots", "potatoes", "beetroot"} {
		a := x + 2 + 9*k
		b.fill(a, ground, z+2, a+8, ground, z+29, `farmland ["moisturized_amount"=7]`)
		b.fill(a+4, ground, z+2, a+4, ground, z+29, "water")
		ripe := crop + ` ["growth"=7]`
		b.fill(a, ground+1, z+2, a+3, ground+1, z+29, ripe)
		b.fill(a+5, ground+1, z+2, a+8, ground+1, z+29, ripe)
	}
	b.fill(x+2, ground, z+32, x+37, ground, z+32, "water")
	b.fill(x+2, ground, z+33, x+37, ground, z+33, "sand")
	for a := x + 2; a < x+38; a += 2 {
		b.fill(a, ground+1, z+33, a, ground+3, z+33, "reeds")
	}
	for a := x + 2; a < x+38; a += 3 {
		b.set(a, ground+1, z+35, "pumpkin")
		b.set(a+1, ground+1, z+36, "melon_block")
	}
	b.set(x+37, ground+1, z+37, "composter")
}

func pens(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.lights(b, i, j, 9, ground+4)
	mobs := []struct {
		kind  string
		count int
	}{{"cow", 4}, {"sheep", 4}, {"pig", 4}, {"chicken", 6}}
	for k, m := range mobs {
		a, c := x+2+19*(k%2), z+2+19*(k/2)
		b.fill(a, ground+1, c, a+16, ground+1, c+16, "oak_fence")
		b.fill(a+1, ground+1, c+1, a+15, ground+1, c+15, "air")
		gz := c + 16
		if k >= 2 {
			gz = c
		}
		b.set(a+8, ground+1, gz, `fence_gate ["direction"=0]`)
		b.set(a+2, ground+1, c+2, "hay_block")
		b.set(a+14, ground+1, c+2, "cauldron")
		// Rebuilds replace the animals rather than adding to them, including
		// strays that got out of their pen. The selector is a mob type inside
		// this cell, never a player.
		b.raw("kill @e[type=%s,x=%d,y=%d,z=%d,dx=%d,dy=8,dz=%d]", m.kind, x, ground-2, z, g.cell-1, g.cell-1)
		for n := range m.count {
			b.raw("summon %s %d %d %d", m.kind, a+4+2*n, ground+1, c+8)
		}
	}
}

func pathfinding(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	for k := range 7 { // stairs up, a one-wide bridge, a ladder down
		b.fill(x+3+k, ground+1, z+3, x+3+k, ground+1+k, z+5, "stone_bricks")
	}
	b.fill(x+10, ground+7, z+4, x+24, ground+7, z+4, "oak_planks")
	b.fill(x+25, ground+1, z+3, x+26, ground+7, z+5, "stone_bricks")
	b.fill(x+27, ground+1, z+4, x+27, ground+7, z+4, `ladder ["facing_direction"=5]`)
	a := x + 3
	for _, gap := range []int{0, 1, 2, 3, 2, 1} { // jump gaps on a raised walk
		a += gap
		b.fill(a, ground+1, z+9, a+2, ground+2, z+11, "stone_bricks")
		a += 3
	}
	for k, s := range []string{"wooden_door", "spruce_door", "iron_bars", "fence_gate", "trapdoor", "scaffolding"} {
		c := x + 3 + 5*k
		b.fill(c, ground+1, z+15, c+3, ground+3, z+15, "stone_bricks")
		b.fill(c+1, ground+1, z+15, c+2, ground+2, z+15, "air")
		switch s {
		case "wooden_door", "spruce_door":
			b.door(c+1, ground+1, z+15, s+` ["direction"=1,"upper_block_bit"=false]`)
		case "trapdoor":
			b.set(c+1, ground+3, z+14, s+` ["open_bit"=false,"upside_down_bit"=true]`)
		case "scaffolding":
			b.fill(c+1, ground+1, z+15, c+1, ground+4, z+15, s)
		default:
			b.set(c+1, ground+1, z+15, s)
		}
	}
	for k, s := range []string{"soul_sand", "ice", "honey_block", "slime", "gravel", "sand"} {
		b.fill(x+3, ground, z+18+k, x+20, ground, z+18+k, s)
	}
	b.fill(x+23, ground-1, z+18, x+36, ground, z+23, "water") // wade-through trench
	maze(b, x+3, z+25, 6, 3)
	for k := range 5 { // slab and block steps
		blk := "stone_bricks"
		if k%2 == 0 {
			blk = "stone_brick_slab"
		}
		b.set(x+22+2*k, ground+1+k/2, z+28, blk)
	}
}

// maze carves an n x n maze (walls two high) by depth-first search, with a
// fixed seed so every rebuild has the same layout.
func maze(b *builder, mx, mz, n int, seed int64) {
	b.fill(mx, ground+1, mz, mx+2*n, ground+2, mz+2*n, "mossy_stone_bricks")
	open := func(x, z int) { b.fill(x, ground+1, z, x, ground+2, z, "air") }
	rnd := rand.New(rand.NewSource(seed))
	seen := map[[2]int]bool{{0, 0}: true}
	stack := [][2]int{{0, 0}}
	open(mx+1, mz+1)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		var next [][2]int
		for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			p := [2]int{cur[0] + d[0], cur[1] + d[1]}
			if p[0] >= 0 && p[0] < n && p[1] >= 0 && p[1] < n && !seen[p] {
				next = append(next, p)
			}
		}
		if len(next) == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		p := next[rnd.Intn(len(next))]
		seen[p] = true
		stack = append(stack, p)
		open(mx+1+cur[0]+p[0], mz+1+cur[1]+p[1])
		open(mx+1+2*p[0], mz+1+2*p[1])
	}
	open(mx, mz+1)         // entrance
	open(mx+2*n, mz+2*n-1) // exit
}
