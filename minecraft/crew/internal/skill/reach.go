package skill

import (
	"errors"
	"fmt"
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// placeSpan bounds the stand-cell search around a target.
const placeSpan = 4

// approach brings the unit's eye within placement reach of a cell. Placing and
// breaking share it: both are acted at the server's own reach.
type approach struct {
	world  path.Blocks
	target protocol.BlockPos
	nav    *navigator
}

// step reports arrived once the eye is within reach, sending no input; until
// then it walks and sends one. A failure carries st == Failed.
func (ap *approach) step(a Actuator) (arrived bool, st Status, err error) {
	pose := a.Pose()
	if centre(ap.target).Sub(eyeOf(pose.Feet)).Len() <= placeReach-arriveSlack {
		return true, Done, nil
	}
	if ap.nav == nil {
		stand, ok := ap.standCell(pose)
		if !ok {
			return false, Failed, errors.New("no standing spot within reach of the target")
		}
		ap.nav = newNavigator(ap.world, stand, arriveSlack)
	}
	st, err = ap.nav.step(a)
	switch st {
	case Running:
		return false, Running, nil
	case Done:
		ap.nav = nil
		return true, Done, nil
	}
	return false, Failed, fmt.Errorf("getting to the target: %w", err)
}

// standCell picks the standable cell within reach of the target, nearest the
// unit.
func (ap *approach) standCell(pose act.Pose) (protocol.BlockPos, bool) {
	return standNear(ap.world, ap.target, pose, 0)
}

// standNear finds the standable cell within reach of t, nearest the unit. A
// cell whose feet or head would be in the target is not one: the server
// refuses a placement into the unit's own body. Cells closer than apart to the
// target's column, horizontally, are left out, for a caller that must not end
// up with its body touching the target.
func standNear(world path.Blocks, t protocol.BlockPos, pose act.Pose, apart float32) (protocol.BlockPos, bool) {
	best, bestD, found := protocol.BlockPos{}, float32(1e30), false
	for dx := int32(-placeSpan); dx <= placeSpan; dx++ {
		for dy := int32(-placeSpan); dy <= placeSpan; dy++ {
			for dz := int32(-placeSpan); dz <= placeSpan; dz++ {
				c := protocol.BlockPos{t.X() + dx, t.Y() + dy, t.Z() + dz}
				if c.X() == t.X() && c.Z() == t.Z() && (c.Y() == t.Y() || c.Y()+1 == t.Y()) {
					continue
				}
				if centre(t).Sub(standEye(c)).Len() > placeReach-arriveSlack || !path.Standable(world, c) {
					continue
				}
				if apart > 0 && math.Hypot(float64(dx), float64(dz)) < float64(apart) {
					continue
				}
				if d := centre(c).Sub(pose.Feet).LenSqr(); d < bestD {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}
