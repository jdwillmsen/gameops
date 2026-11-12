package skill

import (
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// A bed's respawn point is where the unit stood when it tapped, which is
	// within a few blocks of the bed, so that is how far the search goes.
	findBedSpan = 4
	findBedRise = 2
)

// FindBed looks for a bed in the loaded blocks around a respawn point, nearest
// first, and says where it is. The server remembers only the point and not the
// bed, so a bed that was broken since is simply not found.
func FindBed(world ChestWorld, near protocol.BlockPos) (protocol.BlockPos, bool) {
	best, bestD, found := protocol.BlockPos{}, math.MaxFloat64, false
	for dx := int32(-findBedSpan); dx <= findBedSpan; dx++ {
		for dz := int32(-findBedSpan); dz <= findBedSpan; dz++ {
			for dy := int32(-findBedRise); dy <= findBedRise; dy++ {
				c := protocol.BlockPos{near.X() + dx, near.Y() + dy, near.Z() + dz}
				name, known := world.Block(c)
				if !known || !isBed(name) {
					continue
				}
				if d := float64(dx*dx + dy*dy + dz*dz); d < bestD {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}
