package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A one-block step up with a light block over the step: the body on the step
// reaches into the light's cell, which a phantom cube would make a ceiling.
func TestAStepUpUnderALightBlockIsPlannedAndWalked(t *testing.T) {
	for _, name := range []string{"light_block_15", "structure_void"} {
		w := flatWorld(12)
		w.fill("dirt", 5, 1, 0, 11, 1, 11)
		w.fill("air", 5, 2, 0, 11, 7, 11)
		for x := int32(5); x < 12; x += 3 {
			for z := int32(0); z < 12; z += 3 {
				w[protocol.BlockPos{x, 3, z}] = name
			}
		}
		r := newRig(t, w, feetAt(1, 1, 3))
		goal := protocol.BlockPos{8, 2, 3}
		st, err := r.run(NewNavigate(w, r.self, goal, 0.8), 300)
		if st != Done || err != nil {
			t.Errorf("%s: status %v, err %v", name, st, err)
			continue
		}
		near(t, r, goal, 0.8)
	}
}
