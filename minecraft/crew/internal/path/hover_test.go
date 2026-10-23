package path

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// holeWorld is flat ground with a one-deep hole at (3, 0, 3) leaving its
// floor at the level below the surface.
func holeWorld() stated {
	w := statedFlat()
	w.set(3, 0, 3, "air", nil)
	w.set(3, -1, 3, "stone", nil)
	return w
}

func TestAUnitInMidAirOverAShaftPlansFromWhereItLands(t *testing.T) {
	w := holeWorld()
	for _, y := range []float32{1, 2.04, 3.04} {
		plan, err := FindFromPos(w, 0.5, y, 3.5, protocol.BlockPos{6, 1, 3}, 0)
		if err != nil || len(plan) == 0 {
			t.Errorf("start at y=%v: plan %v, err %v", y, plan, err)
		}
	}
	for _, y := range []float32{1.5, 2.5} {
		plan, err := FindFromPos(w, 3.5, y, 3.5, protocol.BlockPos{6, 1, 3}, 0)
		if err != nil || len(plan) == 0 {
			t.Errorf("hovering over the hole at y=%v: plan %v, err %v", y, plan, err)
		}
	}
}

func TestAGoalOverAHoleIsReachedAtTheFloorTheUnitWouldFallTo(t *testing.T) {
	w := holeWorld()
	goal := protocol.BlockPos{3, 1, 3}
	if Standable(w, goal) {
		t.Fatal("test world: the goal cell must hang over the hole")
	}
	if got, y := Landing(w, goal); got != (protocol.BlockPos{3, 0, 3}) || y != 0 {
		t.Fatalf("Landing = %v at %v, want the hole's floor cell", got, y)
	}
	plan, err := FindFromPos(w, 0.5, 1, 3.5, goal, 0)
	if err != nil {
		t.Fatalf("no path to a goal that hangs over a hole: %v", err)
	}
	if last := plan[len(plan)-1]; last != (protocol.BlockPos{3, 0, 3}) {
		t.Errorf("plan %v ends off the hole's floor", plan)
	}
}
