package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A placement walks to the target, puts the block there and reads the
// confirmation back from the world.
func TestPlaceBlockWalksPlacesAndConfirms(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(3, idCobblestone, 8)
	target := protocol.BlockPos{7, 1, 7}
	st, err := r.run(NewPlaceBlock(w, r.self, target, "cobblestone"), 400)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if name, _ := w[target]; name != "cobblestone" {
		t.Errorf("target holds %q", name)
	}
	if got := r.have("cobblestone"); got != 7 {
		t.Errorf("cobblestone left = %d, want 7", got)
	}
	if r.placed != 1 {
		t.Errorf("placed %d blocks, want 1", r.placed)
	}
}

// A server that silently drops the placement is retried exactly once, then
// the skill fails without touching the world.
func TestPlaceBlockRetriesOnceWhenTheServerStaysSilent(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	// The rig's server places dirt and cobblestone only, and never says why.
	r.give(3, idOakLog, 8)
	target := protocol.BlockPos{7, 1, 7}
	st, err := r.run(NewPlaceBlock(w, r.self, target, "oak_log"), 600)
	if st != Failed || err == nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placeClicks != 2 {
		t.Errorf("clicked %d times, want 2", r.placeClicks)
	}
	if name, _ := w[target]; name != "air" {
		t.Errorf("target holds %q", name)
	}
}
