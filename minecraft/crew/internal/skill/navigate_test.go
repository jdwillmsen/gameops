package skill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

func flatWorld(size int32) simWorld {
	w := simWorld{}
	w.platform(0, size-1, 0, size-1)
	return w
}

func feetAt(x, y, z float32) mgl32.Vec3 { return mgl32.Vec3{x + 0.5, y, z + 0.5} }

func near(t *testing.T, r *rig, goal protocol.BlockPos, within float64) {
	t.Helper()
	f := r.Pose().Feet
	dx, dy, dz := float64(f.X())-float64(goal.X())-0.5, float64(f.Y())-float64(goal.Y()), float64(f.Z())-float64(goal.Z())-0.5
	if d := dx*dx + dy*dy + dz*dz; d > within*within {
		t.Errorf("feet %v are not within %.1f of %v", f, within, goal)
	}
}

func TestNavigateArrivesOnFlatGround(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 1))
	goal := protocol.BlockPos{9, 1, 4}
	nav := NewNavigate(w, r.self, goal, 0.8)
	if err := nav.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(nav, 200)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, goal, 0.8)
}

func TestNavigateClimbsAStep(t *testing.T) {
	w := flatWorld(12)
	w.fill("dirt", 5, 1, 0, 11, 1, 11)
	w.fill("air", 5, 2, 0, 11, 7, 11)
	r := newRig(t, w, feetAt(1, 1, 3))
	goal := protocol.BlockPos{8, 2, 3}
	st, err := r.run(NewNavigate(w, r.self, goal, 0.8), 200)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, goal, 0.8)
}

func TestNavigateReplansAfterACorrection(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 1))
	r.correctAt, r.correctBy = 12, mgl32.Vec3{0, 0, 3}
	goal := protocol.BlockPos{11, 1, 2}
	nav := NewNavigate(w, r.self, goal, 0.8)
	st, err := r.run(nav, 300)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, goal, 0.8)
	if n := nav.(*Navigate).nav.replans; n < 1 {
		t.Errorf("replans = %d, want the correction to force one", n)
	}
}

func TestNavigateFailsWithNoPath(t *testing.T) {
	w := flatWorld(12)
	w.fill("stone", 8, 1, 3, 10, 3, 5)
	w.fill("air", 9, 1, 4, 9, 3, 4)
	r := newRig(t, w, feetAt(1, 1, 1))
	st, err := r.run(NewNavigate(w, r.self, protocol.BlockPos{9, 1, 4}, 0.8), 50)
	if st != Failed || !errors.Is(err, path.ErrNoPath) {
		t.Fatalf("status %v, err %v; want a failure wrapping ErrNoPath", st, err)
	}
}

func TestNavigateGivesUpWhenReplansMakeNoProgress(t *testing.T) {
	w := flatWorld(16)
	r := newRig(t, w, feetAt(1, 1, 1))
	nav := NewNavigate(w, r.self, protocol.BlockPos{13, 1, 1}, 0.8)
	var st Status
	var err error
	for i := 0; i < 400 && st == Running; i++ {
		if i%5 == 4 {
			// The server keeps putting the unit back where it started.
			r.send(&packet.CorrectPlayerMovePrediction{Position: feetAt(1, 1, 3).Add(mgl32.Vec3{0, 1.62, 0}), OnGround: true})
		}
		st, err = nav.Step(context.Background(), r)
	}
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no progress") {
		t.Fatalf("status %v, err %v; want a no-progress failure", st, err)
	}
}

func TestNavigateWalksUpSlabsAndStairsWithoutJumping(t *testing.T) {
	// Slabs lead to a stair flight rising east; neither takes a jump.
	w := flatWorld(16)
	for x := int32(4); x <= 5; x++ {
		w[protocol.BlockPos{x, 1, 3}] = "stone_slab"
	}
	for i := int32(0); i < 3; i++ {
		w[protocol.BlockPos{7 + i, 1 + i, 3}] = "oak_stairs"
		for y := int32(1); y < 1+i; y++ {
			w[protocol.BlockPos{7 + i, y, 3}] = "stone"
		}
	}
	r := newRig(t, w, feetAt(1, 1, 3))
	goal := protocol.BlockPos{9, 4, 3}
	st, err := r.run(NewNavigate(w, r.self, goal, 0.8), 300)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, goal, 0.8)
	if r.jumps != 0 {
		t.Errorf("%d jumps, want none", r.jumps)
	}
}

func TestNavigateStillJumpsFullBlocks(t *testing.T) {
	w := flatWorld(12)
	w.fill("dirt", 5, 1, 0, 11, 1, 11)
	w.fill("air", 5, 2, 0, 11, 7, 11)
	r := newRig(t, w, feetAt(1, 1, 3))
	if st, err := r.run(NewNavigate(w, r.self, protocol.BlockPos{8, 2, 3}, 0.8), 200); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.jumps == 0 {
		t.Error("a full block is not steppable, the unit must jump")
	}
}

func TestNavigateArrivesOnTopOfASlab(t *testing.T) {
	w := flatWorld(12)
	w[protocol.BlockPos{6, 1, 3}] = "stone_slab"
	r := newRig(t, w, feetAt(1, 1, 3))
	goal := protocol.BlockPos{6, 1, 3}
	st, err := r.run(NewNavigate(w, r.self, goal, 0.4), 200)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if y := r.Pose().Feet.Y(); y != 1.5 {
		t.Errorf("feet y = %v, want on top of the slab at 1.5", y)
	}
	if r.jumps != 0 {
		t.Errorf("%d jumps, want a step", r.jumps)
	}
}

// A unit that brushed a fence post or a closed gate stands in the post's cell
// with its centre off to one side; planning from the cell's centre would put
// the box inside the post and find no way out.
func TestNavigateLeavesTheSideOfAFencePostOrGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   protocol.BlockPos
		feet mgl32.Vec3
	}{
		{"oak_fence", protocol.BlockPos{3, 1, 3}, mgl32.Vec3{3.05, 1, 3.5}},
		{"fence_gate", protocol.BlockPos{3, 1, 3}, mgl32.Vec3{3.5, 1, 3.05}},
		{"fence_gate", protocol.BlockPos{3, 1, 3}, mgl32.Vec3{3.5, 1, 3.95}},
	} {
		w := flatWorld(12)
		w[tc.at] = tc.name
		r := newRig(t, w, tc.feet)
		goal := protocol.BlockPos{8, 1, 8}
		st, err := r.run(NewNavigate(w, r.self, goal, 0.8), 400)
		if st != Done || err != nil {
			t.Errorf("%s from %v: status %v, err %v", tc.name, tc.feet, st, err)
			continue
		}
		near(t, r, goal, 0.8)
	}
}

func TestNavigateReachesAGoalThatHangsOverAHole(t *testing.T) {
	w := flatWorld(12)
	w.fill("air", 6, 0, 3, 6, 0, 3)
	w.fill("stone", 6, -1, 3, 6, -1, 3)
	goal := protocol.BlockPos{6, 1, 3}
	r := newRig(t, w, feetAt(1, 1, 3))
	st, err := r.run(NewNavigate(w, r.self, goal, arriveSlack), 300)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}
