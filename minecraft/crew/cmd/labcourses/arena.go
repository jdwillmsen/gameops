package main

// The Colosseum is one solid ring of stands with the pens and the four
// entrance passages cut out of it afterwards. Building the mass first keeps
// every pen fully enclosed and lets the pens be defined as holes rather than
// as walls that must line up with the tier above them.

const (
	arenaCX = 0
	arenaCZ = -760

	arenaFloorR     = 45
	arenaTier1Inner = 46
	arenaTier1Outer = 55
	arenaTier2Outer = 62
	arenaTier3Outer = 67
	arenaWallInner  = 68
	arenaWallOuter  = 70

	arenaTier2Top  = 71
	arenaTier3Top  = 75
	arenaWallTop   = 84
	arenaCorridorY = 68

	arenaArchHalfWidth = 6
	arenaCorridorHalf  = 3

	arenaPenCount  = 12
	arenaPenRadius = 52
	arenaPenSize   = 6
	arenaPenRoof   = 67
)

func buildArena(b *builder) {
	arenaFloor(b)
	arenaStands(b)
	arenaTrim(b)
	arenaGates(b)
	arenaPens(b)
	arenaDais(b)
	arenaWallLights(b)
}

func arenaAreas() []box {
	return tickingAreas(arenaBounds)
}

var arenaBounds = box{arenaCX - 80, arenaCZ - 80, arenaCX + 80, arenaCZ + 80}

func arenaD2(x, z int) int {
	dx, dz := x-arenaCX, z-arenaCZ
	return dx*dx + dz*dz
}

func arenaWithin(x, z, r int) bool { return arenaD2(x, z) <= r*r }

// arenaShell is the one-cell-wide ring at radius r.
func arenaShell(x, z, r int) bool {
	d := arenaD2(x, z)
	return d <= r*r && d > (r-1)*(r-1)
}

// arenaRing fills the cells whose distance from the centre is between r0 and
// r1, one horizontal run per fill so an annulus costs rows, not blocks.
func arenaRing(b *builder, r0, r1, y0, y1 int, block string) {
	lo := (r0 - 1) * (r0 - 1)
	if r0 <= 0 {
		lo = -1
	}
	for z := arenaCZ - r1; z <= arenaCZ+r1; z++ {
		start, have := 0, false
		for x := arenaCX - r1; x <= arenaCX+r1; x++ {
			d := arenaD2(x, z)
			inside := d <= r1*r1 && d > lo
			if inside && !have {
				start, have = x, true
			}
			if !inside && have {
				b.fill(start, y0, z, x-1, y1, z, block)
				have = false
			}
		}
		if have {
			b.fill(start, y0, z, arenaCX+r1, y1, z, block)
		}
	}
}

func arenaShellFill(b *builder, r, y int, block string) {
	for z := arenaCZ - r; z <= arenaCZ+r; z++ {
		start, have := 0, false
		for x := arenaCX - r; x <= arenaCX+r; x++ {
			inside := arenaShell(x, z, r)
			if inside && !have {
				start, have = x, true
			}
			if !inside && have {
				b.fill(start, y, z, x-1, y, z, block)
				have = false
			}
		}
		if have {
			b.fill(start, y, z, arenaCX+r, y, z, block)
		}
	}
}

// arenaFloor is a hatched sand / smooth sandstone floor: alternating 5x5
// patches break up the texture without a command per block.
func arenaFloor(b *builder) {
	for z := arenaCZ - arenaFloorR; z <= arenaCZ+arenaFloorR; z++ {
		start, have := 0, false
		block := ""
		flush := func(end int) {
			if have {
				b.fill(start, ground, z, end, ground, z, block)
				have = false
			}
		}
		for x := arenaCX - arenaFloorR; x <= arenaCX+arenaFloorR; x++ {
			if !arenaWithin(x, z, arenaFloorR) {
				flush(x - 1)
				continue
			}
			next := "sand"
			if (patch(x, arenaCX)+patch(z, arenaCZ))%2 == 0 {
				next = "smooth_sandstone"
			}
			if !have {
				start, block, have = x, next, true
				continue
			}
			if next != block {
				flush(x - 1)
				start, block, have = x, next, true
			}
		}
		flush(arenaCX + arenaFloorR)
	}
}

// patch maps a coordinate to a positive patch number so the hatch is stable
// either side of the centre.
func patch(v, origin int) int {
	p := (v - origin) / 5
	if p < 0 {
		p = -p
	}
	return p
}

func arenaStands(b *builder) {
	arenaRing(b, arenaTier1Inner, arenaTier1Outer, ground, arenaPenRoof, "stone_bricks")
	arenaRing(b, arenaTier1Outer+1, arenaTier2Outer, ground, arenaTier2Top, "sandstone")
	arenaRing(b, arenaTier2Outer+1, arenaTier3Outer, ground, arenaTier3Top, "stone_bricks")
	arenaRing(b, arenaTier3Outer+1, arenaWallOuter, ground, arenaWallTop, "stone_bricks")
}

func arenaTrim(b *builder) {
	arenaShellFill(b, arenaTier1Outer, arenaPenRoof, "quartz_block")
	arenaShellFill(b, arenaTier2Outer, arenaTier2Top, "quartz_block")
	arenaShellFill(b, arenaTier3Outer, arenaTier3Top, "quartz_block")
	arenaRing(b, arenaTier3Outer+1, arenaWallOuter, arenaWallTop, arenaWallTop, "quartz_block")
}

// archTop is the highest air block of an arched gate at tangential offset t:
// straight sides to the corridor's height, then a half circle of radius six.
func archTop(t int) int {
	return arenaCorridorY + isqrt(arenaArchHalfWidth*arenaArchHalfWidth-t*t)
}

func isqrt(n int) int {
	if n <= 0 {
		return 0
	}
	r := 1
	for (r+1)*(r+1) <= n {
		r++
	}
	return r
}

// arenaGates cuts four arched entrances through the outer wall and the stands,
// narrowing to a straight passage that reaches the fighting floor.
func arenaGates(b *builder) {
	for _, g := range arenaArches() {
		for t := -arenaArchHalfWidth; t <= arenaArchHalfWidth; t++ {
			top := archTop(t)
			for _, band := range [][2]int{{arenaWallInner, arenaWallOuter}, {arenaTier2Outer + 1, arenaTier3Outer}} {
				cell := g.yrect(band[0], band[1], t, t, 64, top)
				b.fill(cell.X0, cell.Y0, cell.Z0, cell.X1, cell.Y1, cell.Z1, "air")
			}
		}
		c := g.yrect(arenaFloorR, arenaTier2Outer, -arenaCorridorHalf, arenaCorridorHalf, 64, arenaCorridorY)
		b.fill(c.X0, c.Y0, c.Z0, c.X1, c.Y1, c.Z1, "air")
	}
	s := arenaStartGate()
	b.fill(s.X0, s.Y0, s.Z0, s.X1, s.Y1, s.Z1, "iron_bars")
}

// arenaPens hollows the twelve pens out of the stands. Each pen has a three
// wide tunnel to the floor; the iron bars sit at the pen end of it, so the
// director opens a pen by clearing that gate box.
func arenaPens(b *builder) {
	for _, p := range arenaPensLayout {
		b.fill(p.X0, 64, p.Z0, p.X1, 66, p.Z1, "air")
		b.fill(p.X0, ground, p.Z0, p.X1, ground, p.Z1, "sand")
		b.set(p.X, arenaPenRoof, p.Z, "sea_lantern")
		for _, step := range p.tunnel() {
			b.fill(step.X0, 64, step.Z0, step.X1, 66, step.Z1, "air")
		}
		b.fill(p.Gate.X0, p.Gate.Y0, p.Gate.Z0, p.Gate.X1, p.Gate.Y1, p.Gate.Z1, "iron_bars")
	}
}

func arenaDais(b *builder) {
	arenaRing(b, 0, 7, 64, 64, "smooth_sandstone")
	arenaShellFill(b, 7, 64, "quartz_block")
	b.fill(-1, 64, arenaCZ-1, 1, 64, arenaCZ+1, "chiseled_quartz_block")
}

// arenaWallLights hides sea lanterns in the inner face of the wall. The arena
// must not use light blocks: the flat square is already lit, and a lantern in
// the masonry is part of the build rather than another invisible grid.
func arenaWallLights(b *builder) {
	for _, y := range []int{73, 79} {
		for k := range 12 {
			angle := 15 + 30*float64(k)
			x, z := arenaPoint(68, angle)
			if arenaD2(x, z) <= arenaTier3Outer*arenaTier3Outer || arenaD2(x, z) > arenaWallOuter*arenaWallOuter {
				continue
			}
			if arenaInArch(x, z) {
				continue
			}
			b.set(x, y, z, "sea_lantern")
		}
	}
}

func arenaInArch(x, z int) bool {
	for _, g := range arenaArches() {
		cell := g.rect(arenaTier2Outer+1, arenaWallOuter, -arenaArchHalfWidth, arenaArchHalfWidth)
		if x >= cell.X0 && x <= cell.X1 && z >= cell.Z0 && z <= cell.Z1 {
			return true
		}
	}
	return false
}
