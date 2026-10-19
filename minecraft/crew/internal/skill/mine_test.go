package skill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// quarry is a dirt platform with 18 stone in a 3x3x2 block.
func quarry() simWorld {
	w := flatWorld(20)
	w.fill("stone", 9, 1, 9, 11, 2, 11)
	return w
}

func withPickaxe(r *rig) *rig {
	r.give(0, idWoodenPick, 1)
	return r
}

func TestMineBlockCollectsSixteenCobblestone(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 16, 0)
	if err := m.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(m, 1500)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("cobblestone"); got != 16 {
		t.Errorf("cobblestone = %d, want 16", got)
	}
	if got := w.count("stone"); got != 2 {
		t.Errorf("stone left = %d, want 2", got)
	}
	if r.held != 0 {
		t.Errorf("held slot = %d, want the pickaxe in 0", r.held)
	}
}

func TestMineBlockCountsFromInventoryAndGivesUp(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.refusePickups = true
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 16, 0)
	st, err := r.run(m, 1500)
	if st != Failed || err == nil || err.Error() != "no pickup after 3 breaks" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.breaks != 3 {
		t.Errorf("broke %d blocks, want it to stop at 3", r.breaks)
	}
	if r.aborts != 0 {
		t.Errorf("aborts = %d: every block had already broken, so there was nothing to abort", r.aborts)
	}
}

func TestMineBlockAbortsABreakThatNeverFinishes(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.stallBreaks = true
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 4, 0)
	st, err := r.run(m, 800)
	if st != Failed || err == nil || err.Error() != "block did not break" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.aborts != 1 || r.breaking != nil {
		t.Errorf("aborts = %d, session = %v; want the live break aborted", r.aborts, r.breaking)
	}
}

func TestMineBlockPropagatesAWriteErrorMidWalk(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.failWriteAt = 5
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 4, 0)
	st, err := r.run(m, 800)
	if st != Failed || !errors.Is(err, errWire) {
		t.Fatalf("status %v, err %v; want the transport error", st, err)
	}
}

func TestMineBlockStartsFromWhatIsAlreadyHeld(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.give(1, idCobblestone, 40)
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 3, 0)
	if st, err := r.run(m, 800); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("cobblestone"); got != 43 {
		t.Errorf("cobblestone = %d, want 43", got)
	}
}

func TestMineBlockFailsWithNoReachableSource(t *testing.T) {
	w := flatWorld(6)
	w.platform(30, 33, 0, 3)
	w.fill("stone", 31, 1, 1, 32, 2, 2)
	r := withPickaxe(newRig(t, w, feetAt(1, 1, 1)))
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 4, 0)
	st, err := r.run(m, 200)
	if st != Failed || err == nil || err.Error() != "no reachable stone within 24 blocks" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.breaks != 0 || r.aborts != 0 {
		t.Errorf("breaks %d aborts %d: nothing was started", r.breaks, r.aborts)
	}
}

func TestMineBlockNeverBreaksTheFloorUnderIt(t *testing.T) {
	w := flatWorld(8)
	w.fill("stone", 0, 0, 0, 7, 0, 7)
	r := withPickaxe(newRig(t, w, feetAt(3, 1, 3)))
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 2, 0)
	if st, err := r.run(m, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n, _ := w.Block(protocol.BlockPos{3, 0, 3}); n != "stone" {
		t.Errorf("block under the unit became %q", n)
	}
	if r.Pose().Feet.Y() < 1 {
		t.Errorf("the unit fell to %v", r.Pose().Feet)
	}
}

func TestMineBlockIsNotReadyWithoutAPickaxe(t *testing.T) {
	w := quarry()
	r := newRig(t, w, feetAt(2, 1, 2))
	r.give(1, idCobblestone, 4)
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 4, 0)
	err := m.Ready(r.self())
	if err == nil || err.Error() != "no pickaxe in inventory" {
		t.Fatalf("Ready = %v", err)
	}
}

func TestSkillsRefuseOtherDimensions(t *testing.T) {
	w := quarry()
	w[protocol.BlockPos{5, 1, 5}] = "chest"
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.give(1, idCobblestone, 16)
	r.tracker.Spawn(1, mgl32.Vec3{2.5, 2.62, 2.5}, 0, 0, 1)
	snap := r.self()
	if snap.Dimension != 1 {
		t.Fatal("setup: not in the nether")
	}
	for _, s := range []Skill{
		NewNavigate(w, r.self, protocol.BlockPos{5, 1, 2}, 1),
		NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 1, 0),
		NewDepositChest(w, r.self, protocol.BlockPos{5, 1, 5}, "cobblestone", 1),
	} {
		if err := s.Ready(snap); err == nil || err.Error() != "only the overworld is supported" {
			t.Errorf("%s: Ready = %v", s.Name(), err)
		}
		if st, err := s.Step(t.Context(), r); st != Failed || err == nil || !strings.Contains(err.Error(), "only the overworld") {
			t.Errorf("%s: Step = %v, %v", s.Name(), st, err)
		}
	}
}

func TestMineBlockStopAbortsALiveBreakOnce(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.stallBreaks = true
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 16, 0)
	_ = m.Ready(r.self())
	for i := 0; i < 400 && r.breaking == nil; i++ {
		if st, _ := m.Step(context.Background(), r); st != Running {
			break
		}
	}
	if r.breaking == nil {
		t.Fatal("no break started")
	}
	m.(Stopper).Stop(r)
	m.(Stopper).Stop(r)
	if r.aborts != 1 || r.breaking != nil {
		t.Errorf("aborts = %d, session = %v", r.aborts, r.breaking)
	}
}

func TestMineBlockIgnoresBlocksOutsideTheArea(t *testing.T) {
	w := quarry()
	w.fill("stone", 4, 1, 2, 4, 1, 2)
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	area := Area{Min: protocol.BlockPos{9, 1, 9}, Max: protocol.BlockPos{11, 2, 11}}
	m := NewMineBlockWith(w, r.self, "stone", "cobblestone", "pickaxe", 4, 0, InArea(area))
	if st, err := r.run(m, 800); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n, _ := w.Block(protocol.BlockPos{4, 1, 2}); n != "stone" {
		t.Errorf("the nearer block outside the area became %q", n)
	}

	empty := Area{Min: protocol.BlockPos{0, 5, 0}, Max: protocol.BlockPos{1, 6, 1}}
	r = withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	m = NewMineBlockWith(w, r.self, "stone", "cobblestone", "pickaxe", 1, 0, InArea(empty))
	st, err := r.run(m, 200)
	if st != Failed || err == nil || err.Error() != "no reachable stone in the area" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if strings.ContainsAny(err.Error(), "0123456789") {
		t.Errorf("error carries coordinates: %q", err)
	}
}

// A loaded server finishes a break and offers its drop many ticks late; the
// skill must wait for it, not fail a task that is only slow.
func TestMineBlockToleratesALoadedServer(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
	r.slowBreak, r.slowPickup = 70, 70
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 3, 0)
	if st, err := r.run(m, 3000); st != Done {
		t.Fatalf("status %v, err %v", st, err)
	}
}
