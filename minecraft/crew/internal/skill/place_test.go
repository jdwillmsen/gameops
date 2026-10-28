package skill

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
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

// A stack that only the main inventory holds has to first travel to the
// hotbar. When the server stays silent about that move the skill gives up
// with its own reason, after the wait answerTicks bounds, instead of
// spinning until the whole task times out.
func TestPlaceBlockGivesUpWhenTheMoveIsUnanswered(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(12, idCobblestone, 8)
	r.dropResponses = true
	target := protocol.BlockPos{7, 1, 7}
	st, err := r.run(NewPlaceBlock(w, r.self, target, "cobblestone"), 600)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no answer to the move") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placeClicks != 0 {
		t.Errorf("placed %d times with the stack away from the hand", r.placeClicks)
	}
	if name, _ := w[target]; name != "air" {
		t.Errorf("target holds %q", name)
	}
}

func TestPlaceBlockReadyNeedsTheItemOnlyOutsideCreative(t *testing.T) {
	w := flatWorld(12)
	p := NewPlaceBlock(w, nil, protocol.BlockPos{7, 1, 7}, "cobblestone")
	if err := p.Ready(belief.Snapshot{}); err == nil || !strings.Contains(err.Error(), "no cobblestone in inventory") {
		t.Errorf("survival, empty inventory: err = %v", err)
	}
	if err := p.Ready(belief.Snapshot{Creative: true}); err != nil {
		t.Errorf("creative, empty inventory: err = %v", err)
	}
}
