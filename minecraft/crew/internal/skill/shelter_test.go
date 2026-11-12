package skill

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// atNight is the unit's belief with a clock that turns to dawn once the unit
// has sent dawnAt inputs; a negative dawnAt keeps it night for good.
func atNight(r *rig, dawnAt int) func() belief.Snapshot {
	return func() belief.Snapshot {
		s := r.self()
		s.Env.DayPhase = belief.PhaseNight
		if dawnAt >= 0 && r.ticks >= dawnAt {
			s.Env.DayPhase = belief.PhaseDawn
		}
		return s
	}
}

func shelterRig(t *testing.T, w simWorld, dirt int) *rig {
	t.Helper()
	r := newRig(t, w, feetAt(20, 1, 20))
	if dirt > 0 {
		r.give(0, idDirt, dirt)
	}
	return r
}

func ringCells(c protocol.BlockPos) []protocol.BlockPos {
	var out []protocol.BlockPos
	for dy := int32(0); dy <= 1; dy++ {
		for _, d := range sideOffsets {
			out = append(out, off(c, d[0], dy, d[1]))
		}
	}
	return out
}

func TestShelterEnclosesWaitsForDawnAndDigsOut(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 32)
	start := protocol.BlockPos{20, 1, 20}
	sk := NewShelter(w, atNight(r, 400), 0)
	if err := sk.Ready(atNight(r, 400)()); err != nil {
		t.Fatal(err)
	}
	// Stop at the first step that waits, to see the sealed room.
	for sk.(*Shelter).phase != shlWait {
		if st, err := sk.Step(context.Background(), r); st != Running || err != nil {
			t.Fatalf("status %v, err %v while building", st, err)
		}
		if r.ticks > 300 {
			t.Fatal("still building after 300 inputs")
		}
	}
	for _, c := range append(ringCells(start), off(start, 0, 2, 0)) {
		if n := w[c]; n != "dirt" {
			t.Errorf("cell %v holds %q while sealed", c, n)
		}
	}
	if r.placed != 10 {
		t.Errorf("placed %d blocks, want 10: 8 walls, a bridge and the roof", r.placed)
	}
	if got := r.have("dirt"); got != 22 {
		t.Errorf("dirt left = %d, want 22", got)
	}

	st, err := r.run(sk, 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.ticks < 400 {
		t.Errorf("left after %d inputs, before dawn at 400", r.ticks)
	}
	open := 0
	for _, c := range ringCells(start) {
		if w[c] == "air" {
			open++
		}
	}
	if open != 2 {
		t.Errorf("%d ring cells open, want the head and feet cell of one side", open)
	}
	if w[off(start, 0, 2, 0)] != "dirt" {
		t.Error("the roof was taken down")
	}
}

func TestShelterNeedsEnoughBlocks(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 5)
	st, err := r.run(NewShelter(w, atNight(r, -1), 0), 200)
	if st != Failed || err == nil || err.Error() != "not enough blocks to shelter (need 10)" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placed != 0 {
		t.Errorf("placed %d blocks of an enclosure it cannot finish", r.placed)
	}
}

func TestShelterWithNoBlocksAtAllFails(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 0)
	st, err := r.run(NewShelter(w, atNight(r, -1), 0), 200)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "not enough blocks to shelter") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestShelterAvoidsASpotBesideWater(t *testing.T) {
	w := flatWorld(40)
	w[protocol.BlockPos{21, 1, 20}] = "water"
	r := shelterRig(t, w, 32)
	start := protocol.BlockPos{20, 1, 20}
	st, err := r.run(NewShelter(w, atNight(r, 60), 0), 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[protocol.BlockPos{21, 1, 20}] != "water" {
		t.Error("the water cell was built over")
	}
	if cellOf(r.Pose().Feet) == start {
		t.Error("the unit sealed itself in the spot beside the water")
	}
	if r.placed == 0 {
		t.Fatal("nothing was built at the other spot")
	}
}

func TestShelterAvoidsASideWithNoFloor(t *testing.T) {
	w := flatWorld(40)
	// A side cell with no floor to place on: the block could not be clicked.
	w[protocol.BlockPos{21, 0, 20}] = "air"
	r := shelterRig(t, w, 32)
	st, err := r.run(NewShelter(w, atNight(r, 60), 0), 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if cellOf(r.Pose().Feet) == (protocol.BlockPos{20, 1, 20}) {
		t.Error("the unit sealed itself in a cell whose side has no floor")
	}
}

func TestShelterUsesAHillsideForFewerBlocks(t *testing.T) {
	w := flatWorld(40)
	w.fill("stone", 17, 1, 17, 23, 4, 23)
	// The tunnel runs out to open ground, so leaving it at dawn has somewhere to go.
	w.fill("air", 20, 1, 20, 23, 2, 20)
	r := shelterRig(t, w, 2)
	st, err := r.run(NewShelter(w, atNight(r, 60), 0), 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placed != 2 {
		t.Errorf("placed %d blocks, want only the two that close the tunnel", r.placed)
	}
}

func TestShelterExitsAtTheTickCapWhenNightNeverEnds(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 32)
	st, err := r.run(NewShelter(w, atNight(r, -1), 80), 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestShelterFailsWhenAWallIsBroken(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 32)
	start := protocol.BlockPos{20, 1, 20}
	sk := NewShelter(w, atNight(r, -1), 0)
	for sk.(*Shelter).phase != shlWait {
		if st, err := sk.Step(context.Background(), r); st != Running || err != nil {
			t.Fatalf("status %v, err %v while building", st, err)
		}
	}
	for i := 0; i < 5; i++ {
		if st, err := sk.Step(context.Background(), r); st != Running || err != nil {
			t.Fatalf("status %v, err %v while waiting", st, err)
		}
	}
	w[off(start, 0, 1, -1)] = "air"
	st, err := r.run(sk, 20)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "breached") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestShelterReadyRefusesDaytime(t *testing.T) {
	sk := NewShelter(flatWorld(4), nil, 0)
	var s belief.Snapshot
	s.Env.DayPhase = belief.PhaseDay
	if err := sk.Ready(s); err == nil {
		t.Error("ready by day")
	}
	s.Env.DayPhase = belief.PhaseDusk
	if err := sk.Ready(s); err != nil {
		t.Errorf("not ready at dusk: %v", err)
	}
	s.Dimension = 1
	if err := sk.Ready(s); err == nil {
		t.Error("ready outside the overworld")
	}
}

func TestShelterFailsAfterOneRetryWhenTheBlockNeverAppears(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 32)
	clicks := 0
	// A mob in the cell: the server takes the click and shows nothing.
	r.onWrite = func(pk packet.Packet) bool {
		if _, ok := pk.(*packet.InventoryTransaction); ok {
			clicks++
			return true
		}
		return false
	}
	st, err := r.run(NewShelter(w, atNight(r, -1), 0), 1000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "did not confirm") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if clicks != 2 {
		t.Errorf("clicked %d times, want the placement and one retry", clicks)
	}
}

func TestShelterLeavesThroughAPlacedWallNotNaturalRock(t *testing.T) {
	w := flatWorld(40)
	w.fill("stone", 21, 1, 20, 22, 3, 20)
	r := shelterRig(t, w, 32)
	st, err := r.run(NewShelter(w, atNight(r, 60), 0), 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[protocol.BlockPos{21, 1, 20}] != "stone" || w[protocol.BlockPos{21, 2, 20}] != "stone" {
		t.Error("the natural wall was dug")
	}
	open := 0
	for _, c := range ringCells(protocol.BlockPos{20, 1, 20}) {
		if w[c] == "air" {
			open++
		}
	}
	if open != 2 {
		t.Errorf("%d ring cells open, want one placed column", open)
	}
}

func TestShelterFailsRatherThanLeaveBySealedRock(t *testing.T) {
	w := flatWorld(40)
	w.fill("stone", 18, 1, 18, 22, 4, 22)
	w.fill("air", 19, 1, 19, 21, 2, 21)
	r := shelterRig(t, w, 32)
	st, err := r.run(NewShelter(w, atNight(r, 60), 0), 2000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no safe exit") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func sealedShelter(t *testing.T, w simWorld, r *rig, dawnAt int) Skill {
	t.Helper()
	sk := NewShelter(w, atNight(r, dawnAt), 0)
	for sk.(*Shelter).phase != shlWait {
		if st, err := sk.Step(context.Background(), r); st != Running || err != nil {
			t.Fatalf("status %v, err %v while building", st, err)
		}
	}
	return sk
}

func TestShelterStopOpensTheExitSoACancelledTaskDoesNotLeaveTheUnitWalledIn(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 32)
	sk := sealedShelter(t, w, r, -1)
	before := r.ticks
	sk.(Stopper).Stop(r)
	if sent := r.ticks - before; sent > 1 {
		t.Fatalf("Stop sent %d inputs, want at most 1", sent)
	}
	for i := 0; i < 40; i++ {
		_ = r.Tick(act.Input{})
	}
	open := 0
	for _, c := range ringCells(protocol.BlockPos{20, 1, 20}) {
		if w[c] == "air" {
			open++
		}
	}
	if open == 0 {
		t.Error("every wall is still up after the task was stopped")
	}
}

func TestShelterResumeClosesTheCellStopOpened(t *testing.T) {
	w := flatWorld(40)
	r := shelterRig(t, w, 32)
	sk := sealedShelter(t, w, r, 600)
	sk.(Stopper).Stop(r)
	for i := 0; i < 40; i++ {
		_ = r.Tick(act.Input{})
	}
	sk.(Resumer).Resume()
	placedBefore := r.placed
	for sk.(*Shelter).phase != shlWait {
		if st, err := sk.Step(context.Background(), r); st != Running || err != nil {
			t.Fatalf("status %v, err %v while resealing", st, err)
		}
	}
	if r.placed != placedBefore+1 {
		t.Errorf("placed %d blocks to reseal, want 1", r.placed-placedBefore)
	}
	if st, err := r.run(sk, 2000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestFullCubeRejectsShapesThatLeaveGaps(t *testing.T) {
	for _, n := range []string{"flower_pot", "oak_sign", "white_banner", "skeleton_skull", "lantern", "chain", "end_rod", "lightning_rod",
		"white_candle", "cake", "anvil", "chipped_anvil", "oak_slab", "oak_stairs", "oak_fence", "cobblestone_wall", "glass_pane",
		"oak_door", "oak_trapdoor", "stone_button", "stone_pressure_plate", "white_carpet", "red_bed", "iron_bars", "enchanting_table", "water", "air"} {
		if fullCube(n) {
			t.Errorf("%s counts as a full cube", n)
		}
	}
	for _, n := range []string{"stone", "dirt", "cobblestone", "oak_planks", "deepslate", "sea_lantern", "glass"} {
		if !fullCube(n) {
			t.Errorf("%s does not count as a full cube", n)
		}
	}
}

// The server refuses a block placed where the unit's own body is, and the body
// is 0.6 wide, so a unit standing 0.2 or more off the middle of its cell
// cannot wall the neighbour on that side. The shelter has to centre first.
func TestShelterCentresTheUnitBeforeItBuilds(t *testing.T) {
	for _, off := range []float32{0.43, -0.4} {
		w := flatWorld(40)
		r := newRig(t, w, feetAt(20, 1, 20))
		r.give(0, idDirt, 32)
		r.Actuator.Apply(&packet.MovePlayer{EntityRuntimeID: 1, Position: feetAt(20, 1, 20).Add(mgl32.Vec3{off, 1.62, -off}), OnGround: true})
		worst := 0.0
		sk := NewShelter(w, atNight(r, -1), 0)
		for sk.(*Shelter).phase != shlWait {
			clicks, f := r.placeClicks, r.Pose().Feet
			if st, err := sk.Step(context.Background(), r); st != Running || err != nil {
				t.Fatalf("offset %v: status %v, err %v while building", off, st, err)
			}
			if r.placeClicks > clicks {
				worst = math.Max(worst, math.Max(math.Abs(float64(f.X())-20.5), math.Abs(float64(f.Z())-20.5)))
			}
			if r.ticks > 400 {
				t.Fatalf("offset %v: still building after 400 inputs", off)
			}
		}
		if r.placed == 0 || worst > 0.2 {
			t.Errorf("offset %v: placed %d blocks, the unit stood %.2f off the middle for a placement (limit 0.2)", off, r.placed, worst)
		}
	}
}
