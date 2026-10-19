package path

import (
	"errors"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type block struct {
	name  string
	props map[string]any
}

// stated is a world whose blocks carry states, on a stone floor with its top
// face at y=1 and air above, in x and z from 0 to 8.
type stated map[protocol.BlockPos]block

func statedFlat() stated {
	w := stated{}
	for x := int32(0); x < 8; x++ {
		for z := int32(0); z < 8; z++ {
			w[protocol.BlockPos{x, 0, z}] = block{"stone", nil}
			for y := int32(1); y <= 8; y++ {
				w[protocol.BlockPos{x, y, z}] = block{"air", nil}
			}
		}
	}
	return w
}

func (w stated) State(p protocol.BlockPos) (string, map[string]any, bool) {
	b, ok := w[p]
	return b.name, b.props, ok
}

func (w stated) Block(p protocol.BlockPos) (string, bool) {
	b, ok := w[p]
	return b.name, ok
}

func (w stated) set(x, y, z int32, name string, props map[string]any) {
	w[protocol.BlockPos{x, y, z}] = block{name, props}
}

// jumps counts the moves of a plan that need a jump.
func jumps(t *testing.T, w Blocks, from protocol.BlockPos, plan []protocol.BlockPos) int {
	t.Helper()
	n := 0
	for _, to := range plan {
		y, _ := Footing(w, from)
		if NeedsJump(w, from, y, to) {
			n++
		}
		from = to
	}
	return n
}

func bottom() map[string]any { return map[string]any{"minecraft:vertical_half": "bottom"} }

func TestSlabsAreWalkedWithoutAJump(t *testing.T) {
	w := statedFlat()
	for x := int32(2); x <= 4; x++ {
		w.set(x, 1, 0, "stone_brick_slab", bottom())
	}
	from, to := protocol.BlockPos{0, 1, 0}, protocol.BlockPos{5, 1, 0}
	plan, err := Find(w, from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n := jumps(t, w, from, plan); n != 0 {
		t.Errorf("plan %v jumps %d times, want a walk", plan, n)
	}
	if y, ok := Footing(w, protocol.BlockPos{3, 1, 0}); !ok || y != 1.5 {
		t.Errorf("footing on a slab = %v, %v, want 1.5", y, ok)
	}
}

func TestStairsAreClimbedWithoutAJumpButFullBlocksAreJumped(t *testing.T) {
	stairs := statedFlat()
	for i := int32(0); i < 3; i++ {
		stairs.set(2+i, 1+i, 0, "oak_stairs", map[string]any{"weirdo_direction": int32(0), "minecraft:corner": "none"})
		for y := int32(1); y < 1+i; y++ {
			stairs.set(2+i, y, 0, "stone", nil)
		}
	}
	from, to := protocol.BlockPos{0, 1, 0}, protocol.BlockPos{4, 4, 0}
	plan, err := Find(stairs, from, to, 0)
	if err != nil {
		t.Fatalf("stairs: %v", err)
	}
	if n := jumps(t, stairs, from, plan); n != 0 {
		t.Errorf("stairs plan %v jumps %d times", plan, n)
	}

	blocks := statedFlat()
	for i := int32(0); i < 3; i++ {
		for y := int32(1); y <= 1+i; y++ {
			blocks.set(2+i, y, 0, "stone", nil)
		}
	}
	plan, err = Find(blocks, from, protocol.BlockPos{4, 4, 0}, 0)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if n := jumps(t, blocks, from, plan); n != 3 {
		t.Errorf("full-block steps jump %d times, want 3", n)
	}
}

func TestStairsAreDescendedWithoutAJump(t *testing.T) {
	w := statedFlat()
	for i := int32(0); i < 3; i++ {
		w.set(2+i, 1+i, 0, "oak_stairs", map[string]any{"weirdo_direction": int32(0), "minecraft:corner": "none"})
		for y := int32(1); y < 1+i; y++ {
			w.set(2+i, y, 0, "stone", nil)
		}
	}
	from, to := protocol.BlockPos{4, 4, 0}, protocol.BlockPos{0, 1, 0}
	plan, err := Find(w, from, to, 0)
	if err != nil {
		t.Fatalf("down the stairs: %v", err)
	}
	if n := jumps(t, w, from, plan); n != 0 {
		t.Errorf("plan %v jumps %d times", plan, n)
	}
}

func TestFencesAndWallsAreWallsEvenWhenOnlyOneHigh(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
	}{
		{"oak_fence", map[string]any{"minecraft:connection_north": true, "minecraft:connection_south": true}},
		{"cobblestone_wall", map[string]any{"wall_connection_type_north": "tall", "wall_connection_type_south": "tall"}},
	} {
		w := statedFlat()
		for z := int32(0); z < 8; z++ {
			w.set(3, 1, z, tc.name, tc.props)
		}
		_, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{6, 1, 0}, 0)
		if !errors.Is(err, ErrNoPath) {
			t.Errorf("%s line: err = %v, want no path", tc.name, err)
		}
		// With the line open at one end the plan goes around.
		delete(w, protocol.BlockPos{3, 1, 7})
		w.set(3, 1, 7, "air", nil)
		plan, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{6, 1, 0}, 0)
		if err != nil || plan[len(plan)-1] != (protocol.BlockPos{6, 1, 0}) {
			t.Errorf("%s line with a gap: %v, %v", tc.name, plan, err)
		}
	}
}

func TestStandingOnAFenceIsNotAStepOntoIt(t *testing.T) {
	w := statedFlat()
	w.set(2, 1, 0, "oak_fence", nil)
	if _, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{2, 2, 0}, 0); !errors.Is(err, ErrNoPath) {
		t.Errorf("err = %v, want a fence top that cannot be reached from the ground", err)
	}
}

func TestOpenGatesPassAndClosedOnesDoNot(t *testing.T) {
	for _, tc := range []struct {
		open bool
		ok   bool
	}{{true, true}, {false, false}} {
		w := statedFlat()
		for z := int32(0); z < 8; z++ {
			w.set(3, 1, z, "stone", nil)
			w.set(3, 2, z, "stone", nil)
		}
		w.set(3, 2, 0, "air", nil)
		w.set(3, 1, 0, "fence_gate", map[string]any{"minecraft:cardinal_direction": "east", "open_bit": tc.open})
		_, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{6, 1, 0}, 0)
		if (err == nil) != tc.ok {
			t.Errorf("gate open=%v: err = %v", tc.open, err)
		}
	}
}

func TestCarpetAndShallowSnowAreWalkedAndDeepSnowIsJumped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		jump  bool
	}{
		{"white_carpet", nil, false},
		{"snow_layer", map[string]any{"height": int32(4)}, false},
		{"snow_layer", map[string]any{"height": int32(5)}, true},
	} {
		w := statedFlat()
		w.set(2, 1, 0, tc.name, tc.props)
		from, to := protocol.BlockPos{1, 1, 0}, protocol.BlockPos{2, 1, 0}
		if got := NeedsJump(w, from, 1, to); got != tc.jump {
			t.Errorf("%s %v: NeedsJump = %v, want %v", tc.name, tc.props, got, tc.jump)
		}
		if !Standable(w, to) {
			t.Errorf("%s %v: not standable", tc.name, tc.props)
		}
	}
}

func TestALowCeilingOverASlabMeansNoFooting(t *testing.T) {
	w := statedFlat()
	w.set(2, 1, 0, "stone_slab", bottom())
	w.set(2, 2, 0, "stone_slab", map[string]any{"minecraft:vertical_half": "top"})
	if Standable(w, protocol.BlockPos{2, 1, 0}) {
		t.Error("a slab under a top slab 1.0 above leaves 1.0 of headroom")
	}
}

func TestColliderShapesComeFromStates(t *testing.T) {
	w := statedFlat()
	w.set(2, 1, 0, "stone_slab", bottom())
	c := NewSolid(w)
	if got := c.Shape(protocol.BlockPos{2, 1, 0}); len(got) != 1 || got[0].Max[1] != 0.5 {
		t.Errorf("slab shape = %v", got)
	}
	if !c.Solid(protocol.BlockPos{2, 1, 0}) || c.Solid(protocol.BlockPos{2, 2, 0}) {
		t.Error("slab must collide and air above must not")
	}
	if !c.Solid(protocol.BlockPos{20, 1, 0}) {
		t.Error("an unloaded block must collide")
	}
}
