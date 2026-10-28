package blueprint

import "testing"

// A 2x1x3 blueprint: stone with a torch on top at one end, so rotation moves
// both cells and the order must keep the torch after the stone below it.
func tinyBlueprint(t *testing.T) *Blueprint {
	t.Helper()
	bp, err := Parse([]byte(`{"name":"tiny","kind":"house","size":{"x":2,"y":2,"z":3},
		"palette":[{"name":"air"},{"name":"stone"},{"name":"torch","states":{"torch_facing_direction":"top"}}],
		"layers":["111111","200000"],
		"materials":[{"item":"stone","count":6},{"item":"torch","count":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return bp
}

func TestFootprintSwapsAxesOnQuarterTurns(t *testing.T) {
	bp := tinyBlueprint(t)
	for rot, want := range map[int][2]int{0: {2, 3}, 90: {3, 2}, 180: {2, 3}, 270: {3, 2}} {
		if w, d := bp.Footprint(rot); w != want[0] || d != want[1] {
			t.Errorf("rotation %d: footprint %dx%d, want %dx%d", rot, w, d, want[0], want[1])
		}
	}
}

func TestCellsStayInsideTheRotatedFootprintAndKeepSupportFirst(t *testing.T) {
	bp := tinyBlueprint(t)
	for _, rot := range []int{0, 90, 180, 270} {
		w, d := bp.Footprint(rot)
		cells := bp.Cells(rot)
		if len(cells) != 7 {
			t.Fatalf("rotation %d: %d cells, want 7", rot, len(cells))
		}
		seen := map[Position]bool{}
		for _, c := range cells {
			if c.Pos.X < 0 || c.Pos.X >= w || c.Pos.Z < 0 || c.Pos.Z >= d || c.Pos.Y < 0 || c.Pos.Y >= 2 {
				t.Errorf("rotation %d: cell %v outside %dx%d", rot, c.Pos, w, d)
			}
			if c.Block.Name == "torch" && !seen[Position{c.Pos.X, 0, c.Pos.Z}] {
				t.Errorf("rotation %d: torch ordered before the stone under it", rot)
			}
			seen[c.Pos] = true
		}
	}
}

func TestCellsLeaveOutAirAndUpperDoorHalves(t *testing.T) {
	bp := loadHouse(t)
	cells := bp.Cells(0)
	doors := 0
	for _, c := range cells {
		if c.Block.Name == "air" {
			t.Fatal("air in cells")
		}
		if c.Block.Name == "wooden_door" {
			doors++
			if c.Block.States["upper_block_bit"] == true {
				t.Error("upper door half in cells")
			}
		}
	}
	if doors != 1 {
		t.Errorf("%d doors, want 1", doors)
	}
}

func loadHouse(t *testing.T) *Blueprint {
	t.Helper()
	bp, err := Load("../../blueprints/small_house.json")
	if err != nil {
		t.Fatal(err)
	}
	return bp
}
