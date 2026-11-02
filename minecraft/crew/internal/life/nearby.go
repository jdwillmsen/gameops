package life

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const (
	// nearbyRange is how far around the unit a mob or player is counted.
	nearbyRange = 16.0
	// nearbyCap bounds each count: past it the exact number tells an
	// operator nothing more, and it keeps the answer small.
	nearbyCap = 9
	// eyeToFeet turns the snapshot's eye-height position into the feet that
	// entity positions are measured from.
	eyeToFeet = 1.62
)

// Nearby is how many hostile mobs and other players are close to the unit.
// It is counts alone: no names, types or positions leave the unit.
type Nearby struct {
	Hostiles int `json:"hostiles"`
	Players  int `json:"players"`
}

// Nearby counts, in one pass under the entities' read lock, the hostile mobs
// and the players within range of a unit whose eyes are at eye. Other crew
// units are players like any other.
func (l *Life) Nearby(eye [3]float32) Nearby {
	feet := mgl32.Vec3{eye[0], eye[1] - eyeToFeet, eye[2]}
	var n Nearby
	l.ents.Each(func(e belief.Entity) {
		if e.Pos.Sub(feet).Len() > nearbyRange {
			return
		}
		switch {
		case e.Player:
			n.Players++
		case Threat(e.Type):
			n.Hostiles++
		}
	})
	n.Hostiles, n.Players = min(n.Hostiles, nearbyCap), min(n.Players, nearbyCap)
	return n
}
