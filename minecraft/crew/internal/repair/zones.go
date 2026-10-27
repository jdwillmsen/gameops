package repair

import (
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// Build box of the generator, relative to the layout centre. The layout does
// not carry it (the director never needed it), so it is restated here;
// footprint_test.go keeps it covering every area and gate the layout names.
const (
	footprintHalfX = 120 // x centre-120 .. centre+119
	footprintHalfZ = 106 // z centre-106 .. centre+105
	footprintY0    = 36
	footprintY1    = 122
)

// Footprint is the only region the bot may touch.
func Footprint(l arena.Layout) Box {
	return Box{
		X0: l.Center.X - footprintHalfX, X1: l.Center.X + footprintHalfX - 1,
		Z0: l.Center.Z - footprintHalfZ, Z1: l.Center.Z + footprintHalfZ - 1,
		Y0: footprintY0, Y1: footprintY1,
	}
}

// zoner names the part of the arena a cell is in, for the damage report. The
// bounds are approximate (the floor is an ellipse, the radius a circle); the
// report is for people deciding where to look, not for the repair.
func zoner(l arena.Layout) func(x, y, z int) string {
	return func(x, y, z int) string {
		for _, p := range l.Pens {
			if inBox(p.Gate, x, y, z) {
				return fmt.Sprintf("pen-gate-%d", p.ID)
			}
		}
		if inBox(l.StartGate.Gate, x, y, z) {
			return "start-gate"
		}
		dx, dz := x-l.Center.X, z-l.Center.Z
		d2 := dx*dx + dz*dz
		switch {
		case y < l.GroundY:
			return "underground"
		case d2 <= l.FloorRadius*l.FloorRadius:
			return "floor"
		case d2 <= l.OuterRadius*l.OuterRadius:
			return "stands-and-wall"
		default:
			return "plaza"
		}
	}
}

func inBox(b Box, x, y, z int) bool {
	return x >= b.X0 && x <= b.X1 && y >= b.Y0 && y <= b.Y1 && z >= b.Z0 && z <= b.Z1
}
