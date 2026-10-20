package main

import (
	"fmt"
	"math/rand"
)

// worksGrid is the field north of the grounds: things that move a unit or
// that it must time.
var worksGrid = grid{x0: 150, z0: -700, cell: 40, road: 6, n: 3}

// worksPathX is the first column of the path north from the grounds; it
// lines up with the grounds' second road.
const worksPathX = 197

// buildWorks: rails, boats, arena / gates, climbing, bridges /
// surfaces, clutter, tunnels.
func buildWorks(b *builder) {
	g := worksGrid
	r := g.bounds()
	g.base(b, [][]string{{"gray", "blue", "red"}, {"orange", "light_blue", "brown"}, {"yellow", "lime", "black"}})
	b.fill(worksPathX, ground, r.z1+1, worksPathX+4, ground, groundsGrid.z0-1, "grass_path")
	b.fill(worksPathX, ground+1, r.z1+1, worksPathX+4, ground+4, groundsGrid.z0-1, "air")
	rails(b, g, 0, 0)
	boats(b, g, 1, 0)
	arena(b, g, 2, 0)
	gates(b, g, 0, 1)
	climbing(b, g, 1, 1)
	bridges(b, g, 2, 1)
	surfaces(b, g, 0, 2)
	clutter(b, g, 1, 2)
	tunnels(b, g, 2, 2)
	for j := range 3 {
		for i := range 3 {
			lightsAround(b, g, i, j)
		}
	}
}

func worksAreas() []box {
	r := worksGrid.bounds()
	return append(tickingAreas(r), tickingAreas(box{worksPathX, r.z1 + 1, worksPathX + 4, groundsGrid.z0 - 1})...)
}

// clearEntities removes one kind of entity from a whole cell, so a rebuild
// never stacks a second minecart, boat or dummy on the last one.
func clearEntities(b *builder, g grid, i, j int, kind string) {
	x, z := g.cellAt(i, j)
	b.raw("kill @e[type=%s,x=%d,y=%d,z=%d,dx=%d,dy=12,dz=%d]", kind, x, ground-6, z, g.cell-1, g.cell-1)
}

// rails: a powered loop round the cell with a minecart at the north side.
func rails(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	clearEntities(b, g, i, j, "minecart")
	lo, hi := 4, g.cell-5
	// Bedrock rail_direction: 0 north-south, 1 east-west, 6-9 the curves
	// south-east, south-west, north-west and north-east.
	rail := func(d int) string { return fmt.Sprintf(`rail ["rail_direction"=%d]`, d) }
	powered := func(d int) string {
		return fmt.Sprintf(`golden_rail ["rail_direction"=%d,"rail_data_bit"=true]`, d)
	}
	b.fill(x+lo, ground+1, z+lo, x+hi, ground+1, z+lo, rail(1))
	b.fill(x+lo, ground+1, z+hi, x+hi, ground+1, z+hi, rail(1))
	b.fill(x+lo, ground+1, z+lo, x+lo, ground+1, z+hi, rail(0))
	b.fill(x+hi, ground+1, z+lo, x+hi, ground+1, z+hi, rail(0))
	for k := lo + 6; k < hi-3; k += 8 { // powered rails held on by the block below
		for _, p := range [][3]int{{x + k, z + lo, 1}, {x + k, z + hi, 1}, {x + lo, z + k, 0}, {x + hi, z + k, 0}} {
			b.set(p[0], ground, p[1], "redstone_block")
			b.set(p[0], ground+1, p[1], powered(p[2]))
		}
	}
	b.set(x+lo, ground+1, z+lo, rail(6))
	b.set(x+hi, ground+1, z+lo, rail(7))
	b.set(x+hi, ground+1, z+hi, rail(8))
	b.set(x+lo, ground+1, z+hi, rail(9))
	b.set(x+lo+2, ground+1, z+lo+1, "stone_button") // station platform marker
	b.raw("summon minecart %d %d %d", x+lo+3, ground+1, z+lo)
}

// boats: a water ring to row round, with boats at the dock, and a blue ice
// lane on the island where a boat slides fastest.
func boats(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "grass")
	clearEntities(b, g, i, j, "boat")
	lo, hi := 4, g.cell-5
	b.fill(x+lo, ground-2, z+lo, x+hi, ground-2, z+hi, "stone")
	b.fill(x+lo, ground-1, z+lo, x+hi, ground, z+hi, "water")
	b.fill(x+lo+4, ground-1, z+lo+4, x+hi-4, ground, z+hi-4, "dirt")
	b.fill(x+lo+4, ground, z+lo+4, x+hi-4, ground, z+hi-4, "grass")
	b.fill(x+lo+6, ground, z+19, x+hi-6, ground, z+20, "blue_ice")
	b.fill(x+lo-2, ground, z+lo, x+lo-1, ground, z+lo+4, "oak_planks") // dock
	for k := range 2 {
		b.raw("summon boat %d %d %d", x+lo+1, ground+1, z+lo+1+2*k)
	}
	b.raw("summon boat %d %d %d", x+lo+7, ground+1, z+19)
}

// arena: a fenced ring with armoured dummies to strike. Live mobs are left
// to the test that needs them, which summons and clears its own.
func arena(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	clearEntities(b, g, i, j, "armor_stand")
	lo, hi := 3, g.cell-4
	b.fill(x+lo, ground+1, z+lo, x+hi, ground+2, z+hi, "oak_fence")
	b.fill(x+lo+1, ground+1, z+lo+1, x+hi-1, ground+2, z+hi-1, "air")
	b.fill(x+lo+1, ground, z+lo+1, x+hi-1, ground, z+hi-1, "sand")
	b.fill(x+lo, ground+1, z+19, x+lo, ground+2, z+20, "air")
	b.set(x+lo, ground+1, z+19, `fence_gate ["direction"=1,"open_bit"=false]`)
	b.set(x+lo, ground+1, z+20, `fence_gate ["direction"=1,"open_bit"=false]`)
	for k := range 4 {
		a, c := x+12+8*(k%2), z+12+8*(k/2)
		b.raw("summon armor_stand %d %d %d 0 0 minecraft:entity_spawned \"dummy %d\"", a, ground+1, c, k+1)
		for _, it := range []string{"slot.armor.head 0 iron_helmet", "slot.armor.chest 0 iron_chestplate"} {
			b.raw("replaceitem entity @e[type=armor_stand,x=%d,y=%d,z=%d,dx=0,dy=1,dz=0] %s", a, ground+1, c, it)
		}
	}
}

// gates: iron doors in a wall that open only by a button or a lever beside
// them, and a hatch over a ladder shaft held shut by a lever, the controls
// a unit must work rather than walk through.
func gates(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	for row, c := range []int{z + 8, z + 20} {
		b.fill(x+2, ground+1, c, x+g.cell-3, ground+3, c, "stone_bricks")
		for k := range 4 {
			a := x + 5 + 8*k
			b.fill(a, ground+1, c, a, ground+2, c, "air")
			b.door(a, ground+1, c, `iron_door ["direction"=1,"upper_block_bit"=false,"open_bit"=false]`)
			// Controls on both faces of the wall, beside the door, so a unit can
			// open it from either side. Row 0 has buttons, row 1 levers.
			for _, side := range []struct{ dz, face int }{{-1, 2}, {1, 3}} {
				ctl := fmt.Sprintf(`stone_button ["facing_direction"=%d]`, side.face)
				if row == 1 {
					ctl = fmt.Sprintf(`lever ["lever_direction"="%s","open_bit"=false]`, map[int]string{2: "north", 3: "south"}[side.face])
				}
				b.set(a+1, ground+2, c+side.dz, ctl)
			}
		}
	}
	// Hatch: a ladder shaft down to a room, under a closed iron trapdoor.
	hx, hz := x+8, z+30
	b.fill(hx-2, ground-6, hz-2, hx+6, ground-1, hz+4, "stone_bricks")
	b.fill(hx-1, ground-5, hz-1, hx+5, ground-2, hz+3, "air")
	b.fill(hx, ground-5, hz, hx, ground, hz, "air")
	b.fill(hx, ground-5, hz-1, hx, ground-1, hz-1, "stone_bricks")
	b.fill(hx, ground-5, hz, hx, ground-1, hz, `ladder ["facing_direction"=3]`)
	b.set(hx, ground, hz, `iron_trapdoor ["direction"=0,"open_bit"=false,"upside_down_bit"=false]`)
	b.set(hx+1, ground+1, hz, `lever ["lever_direction"="up_east_west","open_bit"=false]`)
	b.set(hx+4, ground-6, hz+2, "lime_concrete")
}

// climbing: five ways up to a platform twelve blocks high — a bubble column,
// ladders, scaffolding, twisting vines and a spiral stair — and a magma
// column down.
func climbing(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	const top = 12
	land := ground + top + 1
	// A door is the way in at the foot of each water shaft: doors hold water
	// back, where a plain opening floods the cell.
	shaft := func(a, c int, floor string) {
		b.fill(a-1, ground+1, c-1, a+1, ground+top, c+1, "glass")
		b.fill(a, ground+1, c, a, land, c, "water")
		b.set(a, ground, c, floor)
		b.fill(a, ground+1, c-1, a, ground+2, c-1, "air")
		b.door(a, ground+1, c-1, `wooden_door ["direction"=1,"upper_block_bit"=false,"open_bit"=false]`)
	}
	// The landing every climb reaches, fenced, with a ladder back down at its
	// far end. Each climb comes up through its own hole in it.
	b.fill(x+4, land, z+3, x+36, land, z+9, "smooth_stone")
	b.fill(x+4, land+1, z+3, x+36, land+1, z+3, "oak_fence")
	b.fill(x+4, land+1, z+9, x+36, land+1, z+9, "oak_fence")
	shaft(x+5, z+6, "soul_sand")
	b.fill(x+12, ground+1, z+4, x+12, land, z+4, "stone_bricks")
	b.fill(x+12, ground+1, z+5, x+12, land, z+5, `ladder ["facing_direction"=3]`)
	b.fill(x+18, ground+1, z+6, x+18, land, z+6, "scaffolding")
	b.fill(x+24, ground+1, z+4, x+24, land, z+4, "stone_bricks")
	b.fill(x+24, ground+1, z+5, x+24, land, z+5, "twisting_vines")
	// Spiral stair round a 2x2 core: each step one up and one round, under an
	// opening in the landing so the climb keeps its headroom.
	core := [2]int{x + 31, z + 5}
	b.fill(core[0]-1, land, core[1]-1, core[0]+2, land, core[1]+2, "air")
	b.fill(core[0], ground+1, core[1], core[0]+1, land, core[1]+1, "stone_bricks")
	ring := [][2]int{{-1, -1}, {0, -1}, {1, -1}, {2, -1}, {2, 0}, {2, 1}, {2, 2}, {1, 2}, {0, 2}, {-1, 2}, {-1, 1}, {-1, 0}}
	for s := range top + 1 {
		p := ring[s%len(ring)]
		b.set(core[0]+p[0], ground+1+s, core[1]+p[1], "stone_bricks")
	}
	b.fill(x+37, ground+1, z+6, x+37, land, z+6, "stone_bricks")
	b.fill(x+36, ground+1, z+6, x+36, land-1, z+6, `ladder ["facing_direction"=4]`)
	b.set(x+36, land, z+6, `ladder ["facing_direction"=4]`)
	// The magma column only goes down: a ladder up its side to a ledge on top.
	shaft(x+5, z+30, "magma")
	b.fill(x+4, land, z+29, x+6, land, z+31, "smooth_stone")
	b.set(x+5, land, z+30, "water")
	b.fill(x+7, ground+1, z+32, x+7, land, z+32, "stone_bricks")
	b.fill(x+7, ground+1, z+33, x+7, land, z+33, `ladder ["facing_direction"=3]`)
	b.set(x+6, land, z+32, "smooth_stone")
}

// bridges: a trench four deep with a water floor, crossed by bridges three,
// two and one wide, a slab bridge, a railed bridge and stepping stones with
// one- and two-block gaps.
func bridges(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "grass")
	lo, hi := z+10, z+29
	b.fill(x+2, ground-5, lo, x+g.cell-3, ground-5, hi, "stone")
	b.fill(x+2, ground-4, lo, x+g.cell-3, ground-4, hi, "water")
	b.fill(x+2, ground-3, lo, x+g.cell-3, ground, hi, "air")
	b.fill(x+2, ground-3, hi+1, x+2, ground, hi+1, "stone")
	b.fill(x+2, ground-3, hi, x+2, ground, hi, `ladder ["facing_direction"=2]`)
	a := x + 4
	for _, w := range []int{3, 2, 1} {
		b.fill(a, ground, lo, a+w-1, ground, hi, "oak_planks")
		a += w + 3
	}
	b.fill(a, ground, lo, a+1, ground, hi, `oak_slab ["minecraft:vertical_half"="bottom"]`)
	a += 5
	b.fill(a, ground, lo, a+2, ground, hi, "oak_planks")
	b.fill(a, ground+1, lo, a, ground+1, hi, "oak_fence")
	b.fill(a+2, ground+1, lo, a+2, ground+1, hi, "oak_fence")
	a += 6
	for _, gap := range []int{1, 2} {
		for c := lo; c <= hi; c += gap + 1 {
			b.set(a, ground, c, "stone_bricks")
		}
		a += 4
	}
}

// surfaces: long lanes of slippery, slow and sticky blocks, thirty blocks
// each, for timing how fast a unit crosses them.
func surfaces(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	lanes := []string{"packed_ice", "blue_ice", "soul_soil", "mud", "moss_block", "farmland", "grass_path", "cobweb", "snow_layer"}
	for k, s := range lanes {
		c := z + 3 + 4*k
		b.fill(x+4, ground, c, x+33, ground, c+2, "polished_andesite")
		switch s {
		case "cobweb": // webs every third block: a full lane would trap the unit
			for a := x + 6; a <= x+33; a += 3 {
				b.fill(a, ground+1, c, a, ground+1, c+2, "web")
			}
		case "snow_layer":
			b.fill(x+4, ground+1, c, x+33, ground+1, c+2, `snow_layer ["height"=4]`)
		default:
			b.fill(x+4, ground, c, x+33, ground, c+2, s)
		}
		b.set(x+3, ground, c+1, "lime_concrete")
		b.set(x+34, ground, c+1, "red_concrete")
	}
}

// clutter: a field of low pillars and odd blocks to thread a path through,
// seeded so every rebuild matches.
func clutter(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	rng := rand.New(rand.NewSource(17))
	odd := []string{"cauldron", "oak_fence", "cobblestone_wall", "hay_block", "barrel", "flower_pot", "anvil", "lantern"}
	for range 90 {
		a, c := x+4+rng.Intn(g.cell-9), z+4+rng.Intn(g.cell-9)
		switch rng.Intn(3) {
		case 0:
			b.fill(a, ground+1, c, a, ground+1+rng.Intn(3), c, "cobblestone")
		case 1:
			b.fill(a, ground+1, c, a+1, ground+1+rng.Intn(2), c+1, "stone_bricks")
		default:
			b.set(a, ground+1, c, odd[rng.Intn(len(odd))])
		}
	}
	b.fill(x+2, ground, z+2, x+3, ground, z+3, "lime_concrete")
	b.fill(x+g.cell-4, ground, z+g.cell-4, x+g.cell-3, ground, z+g.cell-3, "red_concrete")
}

// tunnels: a roofed maze, every corridor lit, the dark-cave case for a
// unit's pathfinding.
func tunnels(b *builder, g grid, i, j int) {
	x, z := g.cellAt(i, j)
	g.floor(b, i, j, "smooth_stone")
	const n = 17
	mx, mz := x+2, z+2
	maze(b, mx, mz, n, 23)
	b.fill(mx, ground+3, mz, mx+2*n, ground+3, mz+2*n, "stone")
	for a := mx + 1; a < mx+2*n; a += 4 {
		for c := mz + 1; c < mz+2*n; c += 4 {
			b.fill(a, ground+2, c, a, ground+2, c, "light_block_15", "keep")
		}
	}
}
