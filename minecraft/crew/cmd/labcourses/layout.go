package main

import (
	"fmt"
	"math"
)

// archGate is one of the four entrances. radial is the distance from the
// centre along axis; tangential is the perpendicular offset, so the same
// carve works for all four by swapping which coordinate carries which.
type archGate struct {
	name string
	axis byte
	sign int
}

// cell turns a radial distance and a tangential offset into a world cell.
func (g archGate) cell(radial, tangential int) (int, int) {
	radial *= g.sign
	if g.axis == 'x' {
		return arenaCX + radial, arenaCZ + tangential
	}
	return arenaCX + tangential, arenaCZ + radial
}

// rect is the box covering a radial band and a tangential span.
func (g archGate) rect(r0, r1, t0, t1 int) box3 {
	xa, za := g.cell(r0, t0)
	xb, zb := g.cell(r1, t1)
	return box3{min(xa, xb), 0, min(za, zb), max(xa, xb), 0, max(za, zb)}
}

func (g archGate) yrect(r0, r1, t0, t1, y0, y1 int) box3 {
	b := g.rect(r0, r1, t0, t1)
	b.Y0, b.Y1 = y0, y1
	return b
}

func arenaArches() []archGate {
	return []archGate{
		{name: "north", axis: 'z', sign: -1},
		{name: "east", axis: 'x', sign: 1},
		{name: "south", axis: 'z', sign: 1},
		{name: "west", axis: 'x', sign: -1},
	}
}

// box3 is an inclusive three-dimensional box.
type box3 struct{ X0, Y0, Z0, X1, Y1, Z1 int }

type point3 struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// pen is a spawn room hollowed out of the stands. The gate is the iron-bar
// wall at the pen end of the tunnel, which is the box the director clears to
// release the mobs.
type pen struct {
	ID     int
	Angle  int
	X, Z   int
	X0, Z0 int
	X1, Z1 int
	Gate   box3
	axis   byte
	inward int
	edge   int
}

// tunnel walks from the gate towards the centre until the passage reaches the
// fighting floor. The short tunnels never meet: neighbouring pens are 30
// degrees apart, about 27 blocks at the pen radius.
func (p pen) tunnel() []box3 {
	var out []box3
	if p.axis == 'x' {
		for x := p.edge; arenaD2(x, p.Z) > arenaFloorR*arenaFloorR; x += p.inward {
			out = append(out, box3{x, 64, p.Z - 1, x, 66, p.Z + 1})
		}
		return out
	}
	for z := p.edge; arenaD2(p.X, z) > arenaFloorR*arenaFloorR; z += p.inward {
		out = append(out, box3{p.X - 1, 64, z, p.X + 1, 66, z})
	}
	return out
}

func (p pen) spawn() point3 { return point3{p.X, ground + 1, p.Z} }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func arenaPoint(radius int, degrees float64) (int, int) {
	a := degrees * math.Pi / 180
	return arenaCX + int(math.Round(float64(radius)*math.Sin(a))),
		arenaCZ - int(math.Round(float64(radius)*math.Cos(a)))
}

// arenaPensLayout is the single source of pen geometry for both the build
// commands and the JSON the director reads.
var arenaPensLayout = func() []pen {
	const half = arenaPenSize / 2
	pens := make([]pen, 0, arenaPenCount)
	for k := range arenaPenCount {
		angle := 15 + 30*k
		x, z := arenaPoint(arenaPenRadius, float64(angle))
		dx, dz := x-arenaCX, z-arenaCZ
		p := pen{ID: k + 1, Angle: angle, X: x, Z: z,
			X0: x - half, X1: x + half, Z0: z - half, Z1: z + half}
		if abs(dx) >= abs(dz) {
			p.axis = 'x'
			if dx > 0 {
				p.inward, p.edge = -1, p.X0-1
			} else {
				p.inward, p.edge = 1, p.X1+1
			}
			p.Gate = box3{p.edge, 64, p.Z - 1, p.edge, 66, p.Z + 1}
		} else {
			p.axis = 'z'
			if dz > 0 {
				p.inward, p.edge = -1, p.Z0-1
			} else {
				p.inward, p.edge = 1, p.Z1+1
			}
			p.Gate = box3{p.X - 1, 64, p.edge, p.X + 1, 66, p.edge}
		}
		pens = append(pens, p)
	}
	return pens
}()

// arenaStartGate is the squad's iron gate at the floor end of the north
// passage: seven wide and four high, cleared when the squad is let in.
func arenaStartGate() box3 {
	return arenaArches()[0].yrect(arenaTier1Inner, arenaTier1Inner, -arenaCorridorHalf, arenaCorridorHalf, 64, arenaCorridorY-1)
}

type arenaCenter struct {
	X int `json:"x"`
	Z int `json:"z"`
}

type arenaBox struct {
	X0 int `json:"x0"`
	Y0 int `json:"y0"`
	Z0 int `json:"z0"`
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	Z1 int `json:"z1"`
}

type arenaArea struct {
	X0 int `json:"x0"`
	Z0 int `json:"z0"`
	X1 int `json:"x1"`
	Z1 int `json:"z1"`
}

type penDoc struct {
	ID       int      `json:"id"`
	AngleDeg int      `json:"angle_deg"`
	Spawn    point3   `json:"spawn"`
	Gate     arenaBox `json:"gate"`
	Open     string   `json:"open"`
}

type gateDoc struct {
	Name     string   `json:"name"`
	Opening  arenaBox `json:"opening"`
	Corridor arenaBox `json:"corridor"`
}

type startGateDoc struct {
	Name string   `json:"name"`
	Gate arenaBox `json:"gate"`
	Open string   `json:"open"`
}

type arenaLayoutDoc struct {
	Center      arenaCenter  `json:"center"`
	GroundY     int          `json:"ground_y"`
	FloorRadius int          `json:"floor_radius"`
	OuterRadius int          `json:"outer_radius"`
	Areas       []arenaArea  `json:"areas"`
	Pens        []penDoc     `json:"pens"`
	ArchedGates []gateDoc    `json:"arched_gates"`
	StartGate   startGateDoc `json:"start_gate"`
	Boss        point3       `json:"boss"`
}

func toArenaBox(b box3) arenaBox {
	return arenaBox{b.X0, b.Y0, b.Z0, b.X1, b.Y1, b.Z1}
}

func openCommand(b box3) string {
	return fmt.Sprintf("fill %d %d %d %d %d %d air", b.X0, b.Y0, b.Z0, b.X1, b.Y1, b.Z1)
}

func arenaLayout() arenaLayoutDoc {
	doc := arenaLayoutDoc{
		Center:      arenaCenter{arenaCX, arenaCZ},
		GroundY:     ground,
		FloorRadius: arenaFloorR,
		OuterRadius: arenaWallOuter,
		Boss:        point3{arenaCX, ground + 2, arenaCZ},
	}
	for _, a := range arenaAreas() {
		doc.Areas = append(doc.Areas, arenaArea{a.x0, a.z0, a.x1, a.z1})
	}
	for _, p := range arenaPensLayout {
		doc.Pens = append(doc.Pens, penDoc{
			ID: p.ID, AngleDeg: p.Angle, Spawn: p.spawn(),
			Gate: toArenaBox(p.Gate), Open: openCommand(p.Gate),
		})
	}
	for _, g := range arenaArches() {
		doc.ArchedGates = append(doc.ArchedGates, gateDoc{
			Name:     g.name,
			Opening:  toArenaBox(g.yrect(arenaWallInner, arenaWallOuter, -arenaArchHalfWidth, arenaArchHalfWidth, 64, archTop(0))),
			Corridor: toArenaBox(g.yrect(arenaFloorR, arenaTier2Outer, -arenaCorridorHalf, arenaCorridorHalf, 64, arenaCorridorY)),
		})
	}
	start := arenaStartGate()
	doc.StartGate = startGateDoc{Name: "squad_start", Gate: toArenaBox(start), Open: openCommand(start)}
	return doc
}
