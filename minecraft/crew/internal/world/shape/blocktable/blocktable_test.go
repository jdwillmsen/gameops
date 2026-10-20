package blocktable

import (
	"math"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

// centreTop is the height a unit dropped on the middle of a block rests at,
// or 0 if nothing is under it.
func centreTop(boxes []shape.Box) float32 {
	top := float32(0)
	for _, b := range boxes {
		if b.Min[0] <= 0.5 && b.Max[0] >= 0.5 && b.Min[2] <= 0.5 && b.Max[2] >= 0.5 && b.Max[1] > top {
			top = b.Max[1]
		}
	}
	return top
}

// The heights below were measured on the lab server by dropping a unit on the
// middle of each block and reading where it came to rest; the blocks were
// placed with commands, so only states that survive placement are listed.
func TestTheTableRestsAUnitWhereTheLabServerDid(t *testing.T) {
	for _, tc := range []struct {
		name string
		top  float32
	}{
		{"chest", 0.95}, {"ender_chest", 0.95}, {"grass_path", 0.9375},
		{"enchanting_table", 0.75}, {"end_portal_frame", 0.8125},
		{"campfire", 0.4375}, {"soul_campfire", 0.4375},
		{"stonecutter_block", 0.5625}, {"honey_block", 1}, {"anvil", 1},
		{"candle", 0.375}, {"scaffolding", 1}, {"lantern", 0.5},
		{"decorated_pot", 1}, {"end_rod", 1}, {"iron_chain", 1},
	} {
		boxes, ok := Boxes(tc.name, nil)
		if !ok {
			t.Errorf("%s: unknown to the table", tc.name)
			continue
		}
		if got := centreTop(boxes); math.Abs(float64(got-tc.top)) > 1e-4 {
			t.Errorf("%s: top = %v, want %v", tc.name, got, tc.top)
		}
	}
}

func TestBlocksThatNeedTheirNeighboursAreLeftToTheCaller(t *testing.T) {
	for _, name := range []string{"glass_pane", "iron_bars"} {
		if boxes, ok := Boxes(name, nil); ok {
			t.Errorf("%s answered %v without a world to read", name, boxes)
		}
	}
}

func TestLiquidsAndHazardsStaySolid(t *testing.T) {
	for _, name := range []string{"water", "lava", "fire", "web"} {
		if _, ok := Boxes(name, nil); ok {
			t.Errorf("%s must stay a cube", name)
		}
	}
}

func TestPlantsAndSignsHaveNoCollision(t *testing.T) {
	for _, name := range []string{"allium", "wheat", "standing_sign", "redstone_wire", "reeds"} {
		boxes, ok := Boxes(name, nil)
		if !ok || len(boxes) != 0 {
			t.Errorf("%s = %v, %v, want no collision", name, boxes, ok)
		}
	}
}
