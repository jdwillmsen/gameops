package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// walkSouthFrom walks +z from the given x in the block row z=0 and returns the
// pose where the unit ends up.
func walkSouthFrom(t *testing.T, w Solid, x float32, ticks int) Pose {
	t.Helper()
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{x, 0, 0.5}})
	for i := 0; i < ticks; i++ {
		if err := a.Tick(Input{Forward: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return a.Pose()
}

// An open trapdoor is a panel standing on one edge of its block: a wall to a
// unit that walks into it and nothing to one that passes beside it.
func TestOpenTrapdoorStandsOnTheEdgeItsDirectionNames(t *testing.T) {
	// direction 2 hangs the open panel on the north edge, 3 on the south.
	for _, tc := range []struct {
		direction int32
		wantZ     float32
	}{
		{2, 2 - halfWidth},              // meets the panel at z=2 from the north
		{3, 2 + 1 - 0.1825 - halfWidth}, // the panel is at the far edge, z=2.8175
	} {
		w := shaped{{0, 0, 2}: {"trapdoor", map[string]any{"direction": tc.direction, "open_bit": true, "upside_down_bit": false}}}
		p := walkSouthFrom(t, w, 0.5, 40)
		if p.Feet.Z() < tc.wantZ-1e-3 || p.Feet.Z() > tc.wantZ+1e-3 {
			t.Errorf("direction %d: z = %v, want %v", tc.direction, p.Feet.Z(), tc.wantZ)
		}
	}
}

func TestOpenTrapdoorOnAnEdgeLeavesTheRestOfTheBlockWalkable(t *testing.T) {
	// East-west panels run along z, so a unit walking south passes between
	// the panel and the far edge of the block.
	for _, direction := range []int32{0, 1} {
		w := shaped{{0, 0, 2}: {"trapdoor", map[string]any{"direction": direction, "open_bit": true}}}
		p := walkSouthFrom(t, w, 0.5, 40)
		if p.Feet.Z() < 4 {
			t.Errorf("direction %d: stopped at z=%v beside the panel", direction, p.Feet.Z())
		}
	}
}

func TestClosedTrapdoorsAreSteppedOnAtTheMeasuredThickness(t *testing.T) {
	w := shaped{
		{0, 0, 2}: {"trapdoor", map[string]any{"direction": int32(0), "open_bit": false, "upside_down_bit": false}},
		{0, 0, 3}: {"trapdoor", map[string]any{"direction": int32(0), "open_bit": false, "upside_down_bit": false}},
		{0, 0, 4}: {"trapdoor", map[string]any{"direction": int32(0), "open_bit": false, "upside_down_bit": false}},
	}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	for i := 0; i < 24; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	near(t, "feet y on a trapdoor", float64(a.Pose().Feet.Y()), 0.1825, 1e-4)
}

// An open door is a panel on one edge, which edge depending on the hinge: a
// unit walking down the middle of the doorway passes it either way, and one
// pressed against that edge is stopped.
func TestOpenDoorPanelSitsOnTheEdgeItsHingeGives(t *testing.T) {
	for _, tc := range []struct {
		hinge  bool
		startX float32 // the unit's centre, 0.7 off the middle towards the panel
		panelX float32
	}{
		// cardinal west, facing south: an open panel on the east edge; the
		// hinge flips it to the west edge.
		{false, 0.72, 0.8175},
		{true, 0.28, 0.1825},
	} {
		door := map[string]any{"minecraft:cardinal_direction": "west", "open_bit": true, "door_hinge_bit": tc.hinge}
		w := shaped{{0, 0, 2}: {"wooden_door", door}, {0, 1, 2}: {"wooden_door", door}}
		mid := walkSouthFrom(t, w, 0.5, 40)
		if mid.Feet.Z() < 4 {
			t.Errorf("hinge %v: the middle of an open doorway stopped the unit at z=%v", tc.hinge, mid.Feet.Z())
		}
		edge := walkSouthFrom(t, w, tc.startX, 40)
		if edge.Feet.Z() > 2-halfWidth+1e-3 {
			t.Errorf("hinge %v: a unit against the panel's edge got to z=%v, want stopped", tc.hinge, edge.Feet.Z())
		}
	}
}

func TestClosedDoorStopsAUnitFromEitherSide(t *testing.T) {
	door := map[string]any{"minecraft:cardinal_direction": "west", "open_bit": false}
	w := shaped{{0, 0, 2}: {"wooden_door", door}, {0, 1, 2}: {"wooden_door", door}}
	if p := walkSouthFrom(t, w, 0.5, 40); p.Feet.Z() > 2-halfWidth+1e-3 {
		t.Errorf("from the north: z = %v", p.Feet.Z())
	}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 4.5}})
	for i := 0; i < 40; i++ {
		_ = a.Tick(Input{Forward: 1, Yaw: 180})
	}
	// The panel is on the north edge of its cell, so from the south the unit
	// is stopped at the panel's far face.
	near(t, "z from the south", float64(a.Pose().Feet.Z()), 2+0.1825+halfWidth, 1e-3)
}
