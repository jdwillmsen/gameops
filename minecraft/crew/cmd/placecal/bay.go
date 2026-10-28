package main

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// airHash is the network hash of air.
const airHash uint32 = 3690217760

// A bay is the prepared platform one unit works on: a stone floor with three
// fixed stone blocks to click, all else air. Every trial places into a cell
// beside a fixture and the bay is put back to exactly this after it.
//
//	s0      floor-standing support: its top and four sides are clicked
//	s1      a stone block floating over a pillar: its bottom is clicked
//	lane    a stretch of bare floor where neighbours are placed around a block
type bay struct {
	ox, f, oz int32
}

const (
	bayWest, bayEast   = -6, 6
	bayNorth, baySouth = -4, 6
	bayHeight          = 8
	scanUp             = 5
	scanWest, scanEast = -5, 5
	scanNorth          = -3
	scanSouth          = 3
)

func (b bay) pos(dx, dy, dz int32) protocol.BlockPos {
	return protocol.BlockPos{b.ox + dx, b.f + dy, b.oz + dz}
}

func (b bay) s0() protocol.BlockPos     { return b.pos(0, 1, 0) }
func (b bay) s1() protocol.BlockPos     { return b.pos(3, 3, 0) }
func (b bay) pillar() protocol.BlockPos { return b.pos(3, 1, 0) }

// lane is the cell on the floor the neighbour pass places the block in.
func (b bay) lane() protocol.BlockPos { return b.pos(-2, 1, 1) }

// stand is where the unit stands, feet in the middle of its cell.
func (b bay) stand() mgl32.Vec3 {
	return mgl32.Vec3{float32(b.ox) + 0.5, float32(b.f + 1), float32(b.oz) + 2.5}
}

func (b bay) fixtures() []protocol.BlockPos { return []protocol.BlockPos{b.s0(), b.s1(), b.pillar()} }

func (b bay) isFixture(p protocol.BlockPos) bool {
	for _, f := range b.fixtures() {
		if f == p {
			return true
		}
	}
	return false
}

func offset(p protocol.BlockPos, o [3]int32) protocol.BlockPos {
	return protocol.BlockPos{p.X() + o[0], p.Y() + o[1], p.Z() + o[2]}
}

// site is the block a click on the face lands against and the cell it places
// into. The bottom face is clicked on the floating block, every other on s0.
func (b bay) site(c click) (against, target protocol.BlockPos) {
	if c.Lane {
		return floorUnder(b.lane()), b.lane()
	}
	against = b.s0()
	if c.Face == fBottom {
		against = b.s1()
	}
	return against, offset(against, faceOff[c.Face])
}

// floorUnder is the floor block below a lane cell, which the neighbour pass
// clicks on its top face.
func floorUnder(p protocol.BlockPos) protocol.BlockPos {
	return protocol.BlockPos{p.X(), p.Y() - 1, p.Z()}
}

// laneNeighbours are the four cells around the lane cell.
func (b bay) laneNeighbours() map[face]protocol.BlockPos {
	l := b.lane()
	return map[face]protocol.BlockPos{
		fEast: offset(l, faceOff[fEast]), fWest: offset(l, faceOff[fWest]),
		fNorth: offset(l, faceOff[fNorth]), fSouth: offset(l, faceOff[fSouth]),
	}
}

// scan lists the cells a trial may leave something in: the cells around the
// fixtures and the lane, wide enough for the second half of a bed or door.
func (b bay) scan() []protocol.BlockPos {
	var out []protocol.BlockPos
	for y := int32(1); y <= scanUp; y++ {
		for z := int32(scanNorth); z <= scanSouth; z++ {
			for x := int32(scanWest); x <= scanEast; x++ {
				if p := b.pos(x, y, z); !b.isFixture(p) {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// prepCommands lay the bay down. They run through the lab console, which is
// the one thing here a unit cannot do.
func (b bay) prepCommands() []string {
	x1, x2 := b.ox+bayWest, b.ox+bayEast
	z1, z2 := b.oz+bayNorth, b.oz+baySouth
	cmds := []string{
		fmt.Sprintf("fill %d %d %d %d %d %d stone", x1, b.f, z1, x2, b.f, z2),
		fmt.Sprintf("fill %d %d %d %d %d %d air", x1, b.f+1, z1, x2, b.f+bayHeight, z2),
	}
	for _, p := range b.fixtures() {
		cmds = append(cmds, setStone(p))
	}
	// Plants need dirt under them; the lane is where a plant or a bed goes.
	l := floorUnder(b.lane())
	return append(cmds, fmt.Sprintf("setblock %d %d %d dirt", l.X(), l.Y(), l.Z()))
}

func setStone(p protocol.BlockPos) string {
	return fmt.Sprintf("setblock %d %d %d stone", p.X(), p.Y(), p.Z())
}

func setAir(p protocol.BlockPos) string {
	return fmt.Sprintf("setblock %d %d %d air", p.X(), p.Y(), p.Z())
}
