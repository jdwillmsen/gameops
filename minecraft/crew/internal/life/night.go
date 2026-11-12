package life

import (
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// atHomeRange is how close to its shelter spot, horizontally, the unit counts
// as inside it: the cell is one block wide, so a block and a half covers a
// unit pushed off its middle.
const atHomeRange = 1.5

// ShouldShelter says a unit ought to start the shelter task: it is dusk or
// night in the overworld, the unit is alive and free to act, it has no bed to
// sleep in, and it is not already standing in the shelter spot home names.
//
// Whether the unit is in a fight is the caller's to check, as the Life
// reflex already knows it; this takes only what a snapshot and the clock say,
// so it can be tested without a world. An unannounced time says nothing, and
// the unit is not sent to hide on a guess.
func ShouldShelter(s belief.Snapshot, env belief.Env, haveBed bool, home *protocol.BlockPos) bool {
	if !s.Spawned || s.Dead || s.Dimension != 0 || s.Creative {
		return false
	}
	if env.DayPhase != belief.PhaseDusk && env.DayPhase != belief.PhaseNight {
		return false
	}
	if haveBed {
		return false
	}
	return !inShelter(s, home)
}

// inShelter says the unit stands in the cell it walled itself into. Position
// is the eye, so the height is judged against the cell's head.
func inShelter(s belief.Snapshot, home *protocol.BlockPos) bool {
	if home == nil || !s.PositionKnown {
		return false
	}
	dx := float64(s.Position[0]) - (float64(home.X()) + 0.5)
	dz := float64(s.Position[2]) - (float64(home.Z()) + 0.5)
	feetY := float64(s.Position[1]) - eyeToFeet
	return math.Hypot(dx, dz) <= atHomeRange && math.Abs(feetY-float64(home.Y())) <= 1
}
