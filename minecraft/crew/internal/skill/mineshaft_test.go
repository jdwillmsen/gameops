package skill

import (
	"context"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

var shaftEntrance = protocol.BlockPos{10, 1, 10}

// mineWorld is solid stone under a flat surface at y=0, with a chest on it.
func mineWorld() simWorld {
	w := simWorld{}
	w.fill("stone", 0, -20, 0, 30, 0, 30)
	w.fill("air", 0, 1, 0, 30, 6, 30)
	w[chestPos] = "chest"
	return w
}

func shaftSpec(depth, branches, length int) ShaftSpec {
	return ShaftSpec{
		Entrance: shaftEntrance, Dir: [2]int32{1, 0},
		Depth: depth, Branches: branches, BranchLength: length,
		Area:  Area{Min: protocol.BlockPos{5, -15, 2}, Max: protocol.BlockPos{25, 6, 20}},
		Chest: chestPos,
	}
}

func shaftRig(t *testing.T, w simWorld) *rig {
	return withPickaxe(newRig(t, w, feetAt(3, 1, 3)))
}

func newShaft(t *testing.T, w simWorld, r *rig, spec ShaftSpec, log *capture) Skill {
	t.Helper()
	opts := []Option{}
	if log != nil {
		opts = append(opts, WithLogger(log))
	}
	s, err := NewMineShaft(w, r.self, spec, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func chestCount(r *rig, item int32) int {
	n := 0
	for _, s := range r.chest {
		if s.item == item {
			n += s.count
		}
	}
	return n
}

// hauled is every cobblestone the shaft brought up, in the chest and in the
// reserve the unit keeps.
func hauled(r *rig) int { return chestCount(r, idCobblestone) + r.have("cobblestone") }

func hazards(log *capture) []string {
	var kinds []string
	for _, l := range log.lines {
		if l.event == "mine_hazard" {
			kinds = append(kinds, l.fields["kind"].(string))
		}
	}
	return kinds
}

// nextToLiquid fails the test if any cell the unit emptied touches a liquid.
func nextToLiquid(t *testing.T, r *rig) {
	t.Helper()
	for _, c := range r.cleared {
		for _, d := range [][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			n := r.world[protocol.BlockPos{c.X() + d[0], c.Y() + d[1], c.Z() + d[2]}]
			if n == "lava" || n == "water" {
				t.Errorf("opened a cell beside %s", n)
			}
		}
	}
}

func TestShaftDescendsMinesBranchesAndDeposits(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	log := &capture{}
	s := newShaft(t, w, r, shaftSpec(4, 2, 3), log)
	if err := s.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(s, 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	// The first steps start in the air above the surface: 1, 2, 3 and 3
	// stone cells, then 2 branches of 3 two-high cells.
	if got := hauled(r); got != 9+12 {
		t.Errorf("chest cobblestone = %d, want 21", got)
	}
	if got := r.have("cobblestone"); got != bridgeReserve {
		t.Errorf("unit carries %d cobblestone, want the reserve of %d", got, bridgeReserve)
	}
	if r.have("wooden_pickaxe") != 1 {
		t.Error("the pickaxe was deposited")
	}
	// Back on the surface, near the chest, walkable both ways.
	if f := r.Pose().Feet; f.Y() < 0.9 {
		t.Errorf("unit ended at depth %v", f)
	}
	for i := int32(1); i <= 4; i++ {
		if n := w[protocol.BlockPos{10 + i, 1 - i, 10}]; n != "air" {
			t.Errorf("step %d floor cell is %q", i, n)
		}
	}
	if len(hazards(log)) != 0 {
		t.Errorf("hazards = %v", hazards(log))
	}
	for _, l := range log.lines {
		for k := range l.fields {
			if k == "x" || k == "y" || k == "z" || k == "pos" {
				t.Errorf("event %s logs a coordinate field %q", l.event, k)
			}
		}
	}
}

func TestShaftWithNoBranchesStillDelivers(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, shaftSpec(3, 0, 0), nil), 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := hauled(r); got != 6 {
		t.Errorf("chest cobblestone = %d, want 6", got)
	}
}

func TestShaftStopsBesideLavaAndMinesBranchesFromThere(t *testing.T) {
	w := mineWorld()
	// Beside the third step's floor cell.
	w[protocol.BlockPos{13, -2, 11}] = "lava"
	r := shaftRig(t, w)
	log := &capture{}
	st, err := r.run(newShaft(t, w, r, shaftSpec(5, 1, 3), log), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := hazards(log); len(got) != 1 || got[0] != "lava" {
		t.Errorf("hazards = %v, want one lava", got)
	}
	nextToLiquid(t, r)
	if n := w[protocol.BlockPos{13, -2, 10}]; n != "stone" {
		t.Errorf("the cell beside the lava was opened: %q", n)
	}
	// Two steps (3 stone cells) and one branch of three two-high cells.
	if got := hauled(r); got != 3+6 {
		t.Errorf("chest cobblestone = %d, want 9", got)
	}
	if r.Pose().Feet.Y() < 0.9 {
		t.Errorf("unit left at depth %v", r.Pose().Feet)
	}
}

func TestShaftFailsWhenLavaIsBesideTheFirstStep(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{11, 0, 9}] = "flowing_lava"
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 2, 3), nil), 3000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "lava") {
		t.Fatalf("status %v, err %v; want a failure naming lava", st, err)
	}
	nextToLiquid(t, r)
	if len(r.cleared) != 0 {
		t.Errorf("opened %d cells beside lava", len(r.cleared))
	}
}

func TestShaftLeavesLavaAboveTheRoofClosed(t *testing.T) {
	w := mineWorld()
	// Above the third step's top cell: breaking it would let the lava in.
	w[protocol.BlockPos{13, 1, 10}] = "lava"
	r := shaftRig(t, w)
	log := &capture{}
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 0, 0), log), 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	nextToLiquid(t, r)
	if got := hazards(log); len(got) != 1 || got[0] != "lava" {
		t.Errorf("hazards = %v", got)
	}
}

func TestShaftSkipsABranchBesideWater(t *testing.T) {
	w := mineWorld()
	// The bottom is (14,-3,10) for depth 4. Branches go forward, then left
	// (north, along z=9), then right; the water sits where the left branch's
	// third cell would be, so its second is already beside it.
	w[protocol.BlockPos{14, -3, 7}] = "water"
	r := shaftRig(t, w)
	log := &capture{}
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 3, 5), log), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	nextToLiquid(t, r)
	if got := hazards(log); len(got) != 1 || got[0] != "water" {
		t.Errorf("hazards = %v, want one water", got)
	}
	// 9 stair cells; the forward branch 5 two-high cells; the left one only
	// its first; the right one 5.
	if got := hauled(r); got != 9+10+2+10 {
		t.Errorf("chest cobblestone = %d, want 31", got)
	}
}

func TestShaftBridgesAVoidFirstStep(t *testing.T) {
	w := mineWorld()
	floor := protocol.BlockPos{11, -1, 10}
	w[floor] = "air"
	r := shaftRig(t, w)
	r.give(3, idCobblestone, 4)
	log := &capture{}
	st, err := r.run(newShaft(t, w, r, shaftSpec(3, 0, 0), log), 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[floor] != "cobblestone" {
		t.Errorf("the first step's floor is %q, want it bridged", w[floor])
	}
	if got := hazards(log); len(got) != 0 {
		t.Errorf("hazards = %v, want none", got)
	}
}

func TestShaftBridgesAVoidMidStairStep(t *testing.T) {
	w := mineWorld()
	floor := protocol.BlockPos{12, -2, 10}
	w[floor] = "air"
	r := shaftRig(t, w)
	r.give(3, idCobblestone, 4)
	log := &capture{}
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 0, 0), log), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[floor] != "cobblestone" {
		t.Errorf("the step's floor is %q, want it bridged", w[floor])
	}
	if got := hazards(log); len(got) != 0 {
		t.Errorf("hazards = %v, want none", got)
	}
	if r.Pose().Feet.Y() < 0.9 {
		t.Error("unit ended at depth")
	}
}

func TestShaftWithAVoidFloorAndNoBlocksSaysItIsOutOfBlocks(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{11, -1, 10}] = "air"
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, shaftSpec(3, 0, 0), nil), 4000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "out of blocks") {
		t.Fatalf("status %v, err %v, want a failure naming the lack of blocks", st, err)
	}
}

func TestShaftNeverBridgesWithATool(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{11, -1, 10}] = "air"
	r := shaftRig(t, w)
	r.give(3, 343, 1)
	st, err := r.run(newShaft(t, w, r, shaftSpec(3, 0, 0), nil), 4000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "out of blocks") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("wooden_pickaxe") != 1 {
		t.Error("the pickaxe was spent")
	}
}

func TestShaftBridgesAStairFloorThatWentMissingBeforeTheClimbOut(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	s := newShaft(t, w, r, shaftSpec(3, 0, 0), nil)
	ms := s.(*MineShaft)
	for i := 0; ms.stepsDone < 2; i++ {
		if i > 6000 {
			t.Fatal("never got down two steps")
		}
		if st, err := s.Step(context.Background(), r); st != Running {
			t.Fatalf("status %v, err %v on the way down", st, err)
		}
	}
	// A pit two deep under the first step: the unit cannot climb out of it,
	// so the stair is cut off until the floor is back.
	floor := protocol.BlockPos{11, -1, 10}
	w[floor] = "air"
	w[protocol.BlockPos{11, -2, 10}] = "air"
	st, err := r.run(s, 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[floor] != "cobblestone" {
		t.Errorf("the floor is %q, want it bridged", w[floor])
	}
	if r.Pose().Feet.Y() < 0.9 {
		t.Error("unit ended at depth")
	}
}

func TestShaftKeepsMiningAGravelColumnUntilItIsStable(t *testing.T) {
	w := mineWorld()
	// Three gravel blocks hang over the third step's top cell.
	w.fill("gravel", 13, 1, 10, 13, 3, 10)
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 0, 0), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("gravel"); got != 0 {
		t.Errorf("%d gravel left in the world", got)
	}
	for y := int32(-2); y <= 0; y++ {
		if n := w[protocol.BlockPos{13, y, 10}]; n != "air" {
			t.Errorf("column cell %d is %q", y, n)
		}
	}
	if got := chestCount(r, idGravel); got != 3 {
		t.Errorf("chest gravel = %d, want 3", got)
	}
}

func TestShaftWaitsOutGravelThatIsSlowToFall(t *testing.T) {
	w := mineWorld()
	w.fill("gravel", 13, 1, 10, 13, 3, 10)
	r := shaftRig(t, w)
	r.fallDelay = 40
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 0, 0), nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	for y := int32(-2); y <= 0; y++ {
		if n := w[protocol.BlockPos{13, y, 10}]; n != "air" {
			t.Errorf("column cell %d is %q: gravel fell in behind the unit", y, n)
		}
	}
}

func TestShaftWaitsForGravelInTheAirToLand(t *testing.T) {
	w := mineWorld()
	w.fill("gravel", 13, 1, 10, 13, 3, 10)
	r := shaftRig(t, w)
	r.fallDelay = 40
	r.fallFlight = 6
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 0, 0), nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("gravel"); got != 0 {
		t.Errorf("%d gravel left in the world", got)
	}
	if got := chestCount(r, idGravel); got != 3 {
		t.Errorf("chest gravel = %d, want 3", got)
	}
}

func TestShaftStopsWhenGravelHidesWater(t *testing.T) {
	w := mineWorld()
	w.fill("gravel", 13, 1, 10, 13, 2, 10)
	w[protocol.BlockPos{13, 3, 10}] = "water"
	r := shaftRig(t, w)
	log := &capture{}
	st, _ := r.run(newShaft(t, w, r, shaftSpec(4, 0, 0), log), 6000)
	if st != Done {
		t.Fatalf("status %v", st)
	}
	if got := hazards(log); len(got) != 1 || got[0] != "water" {
		t.Errorf("hazards = %v", got)
	}
	if n := w[protocol.BlockPos{13, 0, 10}]; n != "stone" {
		t.Errorf("opened the cell under the gravel: %q", n)
	}
}

func TestShaftNeverOpensACellOutsideTheArea(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	spec := shaftSpec(4, 2, 3)
	spec.Area.Max = protocol.BlockPos{13, 6, 20}
	if _, err := NewMineShaft(w, r.self, spec); err == nil || !strings.Contains(err.Error(), "area") {
		t.Fatalf("err = %v, want the plan rejected as outside the area", err)
	}
	spec = shaftSpec(4, 2, 3)
	spec.Area.Min = protocol.BlockPos{5, -2, 2}
	if _, err := NewMineShaft(w, r.self, spec); err == nil {
		t.Error("a shaft deeper than the area accepted")
	}
}

func TestShaftWaitsForAPickaxeThatIsStillBeingIssued(t *testing.T) {
	w := mineWorld()
	r := newRig(t, w, feetAt(3, 1, 3))
	s := newShaft(t, w, r, shaftSpec(2, 0, 0), nil)
	if err := s.Ready(r.self()); err != nil {
		t.Fatalf("Ready = %v, want a unit to be allowed to wait for its kit", err)
	}
	for i := 0; i < 30; i++ {
		if st, err := s.Step(context.Background(), r); st != Running {
			t.Fatalf("status %v, err %v while the pickaxe is missing", st, err)
		}
	}
	withPickaxe(r)
	if st, err := r.run(s, 6000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestShaftFailsWhenThePickaxeNeverComes(t *testing.T) {
	w := mineWorld()
	r := newRig(t, w, feetAt(3, 1, 3))
	st, err := r.run(newShaft(t, w, r, shaftSpec(2, 0, 0), nil), armWait+50)
	if st != Failed || err == nil || err.Error() != "no pickaxe in inventory" {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestShaftWaitsForTheEntranceToLoad(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	known := false
	gate := gated{simWorld: w, known: &known}
	s, err := NewMineShaft(gate, r.self, shaftSpec(2, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if st, err := s.Step(context.Background(), r); st != Running {
			t.Fatalf("status %v, err %v before the entrance loaded", st, err)
		}
	}
	known = true
	if st, err := r.run(s, 6000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

// gated hides the entrance column, as a chunk that has not arrived yet.
type gated struct {
	simWorld
	known *bool
}

func (g gated) Block(p protocol.BlockPos) (string, bool) {
	if !*g.known && p.X() == shaftEntrance.X() && p.Z() == shaftEntrance.Z() {
		return "", false
	}
	return g.simWorld.Block(p)
}

// interruptInPlace is a fight that leaves the unit where it was: in a
// one-block-wide shaft there is nowhere to be knocked to.
func interruptInPlace(t *testing.T, r *rig, s Skill, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if st, err := s.Step(context.Background(), r); st == Failed {
			t.Fatalf("failed before the interruption: %v", err)
		} else if st == Done {
			return
		}
	}
	s.(Stopper).Stop(r)
	for i := 0; i < 3; i++ {
		if err := r.Tick(actIdle(r)); err != nil {
			t.Fatal(err)
		}
	}
	s.(Resumer).Resume()
}

func TestShaftResumesAfterAFightAtAnyPoint(t *testing.T) {
	for _, n := range []int{5, 40, 90, 150, 220, 300, 380, 460, 540, 620, 700, 800} {
		w := mineWorld()
		r := shaftRig(t, w)
		s := newShaft(t, w, r, shaftSpec(3, 2, 2), nil)
		interruptInPlace(t, r, s, n)
		st, err := r.run(s, 12000)
		if st != Done || err != nil {
			t.Fatalf("interrupted at %d: status %v, err %v", n, st, err)
		}
		if got := hauled(r); got != 6+8 {
			t.Errorf("interrupted at %d: chest cobblestone = %d, want 14", n, got)
		}
		if r.Pose().Feet.Y() < 0.9 {
			t.Errorf("interrupted at %d: unit ended at depth", n)
		}
	}
}

func TestShaftDoesNotDigUnderItself(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	if st, err := r.run(newShaft(t, w, r, shaftSpec(4, 1, 3), nil), 8000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	// The unit's own column is never what it breaks: each cell cleared is
	// beside the cell the unit stood on, never below it. The floor under the
	// entrance must be there.
	if n := w[protocol.BlockPos{10, 0, 10}]; n != "stone" {
		t.Errorf("the floor under the entrance became %q", n)
	}
}

func TestShaftKeepsABridgeReserveOutOfTheDeposit(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, shaftSpec(4, 2, 3), nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	// 21 cobblestone were mined; 16 stay with the unit for bridging.
	if got := r.have("cobblestone"); got != bridgeReserve {
		t.Errorf("unit keeps %d cobblestone, want %d", got, bridgeReserve)
	}
	if got := chestCount(r, idCobblestone); got != 21-bridgeReserve {
		t.Errorf("chest cobblestone = %d, want %d", got, 21-bridgeReserve)
	}
}

func TestShaftRestocksBridgeBlocksFromTheChestBeforeStarting(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{11, -1, 10}] = "air"
	r := shaftRig(t, w)
	r.chest[0] = slotStack{idCobblestone, 40, 900}
	st, err := r.run(newShaft(t, w, r, shaftSpec(3, 0, 0), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[protocol.BlockPos{11, -1, 10}] != "cobblestone" {
		t.Error("the void first step was not bridged")
	}
}

func TestShaftWithAnEmptyChestStillRunsWhenNothingNeedsBridging(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, shaftSpec(3, 0, 0), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

// pitWorld is the mine world with a one-wide pit three deep at (5, -2..0, 5),
// too deep to walk or jump out of.
func pitWorld() simWorld {
	w := mineWorld()
	w.fill("air", 5, -2, 5, 5, 0, 5)
	return w
}

func TestShaftTrappedInAPitPillarsUpWithItsBlocksAndGoesOn(t *testing.T) {
	w := pitWorld()
	r := withPickaxe(newRig(t, w, feetAt(5, -2, 5)))
	r.give(3, idCobblestone, 8)
	st, err := r.run(newShaft(t, w, r, shaftSpec(2, 0, 0), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.placed < 2 {
		t.Errorf("placed %d blocks, want a pillar of at least 2", r.placed)
	}
	if r.Pose().Feet.Y() < 0.9 {
		t.Errorf("unit ended at %v, still down", r.Pose().Feet)
	}
}

func TestShaftTrappedInAPitWithNoBlocksFailsSayingSo(t *testing.T) {
	w := pitWorld()
	r := withPickaxe(newRig(t, w, feetAt(5, -2, 5)))
	st, err := r.run(newShaft(t, w, r, shaftSpec(2, 0, 0), nil), 4000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "out of blocks") {
		t.Fatalf("status %v, err %v, want a failure naming the lack of blocks", st, err)
	}
}
