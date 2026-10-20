package path

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestAUnitInAStairsCellStandsOnItsLowerHalf(t *testing.T) {
	w := statedFlat()
	w.set(2, 1, 0, "oak_stairs", map[string]any{"weirdo_direction": int32(0), "minecraft:corner": "none"})
	w.set(3, 1, 0, "stone", nil)
	from, to := protocol.BlockPos{2, 1, 0}, protocol.BlockPos{3, 2, 0}
	// From the lower half (1.5) the rise to the block's top (2.0) is a step;
	// a start at the cell floor (1.0) would be a jump.
	plan, err := Find(w, from, to, 0)
	if err != nil || len(plan) != 1 || plan[0] != to {
		t.Fatalf("plan %v, err %v", plan, err)
	}
	if NeedsJump(w, from, 1.5, to) {
		t.Error("from the lower half the move is a step")
	}
	if !NeedsJump(w, from, 1, to) {
		t.Error("from the floor of the cell the same move is a jump")
	}
	if y, _ := newView(w).landing(from); y != from {
		t.Errorf("landing moved the unit to %v", y)
	}
	if _, y := newView(w).landing(from); y != 1.5 {
		t.Errorf("start height = %v, want 1.5", y)
	}
}
