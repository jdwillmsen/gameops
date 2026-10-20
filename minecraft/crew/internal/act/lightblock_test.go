package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Light blocks hang over the lab on a grid, two above a standing unit's feet.
// Players pass through them, so a jump under one must rise the whole way.
func TestJumpingUnderALightBlockRisesTheFullHeight(t *testing.T) {
	for _, name := range []string{"light_block_15", "light_block_0", "light_block", "structure_void"} {
		w := shaped{{0, 2, 0}: {name, nil}}
		a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
		apex := float32(0)
		for i := 0; i < 20; i++ {
			_ = a.Tick(Input{Jump: i == 0})
			apex = max(apex, a.Pose().Feet.Y())
		}
		if apex < 1.2 {
			t.Errorf("%s: apex %v, want about 1.25: the block is a phantom ceiling", name, apex)
		}
	}
	// The same cell filled with stone does stop the jump, so the test sees it.
	a, _ := newAct(shaped{{0, 2, 0}: {"stone", nil}}, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	apex := float32(0)
	for i := 0; i < 20; i++ {
		_ = a.Tick(Input{Jump: i == 0})
		apex = max(apex, a.Pose().Feet.Y())
	}
	if apex > 0.3 {
		t.Errorf("stone: apex %v, want the ceiling to cap it", apex)
	}
	_ = protocol.BlockPos{}
}
