package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape/blocktable"
)

func init() { shape.Fallback = blocktable.Boxes }

// A path is 1/16 below a full block, so a unit walking down one stands a hair
// lower than on stone and must neither climb nor fall as it goes.
func TestWalkingAGrassPathLineNeverStepsOrFalls(t *testing.T) {
	w := shaped{}
	for z := int32(0); z < 14; z++ {
		w[protocol.BlockPos{0, -1, z}] = cell{"grass_path", nil}
	}
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, -0.0625, 0.5}})
	for i := 0; i < 45; i++ {
		if err := a.Tick(Input{Forward: 1}); err != nil {
			t.Fatal(err)
		}
		if p := a.Pose(); !p.OnGround || p.Feet.Y() != -0.0625 {
			t.Fatalf("tick %d: pose %+v, want standing on the path", i, p)
		}
	}
	for i, in := range s.inputs(t) {
		if hasFlag(in, packet.InputFlagJumping) || hasFlag(in, packet.InputFlagStartJumping) {
			t.Fatalf("tick %d jumped", i)
		}
		near(t, "delta y", float64(in.Delta.Y()), float64(standingDeltaY), 1e-5)
	}
	if z := a.Pose().Feet.Z(); z < 8 {
		t.Errorf("z = %v: the unit did not walk the line", z)
	}
}

func TestStepOffAPathOntoStoneIsOneSixteenthWithNoJump(t *testing.T) {
	w := shaped{}
	for z := int32(0); z < 2; z++ {
		w[protocol.BlockPos{0, -1, z}] = cell{"grass_path", nil}
	}
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, -0.0625, 0.5}})
	for i := 0; i < 20; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	if p := a.Pose(); p.Feet.Y() != 0 || !p.OnGround {
		t.Errorf("pose %+v, want on the stone at y=0", p)
	}
	for _, in := range s.inputs(t) {
		if hasFlag(in, packet.InputFlagJumping) {
			t.Fatal("a 1/16 step must not send a jump")
		}
	}
}

// A chest is inset a fortieth from every side and 0.95 high.
func TestAChestStopsAUnitAtItsInsetFaceAndCanBeStoodOn(t *testing.T) {
	w := shaped{{0, 0, 2}: {"chest", nil}}
	a, _, _ := southward(t, w, 40, false)
	// Walked into from the north the unit meets the face at z=2.025.
	if p := a.Pose(); p.Feet.Z() > 2.025-halfWidth+1e-3 || p.Feet.Z() < 2.025-halfWidth-1e-3 || p.Feet.Y() != 0 {
		t.Errorf("pose %v, want held at the chest's face", p.Feet)
	}
	// 0.95 is more than a step, so only a jump gets on top.
	_, _, trail := southward(t, w, 40, true)
	ontop := false
	for _, p := range trail {
		if p.OnGround && p.Feet.Y() == 0.95 {
			ontop = true
		}
	}
	if !ontop {
		t.Error("a jumping unit never stood on the chest")
	}
}
