package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestMoveCountersCountSprintTicksAndCorrections(t *testing.T) {
	var c MoveCounters
	a, _ := newAct(flat(), Pose{})
	a.CountMovement(&c)

	for range 3 {
		if err := a.Tick(Input{Forward: 1, Sprint: true}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := a.Tick(Input{Forward: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.SprintTicks(); got != 3 {
		t.Errorf("sprint ticks = %d, want the 3 sprinting inputs only", got)
	}
	if got := c.Corrections(); got != 0 {
		t.Errorf("corrections = %d before the server corrected anything", got)
	}

	a.Apply(&packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{4, 9, 9}, OnGround: true})
	if got := c.Corrections(); got != 1 {
		t.Errorf("corrections = %d, want 1", got)
	}
}

func TestMoveCountersAreOptional(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	if err := a.Tick(Input{Forward: 1, Sprint: true}); err != nil {
		t.Fatal(err)
	}
	a.Apply(&packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{4, 9, 9}, OnGround: true})
}
