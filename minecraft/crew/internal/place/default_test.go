package place

import (
	"errors"
	"sync"
	"testing"
)

// These read the checked-in table, so a regenerated one that loses the
// basics fails here before a planner trusts it.
func TestDefaultTableLoadsAndKnowsStairs(t *testing.T) {
	tb, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if tb.Header.ServerVersion == "" || len(tb.Blocks) < 100 {
		t.Fatalf("header %+v, %d blocks", tb.Header, len(tb.Blocks))
	}
	up, ok := Lookup("oak_stairs", map[string]any{"upside_down_bit": true, "weirdo_direction": 0})
	if !ok {
		t.Fatal("no recipe for an upside-down stair")
	}
	down, ok := Lookup("minecraft:oak_stairs", map[string]any{"upside_down_bit": false, "weirdo_direction": 0})
	if !ok {
		t.Fatal("no recipe for an upright stair")
	}
	if up.Face == down.Face && up.Hit == down.Hit && up.Pitch == down.Pitch {
		t.Errorf("upright and upside-down stairs got the same click: %+v", up)
	}
	if _, ok := Lookup("oak_stairs", map[string]any{"minecraft:corner": "inner_left"}); !ok {
		t.Error("a corner state is derived and must not block a lookup")
	}
}

func TestDefaultTableListsWallConnectionsAsDerived(t *testing.T) {
	d := Derived("cobblestone_wall")
	want := map[string]bool{"wall_post_bit": true, "wall_connection_type_east": true}
	for _, k := range d {
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("derived = %v, missing %v", d, want)
	}
}

func TestDefaultTableReportsAnUnplaceableStateAsMissing(t *testing.T) {
	if _, ok := Lookup("oak_stairs", map[string]any{"weirdo_direction": 9}); ok {
		t.Error("a stair facing 9 does not exist")
	}
	if _, ok := Lookup("no_such_block", nil); ok {
		t.Error("an unknown block has no recipe")
	}
}

func TestErrReportsALoadFailureInsteadOfLookingUnreachable(t *testing.T) {
	if err := Err(); err != nil {
		t.Fatalf("embedded table: %v", err)
	}
	oldLoad := loadDefault
	t.Cleanup(func() {
		loadDefault = oldLoad
		defaultOnce, defaultTable, defaultErr = sync.Once{}, nil, nil
	})
	boom := errors.New("corrupt blob")
	loadDefault = func() (*Table, error) { return nil, boom }
	defaultOnce, defaultTable, defaultErr = sync.Once{}, nil, nil

	if err := Err(); !errors.Is(err, boom) {
		t.Fatalf("Err = %v, want the load error", err)
	}
	tb, err := Default()
	if tb == nil || err == nil {
		t.Fatalf("Default = %v, %v, want an empty table and the error", tb, err)
	}
	if _, ok := Lookup("oak_stairs", nil); ok {
		t.Error("lookup succeeded on a failed table")
	}
	if Derived("oak_stairs") != nil {
		t.Error("derived on a failed table")
	}
}

func TestDefaultTableKnowsStandPositionsDoorHalvesAndSixteenSigns(t *testing.T) {
	r, ok := Lookup("piston", map[string]any{"facing_direction": 1})
	if !ok || r.Stand != "above" {
		t.Errorf("piston facing up = %+v, %v", r, ok)
	}
	if r, ok := Lookup("piston", map[string]any{"facing_direction": 0}); !ok || r.Stand != "below" {
		t.Errorf("piston facing down = %+v, %v", r, ok)
	}
	// The upper half and the hinge follow the other half and the neighbours.
	if _, ok := Lookup("wooden_door", map[string]any{"upper_block_bit": true, "door_hinge_bit": true, "open_bit": false}); !ok {
		t.Error("a door's upper half and hinge are derived and must not block a lookup")
	}
	seen := map[int]bool{}
	tb, _ := Default()
	for _, e := range tb.Blocks["standing_sign"].States {
		if v, ok := e.State["ground_sign_direction"].(float64); ok {
			seen[int(v)] = true
		}
	}
	if len(seen) != 16 {
		t.Errorf("standing sign reaches %d of 16 rotations", len(seen))
	}
}
