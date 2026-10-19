package skill

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	idAxe = 313
	// The tree stands at (8, *, 8) on a floor at y=0, so its logs run from
	// y=1 up.
	treeX, treeZ = 8, 8
)

// oak is a trunk of n logs with a two-layer canopy of radius two around the
// top, as the oak feature grows.
func oak(n int) simWorld {
	w := flatWorld(18)
	w.fill("air", 0, 7, 0, 17, 16, 17)
	w.fill("oak_log", treeX, 1, treeZ, treeX, int32(n), treeZ)
	for y := int32(n - 1); y <= int32(n); y++ {
		for dx := int32(-2); dx <= 2; dx++ {
			for dz := int32(-2); dz <= 2; dz++ {
				if c := (protocol.BlockPos{treeX + dx, y, treeZ + dz}); w[c] == "air" && (dx != 0 || dz != 0) {
					w[c] = "oak_leaves"
				}
			}
		}
	}
	w.fill("oak_leaves", treeX-1, int32(n)+1, treeZ-1, treeX+1, int32(n)+2, treeZ+1)
	w[protocol.BlockPos{treeX, int32(n) + 1, treeZ}] = "oak_leaves"
	return w
}

func logsLeft(w simWorld) int { return w.count("oak_log") }

func fellRig(t *testing.T, w simWorld, dirt int) *rig {
	t.Helper()
	r := newRig(t, w, feetAt(3, 1, 3))
	r.give(0, idAxe, 1)
	if dirt > 0 {
		r.give(1, idDirt, dirt)
	}
	return r
}

func newFell(w simWorld, r *rig, qty int, opts ...MineOption) Skill {
	return NewFell(w, r.self, "oak_log", "oak_log", "axe", qty, 0, opts)
}

func onGround(t *testing.T, r *rig) {
	t.Helper()
	if p := r.Pose(); math.Abs(float64(p.Feet.Y())-1) > 0.01 || !p.OnGround {
		t.Errorf("unit ended at height %.2f, on ground %v; want standing on the ground", p.Feet.Y(), p.OnGround)
	}
}

func TestFellTakesAFullTreeIncludingLogsAboveReach(t *testing.T) {
	w := oak(7)
	r := fellRig(t, w, 3)
	f := newFell(w, r, 7)
	if err := f.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	if st, err := r.run(f, 4000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("oak_log"); got != 7 {
		t.Errorf("oak_log = %d, want 7", got)
	}
	if r.placed == 0 {
		t.Error("no pillar was built, yet the top log is above ground reach")
	}
	if got := w.count("dirt"); got != 18*18 {
		t.Errorf("dirt blocks = %d, want only the floor: the pillar must be taken down", got)
	}
	if got := r.have("dirt"); got != 3 {
		t.Errorf("dirt held = %d, want the pillar's blocks back", got)
	}
	if r.maxRise > 1.3 {
		t.Errorf("placed from %.2f above the target: not a jump", r.maxRise)
	}
	onGround(t, r)
}

func TestFellUsesOneBlockForAOneBlockPillar(t *testing.T) {
	w := oak(7)
	r := fellRig(t, w, 1)
	if st, err := r.run(newFell(w, r, 7), 4000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("oak_log"); got != 7 {
		t.Errorf("oak_log = %d, want 7", got)
	}
	if r.placed != 1 || r.have("dirt") != 1 {
		t.Errorf("placed %d blocks and held %d dirt; want one pillar block returned", r.placed, r.have("dirt"))
	}
}

func TestFellTakesOnlyWhatItNeeds(t *testing.T) {
	w := oak(5)
	r := fellRig(t, w, 3)
	if st, err := r.run(newFell(w, r, 2), 2000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placed != 0 {
		t.Errorf("built %d pillar blocks for logs within reach", r.placed)
	}
	if got := r.have("oak_log"); got != 2 {
		t.Errorf("oak_log = %d, want 2", got)
	}
}

func TestFellClearsLeavesInTheWayOfThePillarOnly(t *testing.T) {
	w := oak(7)
	// Leaves hang over every column beside the trunk, where the unit's
	// head and jump need room.
	w.fill("oak_leaves", treeX-1, 3, treeZ-1, treeX+1, 3, treeZ+1)
	w[protocol.BlockPos{treeX, 3, treeZ}] = "oak_log"
	before := w.count("oak_leaves")
	r := fellRig(t, w, 3)
	if st, err := r.run(newFell(w, r, 7), 4000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	cleared := before - w.count("oak_leaves")
	if cleared < 1 || cleared > 3 {
		t.Errorf("cleared %d leaves, want the few in one column and not the canopy", cleared)
	}
	onGround(t, r)
}

func TestFellSkipsLeafThatDisappearsAfterPlanning(t *testing.T) {
	w := oak(7)
	w.fill("oak_leaves", treeX-1, 3, treeZ-1, treeX+1, 3, treeZ+1)
	w[protocol.BlockPos{treeX, 3, treeZ}] = "oak_log"
	r := fellRig(t, w, 3)
	f := NewFell(w, r.self, "oak_log", "oak_log", "axe", 7, 0, nil).(*Fell)
	for i := 0; i < 1000 && (f.phase != fWalk || len(f.leaves) == 0); i++ {
		if st, err := f.Step(t.Context(), r); st == Failed || err != nil {
			t.Fatalf("planning stopped with status %v, err %v", st, err)
		}
	}
	if f.phase != fWalk || len(f.leaves) == 0 || f.brk.name != "" {
		t.Fatal("fell skill did not stop after planning leaves and before clearing")
	}
	w[f.leaves[0]] = "air"
	if st, err := r.run(f, 4000); st != Done || err != nil {
		t.Fatalf("status %v, err %v after planned leaf disappeared", st, err)
	}
	if got := r.have("oak_log"); got != 7 {
		t.Errorf("oak_log = %d, want 7", got)
	}
}

func TestFellWithNothingToBuildWithTakesWhatItReaches(t *testing.T) {
	w := oak(8)
	r := fellRig(t, w, 0)
	st, err := r.run(newFell(w, r, 8), 3000)
	if st != Failed || err == nil || err.Error() != "no reachable oak_log within 24 blocks" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placed != 0 {
		t.Error("placed a block it did not hold")
	}
	if got := r.have("oak_log"); got < 6 {
		t.Errorf("oak_log = %d, want every log in ground reach", got)
	}
	onGround(t, r)
}

func TestFellLeavesLogsBeyondAPillarAndStaysSafe(t *testing.T) {
	w := oak(10)
	r := fellRig(t, w, 4)
	st, err := r.run(newFell(w, r, 10), 5000)
	if st != Failed || err == nil {
		t.Fatalf("status %v, err %v; the top log is out of reach even from a pillar", st, err)
	}
	if got := r.have("oak_log"); got != 8 {
		t.Errorf("oak_log = %d, want 8", got)
	}
	if r.maxRise > 1.3 {
		t.Errorf("rise %.2f", r.maxRise)
	}
	if got := w.count("dirt"); got != 18*18 {
		t.Errorf("dirt blocks = %d: pillar left behind", got)
	}
	onGround(t, r)
}

func TestFellStaysInsideTheArea(t *testing.T) {
	w := oak(7)
	// A second tree outside the area, nearer the unit.
	w.fill("oak_log", 4, 1, 4, 4, 5, 4)
	r := fellRig(t, w, 3)
	area := Area{Min: protocol.BlockPos{6, 1, 6}, Max: protocol.BlockPos{10, 12, 10}}
	if st, err := r.run(newFell(w, r, 7, InArea(area)), 4000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w.count("oak_log"); n != 5 {
		t.Errorf("logs left = %d, want the 5 of the tree outside the area", n)
	}
}

func TestFellResumesAfterAFightAtAnyPoint(t *testing.T) {
	for n := 1; n < 700; n += 11 {
		w := oak(7)
		r := fellRig(t, w, 3)
		f := newFell(w, r, 7)
		if interrupt(t, r, f, n) {
			continue
		}
		st, err := r.run(f, 5000)
		if st != Done || err != nil {
			t.Fatalf("interrupted at %d: status %v, err %v", n, st, err)
		}
		if got := r.have("oak_log"); got != 7 {
			t.Errorf("interrupted at %d: oak_log = %d, want 7", n, got)
		}
		onGround(t, r)
		if got := w.count("dirt"); got != 18*18 {
			t.Errorf("interrupted at %d: %d dirt blocks, pillar left behind", n, got-18*18)
		}
	}
}

func TestFellStopAbortsOnceAndSendsOneInput(t *testing.T) {
	w := oak(7)
	r := fellRig(t, w, 3)
	f := newFell(w, r, 7)
	r.stallBreaks = true
	for i := 0; i < 400 && r.breaking == nil; i++ {
		f.Step(t.Context(), r)
	}
	if r.breaking == nil {
		t.Fatal("no break started")
	}
	before := r.ticks
	f.(Stopper).Stop(r)
	f.(Stopper).Stop(r)
	if r.aborts != 1 || r.ticks-before != 1 {
		t.Errorf("aborts %d, inputs %d", r.aborts, r.ticks-before)
	}
}

func TestFellRefusesTheNetherAndNeedsAnAxe(t *testing.T) {
	w := oak(5)
	r := newRig(t, w, feetAt(3, 1, 3))
	if err := newFell(w, r, 1).Ready(r.self()); err == nil || err.Error() != "no axe in inventory" {
		t.Errorf("Ready = %v", err)
	}
	r.give(0, idAxe, 1)
	r.tracker.Spawn(1, mgl32.Vec3{3.5, 2.62, 3.5}, 0, 0, 1)
	if st, err := newFell(w, r, 1).Step(t.Context(), r); st != Failed || err == nil {
		t.Errorf("Step = %v, %v", st, err)
	}
}

func TestFellBuildsWithCobblestoneWhenItHasAPickaxe(t *testing.T) {
	w := oak(7)
	r := newRig(t, w, feetAt(3, 1, 3))
	r.give(0, idAxe, 1)
	r.give(1, idWoodenPick, 1)
	r.give(2, idCobblestone, 3)
	if st, err := r.run(newFell(w, r, 7), 4000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placed == 0 || w.count("cobblestone") != 0 || r.have("cobblestone") != 3 {
		t.Errorf("placed %d, cobblestone in world %d, held %d: the pillar must go back", r.placed, w.count("cobblestone"), r.have("cobblestone"))
	}
	onGround(t, r)
}

func TestFellDoesNotBuildWithCobblestoneItCannotBreak(t *testing.T) {
	w := oak(7)
	r := newRig(t, w, feetAt(3, 1, 3))
	r.give(0, idAxe, 1)
	r.give(2, idCobblestone, 3)
	if st, _ := r.run(newFell(w, r, 7), 3000); st != Failed || r.placed != 0 {
		t.Errorf("status %v, placed %d", st, r.placed)
	}
}
