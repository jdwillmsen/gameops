package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

type cell struct {
	name  string
	props map[string]any
}

// shaped is a flat floor (top face y=0) with named blocks placed on it.
type shaped map[protocol.BlockPos]cell

func (w shaped) Shape(p protocol.BlockPos) []shape.Box {
	if c, ok := w[p]; ok {
		return shape.Boxes(c.name, c.props)
	}
	if p[1] < 0 {
		return []shape.Box{shape.Full()}
	}
	return nil
}

func (w shaped) Solid(p protocol.BlockPos) bool { return len(w.Shape(p)) > 0 }

func (w shaped) Climbable(p protocol.BlockPos) bool { return w[p].name == "ladder" }

// southward walks the +z direction from the middle of block (0, 0, 0).
func southward(t *testing.T, w Solid, ticks int, jump bool) (*Actuator, *sink, []Pose) {
	t.Helper()
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	var trail []Pose
	for i := 0; i < ticks; i++ {
		if err := a.Tick(Input{Forward: 1, Jump: jump}); err != nil {
			t.Fatal(err)
		}
		trail = append(trail, a.Pose())
	}
	return a, s, trail
}

func TestUnitStepsOntoABottomSlabWithoutJumping(t *testing.T) {
	w := shaped{}
	for z := int32(2); z < 12; z++ {
		w[protocol.BlockPos{0, 0, z}] = cell{"stone_brick_slab", map[string]any{"minecraft:vertical_half": "bottom"}}
	}
	a, s, trail := southward(t, w, 30, false)
	for i, p := range trail {
		if !p.OnGround {
			t.Fatalf("tick %d left the ground at %+v", i, p.Feet)
		}
	}
	if p := a.Pose(); p.Feet.Y() != 0.5 {
		t.Errorf("feet y = %v, want 0.5 on top of the slab", p.Feet.Y())
	}
	rose := false
	for _, in := range s.inputs(t) {
		if hasFlag(in, packet.InputFlagJumping) || hasFlag(in, packet.InputFlagStartJumping) {
			t.Fatal("stepping up must not send a jump")
		}
		// The position rises by the step, but the delta stays the standing
		// one: the server corrects a delta that carries the rise.
		near(t, "delta y", float64(in.Delta.Y()), float64(standingDeltaY), 1e-5)
		if in.Position.Y()-eyeHeight > 0.4 {
			rose = true
		}
	}
	if !rose {
		t.Error("no tick reported the rise")
	}
}

func TestTopSlabIsAWallAtFeetLevel(t *testing.T) {
	w := shaped{{0, 0, 2}: {"stone_brick_slab", map[string]any{"minecraft:vertical_half": "top"}}}
	a, _, _ := southward(t, w, 40, false)
	if p := a.Pose(); p.Feet.Z() > 2-0.3+1e-3 || p.Feet.Y() != 0 {
		t.Errorf("pose = %+v, want held back by a top slab that is 0.5 up", p.Feet)
	}
}

func TestUnitClimbsStairsStepByStep(t *testing.T) {
	// Stairs rising south: each cell one block further and one higher.
	w := shaped{}
	for i := int32(0); i < 4; i++ {
		w[protocol.BlockPos{0, i, 2 + i}] = cell{"oak_stairs", map[string]any{"weirdo_direction": int32(2), "upside_down_bit": false, "minecraft:corner": "none"}}
	}
	for z := int32(6); z < 16; z++ {
		w[protocol.BlockPos{0, 3, z}] = cell{"stone", nil}
	}
	a, s, trail := southward(t, w, 60, false)
	for i, p := range trail {
		if !p.OnGround {
			t.Fatalf("tick %d left the ground at %+v", i, p.Feet)
		}
	}
	if y := a.Pose().Feet.Y(); y < 3.99 {
		t.Errorf("feet y = %v, want to stand on the top stair", y)
	}
	for _, in := range s.inputs(t) {
		if hasFlag(in, packet.InputFlagJumping) {
			t.Fatal("climbing stairs must not send a jump")
		}
	}
}

func TestStairsAscendingTowardsTheUnitAreAWallAtTheirHighSide(t *testing.T) {
	// The high side faces north, towards the unit: a block-high wall.
	w := shaped{{0, 0, 2}: {"oak_stairs", map[string]any{"weirdo_direction": int32(3)}}}
	a, _, _ := southward(t, w, 40, false)
	if p := a.Pose(); p.Feet.Y() != 0 || p.Feet.Z() > 2-0.3+1e-3 {
		t.Errorf("pose = %+v", p.Feet)
	}
}

func TestStairsDescendingAwayFromTheUnitAreWalkedDown(t *testing.T) {
	// Entered from the high side the unit steps onto the low half, then the
	// floor, without a jump.
	w := shaped{{0, 0, 2}: {"oak_stairs", map[string]any{"weirdo_direction": int32(3)}}}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 3.5}})
	for i := 0; i < 30; i++ {
		_ = a.Tick(Input{Forward: 1, Yaw: 180})
	}
	if p := a.Pose(); p.Feet.Z() > 1 || p.Feet.Y() != 0 {
		t.Errorf("pose = %+v, want past the stair", p.Feet)
	}
}

func TestWalkingOffAHalfBlockStepFalls(t *testing.T) {
	w := shaped{{0, 0, 1}: {"stone_slab", map[string]any{"minecraft:vertical_half": "bottom"}}}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0.5, 1.5}})
	a.pose.Carry = mgl32.Vec2{}
	for i := 0; i < 30; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	if p := a.Pose(); p.Feet.Y() != 0 || !p.OnGround {
		t.Errorf("pose = %+v, want back on the floor", p)
	}
}

func TestFenceBlocksAJumpingUnit(t *testing.T) {
	for _, name := range []string{"oak_fence", "cobblestone_wall"} {
		w := shaped{{0, 0, 2}: {name, nil}, {0, 0, 3}: {name, nil}}
		a, _, trail := southward(t, w, 60, true)
		for _, p := range trail {
			if p.Feet.Z() > 2.075+1e-3 {
				t.Fatalf("%s: unit got to z=%v at y=%v", name, p.Feet.Z(), p.Feet.Y())
			}
		}
		if z := a.Pose().Feet.Z(); z > 2.075+1e-3 {
			t.Errorf("%s: z = %v, want held at the post", name, z)
		}
	}
}

func TestUnitWalksThroughTheGapBetweenFencePostsOnlyWhereThereIsNoRail(t *testing.T) {
	// Posts at x=0 and x=2 with a rail between them closed by connections.
	post := map[string]any{"minecraft:connection_east": true}
	rail := map[string]any{"minecraft:connection_east": true, "minecraft:connection_west": true}
	w := shaped{{0, 0, 2}: {"oak_fence", post}, {1, 0, 2}: {"oak_fence", rail}, {2, 0, 2}: {"oak_fence", map[string]any{"minecraft:connection_west": true}}}
	a, _, _ := southward(t, w, 40, true)
	if z := a.Pose().Feet.Z(); z > 2.075+1e-3 {
		t.Errorf("unit passed a fence line at z=%v", z)
	}
}

func TestOpenGateLetsTheUnitThroughAndClosedOneDoesNot(t *testing.T) {
	closed := shaped{{0, 0, 2}: {"fence_gate", map[string]any{"minecraft:cardinal_direction": "north", "open_bit": false}}}
	open := shaped{{0, 0, 2}: {"fence_gate", map[string]any{"minecraft:cardinal_direction": "north", "open_bit": true}}}
	a, _, _ := southward(t, closed, 40, false)
	if z := a.Pose().Feet.Z(); z > 2.375-0.3+1e-3 {
		t.Errorf("closed gate: z = %v", z)
	}
	a, _, _ = southward(t, open, 40, false)
	if z := a.Pose().Feet.Z(); z < 4 {
		t.Errorf("open gate: z = %v, want through", z)
	}
}

func TestCarpetAndLowSnowAreWalkedOverAndHighSnowIsNot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]any
		top   float32
		over  bool
	}{
		{"white_carpet", nil, 1.0 / 16, true},
		{"snow_layer", map[string]any{"height": int32(0)}, 0, true},
		{"snow_layer", map[string]any{"height": int32(4)}, 0.5, true},
		{"snow_layer", map[string]any{"height": int32(5)}, 0.625, false},
	} {
		w := shaped{}
		for z := int32(2); z < 14; z++ {
			w[protocol.BlockPos{0, 0, z}] = cell{tc.name, tc.props}
		}
		a, _, _ := southward(t, w, 40, false)
		p := a.Pose()
		if tc.over && (p.Feet.Z() < 5.5 || p.Feet.Y() != tc.top || !p.OnGround) {
			t.Errorf("%s %v: pose %+v, want walked over at y=%v", tc.name, tc.props, p.Feet, tc.top)
		}
		if !tc.over && p.Feet.Z() > 2-0.3+1e-3 {
			t.Errorf("%s %v: stepped up %v", tc.name, tc.props, p.Feet)
		}
	}
}

func TestStepUpIsRefusedUnderALowCeiling(t *testing.T) {
	w := shaped{
		{0, 0, 2}: {"stone_slab", map[string]any{"minecraft:vertical_half": "bottom"}},
		// A ceiling 1.8 above the floor leaves no room to stand on the slab.
		{0, 1, 2}: {"stone_slab", map[string]any{"minecraft:vertical_half": "top"}},
		{0, 1, 1}: {"stone_slab", map[string]any{"minecraft:vertical_half": "top"}},
		{0, 1, 0}: {"stone_slab", map[string]any{"minecraft:vertical_half": "top"}},
	}
	a, _, _ := southward(t, w, 40, false)
	if p := a.Pose(); p.Feet.Y() != 0 || p.Feet.Z() > 2-0.3+1e-3 {
		t.Errorf("pose = %+v, want blocked", p.Feet)
	}
}

func TestClosedDoorBlocksAndOpenOneWithItsPanelAlongTheWayDoesNot(t *testing.T) {
	// Facing north when closed puts the panel against the south edge.
	closed := map[string]any{"minecraft:cardinal_direction": "east", "open_bit": false}
	w := shaped{{0, 0, 2}: {"wooden_door", closed}, {0, 1, 2}: {"wooden_door", closed}}
	a, _, _ := southward(t, w, 40, false)
	if z := a.Pose().Feet.Z(); z > 2+1-0.1825-0.3+1e-3 {
		t.Errorf("closed door: z = %v", z)
	}
}

func TestOpenTrapdoorIsAWallAndAClosedOneAStep(t *testing.T) {
	w := shaped{{0, 0, 2}: {"trapdoor", map[string]any{"direction": int32(2), "open_bit": false, "upside_down_bit": false}}}
	a, _, _ := southward(t, w, 40, false)
	if p := a.Pose(); p.Feet.Z() < 4 || p.Feet.Y() != 0 {
		t.Errorf("closed trapdoor: %+v, want walked over and back down", p.Feet)
	}
}

func TestWorldWithoutShapesStillMeansFullCubes(t *testing.T) {
	w := flat()
	w.extra = map[protocol.BlockPos]bool{{0, 0, 2}: true}
	a, _, _ := southward(t, w, 40, false)
	if p := a.Pose(); p.Feet.Y() != 0 || p.Feet.Z() > 2-0.3+1e-3 {
		t.Errorf("pose = %+v: a full cube is a wall", p.Feet)
	}
}

func ladderColumn(height int32) shaped {
	w := shaped{}
	for y := int32(0); y < height; y++ {
		// Facing 2 mounts the panel on the cell's south side.
		w[protocol.BlockPos{0, y, 3}] = cell{"ladder", map[string]any{"facing_direction": int32(2)}}
	}
	return w
}

func TestUnitPressingIntoALadderClimbsAtASteadyRate(t *testing.T) {
	_, _, trail := southward(t, ladderColumn(4), 40, false)
	var rises []float32
	prev := float32(0)
	for _, p := range trail {
		if p.Feet.Y() > prev {
			rises = append(rises, p.Feet.Y()-prev)
		}
		prev = p.Feet.Y()
	}
	if len(rises) < 15 {
		t.Fatalf("the unit rose on %d ticks, want it to climb the column", len(rises))
	}
	for _, r := range rises[1 : len(rises)-4] {
		near(t, "rise per tick", float64(r), ladderClimb, 1e-4)
	}
	if top := prev; top < 4 {
		t.Errorf("unit ended at y=%v, want over the top of a 4-high ladder", top)
	}
}

func TestUnitOnALadderSlidesNoFasterThanTheSlide(t *testing.T) {
	a, _ := newAct(ladderColumn(30), Pose{Feet: mgl32.Vec3{0.5, 20, 3.5}})
	a.pose.OnGround = false
	prev := a.Pose().Feet.Y()
	for i := 0; i < 12; i++ {
		_ = a.Tick(Input{})
		y := a.Pose().Feet.Y()
		if prev-y > ladderSlide+1e-5 {
			t.Fatalf("tick %d fell %v, faster than the slide", i, prev-y)
		}
		prev = y
	}
	if prev > 20-0.5 {
		t.Errorf("unit hung at y=%v, want it sliding", prev)
	}
}

func TestStandingInALadderCellWithoutPressingDoesNotClimb(t *testing.T) {
	w := ladderColumn(4)
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 3.2}})
	for i := 0; i < 10; i++ {
		_ = a.Tick(Input{})
	}
	if y := a.Pose().Feet.Y(); y != 0 {
		t.Errorf("standing in a ladder cell moved the unit to y=%v", y)
	}
}
