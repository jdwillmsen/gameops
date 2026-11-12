package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	idRawIron     = 60
	idDiamond     = 61
	idStonePick   = 62
	idIronPick    = 410
	idFurnaceItem = 63
	idIronIngot   = 64
	idCharcoal    = 65
	idRawGold     = 66
)

func init() {
	for name, id := range map[string]int16{
		"raw_iron": idRawIron, "diamond": idDiamond, "stone_pickaxe": idStonePick,
		"furnace": idFurnaceItem, "iron_ingot": idIronIngot, "charcoal": idCharcoal,
	} {
		itemTable = append(itemTable, protocol.ItemEntry{Name: "minecraft:" + name, RuntimeID: id})
	}
}

// noDigDown fails the test if the unit ever breaks a block in the column it
// stands in, below its feet: the shaft descends by stairs, never by a hole.
func noDigDown(t *testing.T, r *rig) {
	t.Helper()
	r.onTick = func() {
		if b := r.breaking; b != nil {
			feet := cellOf(r.Pose().Feet)
			if b.pos.X() == feet.X() && b.pos.Z() == feet.Z() && b.pos.Y() < feet.Y() {
				t.Errorf("broke %v from %v: digging straight down", b.pos, feet)
			}
		}
	}
}

func oreSpec(depth int, kinds ...OreKind) ShaftSpec {
	s := shaftSpec(depth, 0, 0)
	s.Ores = kinds
	return s
}

func events(log *capture, event string) []map[string]any {
	var out []map[string]any
	for _, l := range log.lines {
		if l.event == event {
			out = append(out, l.fields)
		}
	}
	return out
}

func TestShaftNeverDigsStraightDown(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	noDigDown(t, r)
	st, err := r.run(newShaft(t, w, r, shaftSpec(6, 1, 3), nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	// Every step forward is also a step down, and no two stands share a column.
	seen := map[[2]int32]bool{}
	for i := int32(1); i <= 6; i++ {
		col := [2]int32{10 + i, 10}
		if seen[col] {
			t.Errorf("column %v used twice", col)
		}
		seen[col] = true
		if n := w[protocol.BlockPos{10 + i, 1 - i, 10}]; n != "air" {
			t.Errorf("step %d floor cell is %q", i, n)
		}
	}
}

func TestShaftTakesExposedCoalBesideTheStair(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	r := shaftRig(t, w)
	noDigDown(t, r)
	log := &capture{}
	s := newShaft(t, w, r, oreSpec(4, OreCoal), log)
	st, err := r.run(s, 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{12, -1, 11}]; n != "air" {
		t.Errorf("the coal ore is still %q", n)
	}
	if got := chestCount(r, idCoal); got != 1 {
		t.Errorf("coal in the chest = %d, want 1", got)
	}
	if got := len(events(log, "mine_ore")); got != 1 {
		t.Errorf("mine_ore events = %d, want 1", got)
	}
}

func TestShaftTakesOnlyTheOreItCanCollect(t *testing.T) {
	w := mineWorld()
	// A vein running into the wall: the first block is beside the stair, the
	// others lie in a pocket with no headroom the unit could stand in, so their
	// drops would be lost.
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	w[protocol.BlockPos{12, -1, 12}] = "coal_ore"
	w[protocol.BlockPos{12, -1, 13}] = "coal_ore"
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, oreSpec(3, OreCoal), nil), 10000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("coal_ore"); got != 2 {
		t.Errorf("%d coal ore left, want the 2 whose drops could not be collected", got)
	}
	if got := chestCount(r, idCoal); got != 1 {
		t.Errorf("coal in the chest = %d, want 1", got)
	}
}

func TestShaftLeavesOreSealedInStone(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 12}] = "coal_ore" // two cells from the stair, behind stone
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{12, -1, 12}]; n != "coal_ore" {
		t.Errorf("sealed ore was broken: %q", n)
	}
}

func TestShaftOnlyTakesTheOresItWasAskedFor(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, oreSpec(4), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{12, -1, 11}]; n != "coal_ore" {
		t.Errorf("unrequested ore was broken: %q", n)
	}
}

func TestShaftLeavesOreWhoseBreakWouldLetLavaIn(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	w[protocol.BlockPos{12, -1, 12}] = "lava"
	r := shaftRig(t, w)
	log := &capture{}
	// Lava two cells from the stair: the stair itself is clear of it.
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), log), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{12, -1, 11}]; n != "coal_ore" {
		t.Errorf("ore beside lava was broken: %q", n)
	}
	skipped := events(log, "mine_ore_skipped")
	if len(skipped) != 1 || skipped[0]["why"] != "lava" {
		t.Errorf("skip events = %v, want one for lava", skipped)
	}
	nextToLiquid(t, r)
}

func TestShaftLeavesOreUnderWater(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	w[protocol.BlockPos{12, 0, 11}] = "water"
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{12, -1, 11}]; n != "coal_ore" {
		t.Errorf("ore under water was broken: %q", n)
	}
}

func TestShaftLeavesTheOreItStandsOn(t *testing.T) {
	w := mineWorld()
	floor := protocol.BlockPos{12, -2, 10} // the floor of the second step
	w[floor] = "coal_ore"
	r := shaftRig(t, w)
	noDigDown(t, r)
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[floor]; n != "coal_ore" {
		t.Errorf("the stair's own floor was broken: %q", n)
	}
}

func TestShaftNeedsTheRightPickaxeForIron(t *testing.T) {
	iron := protocol.BlockPos{12, -1, 11}
	for _, c := range []struct {
		name     string
		give     int32
		wantGone bool
	}{
		{"wooden pickaxe leaves iron", idWoodenPick, false},
		{"stone pickaxe takes iron", idStonePick, true},
		{"iron pickaxe takes iron", idIronPick, true},
	} {
		w := mineWorld()
		w[iron] = "iron_ore"
		r := newRig(t, w, feetAt(3, 1, 3))
		r.give(0, c.give, 1)
		log := &capture{}
		st, err := r.run(newShaft(t, w, r, oreSpec(4, OreIron), log), 8000)
		if st != Done || err != nil {
			t.Fatalf("%s: status %v, err %v", c.name, st, err)
		}
		if gone := w[iron] != "iron_ore"; gone != c.wantGone {
			t.Errorf("%s: iron gone = %v, want %v", c.name, gone, c.wantGone)
		}
		if c.wantGone && chestCount(r, idRawIron) != 1 {
			t.Errorf("%s: raw iron in the chest = %d", c.name, chestCount(r, idRawIron))
		}
		if !c.wantGone {
			skipped := events(log, "mine_ore_skipped")
			if len(skipped) != 1 || skipped[0]["why"] != "pickaxe too weak" {
				t.Errorf("%s: skip events = %v", c.name, skipped)
			}
		}
	}
}

func TestShaftUsesTheBestPickaxeOnTheHotbar(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 11}] = "iron_ore"
	r := newRig(t, w, feetAt(3, 1, 3))
	r.give(0, idWoodenPick, 1)
	r.give(1, idStonePick, 1)
	var heldWhileBreaking []int
	r.onTick = func() {
		if r.breaking != nil {
			heldWhileBreaking = append(heldWhileBreaking, r.held)
		}
	}
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreIron), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{12, -1, 11}]; n == "iron_ore" {
		t.Error("iron stayed with a stone pickaxe on the hotbar")
	}
	for _, h := range heldWhileBreaking {
		if h != 1 {
			t.Fatalf("broke with hotbar slot %d held, want the stone pickaxe in 1", h)
		}
	}
	if len(heldWhileBreaking) == 0 {
		t.Fatal("nothing was broken")
	}
}

func TestShaftStopsDiggingOnceTheOreGoalIsMet(t *testing.T) {
	w := mineWorld()
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	r := shaftRig(t, w)
	spec := oreSpec(8, OreCoal)
	spec.OreGoal = map[string]int{"coal": 1}
	st, err := r.run(newShaft(t, w, r, spec, nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := chestCount(r, idCoal); got != 1 {
		t.Errorf("coal in the chest = %d, want 1", got)
	}
	// The plan is 8 steps; stopping at 2 leaves the rest closed.
	if n := w[protocol.BlockPos{18, -7, 10}]; n != "stone" {
		t.Errorf("the last step was dug (%q) after the goal was met", n)
	}
	if r.Pose().Feet.Y() < 0.9 {
		t.Errorf("unit ended at depth %v", r.Pose().Feet)
	}
}

func TestShaftOreGoalCountsOnlyWhatWasGained(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	r.give(5, idCoal, 3) // the unit starts with coal; it does not count
	spec := oreSpec(8, OreCoal)
	spec.OreGoal = map[string]int{"coal": 1}
	w[protocol.BlockPos{12, -1, 11}] = "coal_ore"
	st, err := r.run(newShaft(t, w, r, spec, nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := chestCount(r, idCoal); got != 1 {
		t.Errorf("coal in the chest = %d, want the one gained", got)
	}
	if got := r.have("coal"); got != 3 {
		t.Errorf("unit carries %d coal, want the 3 it came with", got)
	}
	if n := w[protocol.BlockPos{18, -7, 10}]; n != "stone" {
		t.Errorf("the last step was dug (%q) after the goal was met", n)
	}
}

func torchedShaft(t *testing.T, torches int, depth int) (*rig, simWorld, *capture) {
	t.Helper()
	w := mineWorld()
	r := shaftRig(t, w)
	if torches > 0 {
		r.give(4, idTorch, torches)
	}
	spec := shaftSpec(depth, 0, 0)
	spec.Torches = true
	log := &capture{}
	st, err := r.run(newShaft(t, w, r, spec, log), 20000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	return r, w, log
}

func TestShaftLightsTheDarkStair(t *testing.T) {
	r, w, _ := torchedShaft(t, 16, 12)
	torches := w.count("torch")
	if torches == 0 {
		t.Fatal("no torch was placed in a 12-step stair")
	}
	// A torch lights about three steps of a stair, so a torch on every step
	// would be a waste of coal.
	if torches > 12/2+1 {
		t.Errorf("%d torches for 12 steps", torches)
	}
	if got := r.have("torch"); got != 16-torches {
		t.Errorf("torches left = %d, want %d", got, 16-torches)
	}
	for i := int32(3); i <= 12; i++ {
		stand := protocol.BlockPos{10 + i, 1 - i, 10}
		if l := blockLight(w, stand); l < torchBelow {
			t.Errorf("step %d is lit to %d, want at least %d", i, l, torchBelow)
		}
	}
}

func TestShaftPlacesNoTorchNearTheSurface(t *testing.T) {
	_, w, _ := torchedShaft(t, 16, 2)
	if n := w.count("torch"); n != 0 {
		t.Errorf("%d torches in the open first steps", n)
	}
}

func TestShaftWithoutTorchesDigsOnInTheDarkAndSaysSoOnce(t *testing.T) {
	_, w, log := torchedShaft(t, 0, 9)
	if n := w.count("torch"); n != 0 {
		t.Errorf("%d torches from nowhere", n)
	}
	dark := events(log, "mine_dark")
	if len(dark) != 1 || dark[0]["reason"] != "no torches" {
		t.Errorf("mine_dark events = %v, want one for no torches", dark)
	}
}

func TestShaftDoesNotPlaceTorchesUnlessAsked(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	r.give(4, idTorch, 8)
	st, err := r.run(newShaft(t, w, r, shaftSpec(8, 0, 0), nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w.count("torch"); n != 0 {
		t.Errorf("%d torches placed without Torches set", n)
	}
}

// The path planner counts a cell above lava as standable, so an ore whose drop
// would fall onto a pool, or come to rest beside liquid, is left alone: going
// for the drop would walk the unit into it.
func TestShaftLeavesOreWhoseDropWouldNeedAWalkNearLiquid(t *testing.T) {
	ore := protocol.BlockPos{12, -1, 11}
	for _, c := range []struct {
		name  string
		setup func(w simWorld)
		why   string
	}{
		{"drop falls onto lava", func(w simWorld) {
			w[protocol.BlockPos{12, -2, 11}] = "air"
			w[protocol.BlockPos{12, -3, 11}] = "air"
			w[protocol.BlockPos{12, -4, 11}] = "lava"
		}, "lava"},
		{"lava beside the cell it rests in", func(w simWorld) {
			w[protocol.BlockPos{13, -2, 12}] = "lava"
		}, "lava"},
		{"water beside the cell it rests in", func(w simWorld) {
			w[protocol.BlockPos{13, -2, 11}] = "water"
		}, "water"},
		{"lava under a stand beside the drop", func(w simWorld) {
			w[protocol.BlockPos{11, -3, 11}] = "lava"
		}, "lava"},
		{"gravel over the ore", func(w simWorld) {
			w[protocol.BlockPos{12, 0, 11}] = "gravel"
		}, "falling"},
	} {
		w := mineWorld()
		w[ore] = "coal_ore"
		c.setup(w)
		r := shaftRig(t, w)
		log := &capture{}
		st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), log), 8000)
		if st != Done || err != nil {
			t.Fatalf("%s: status %v, err %v", c.name, st, err)
		}
		if n := w[ore]; n != "coal_ore" {
			t.Errorf("%s: the ore was broken (%q)", c.name, n)
		}
		skipped := events(log, "mine_ore_skipped")
		if len(skipped) != 1 || skipped[0]["why"] != c.why {
			t.Errorf("%s: skip events = %v, want one for %s", c.name, skipped, c.why)
		}
		if got := chestCount(r, idCoal); got != 0 {
			t.Errorf("%s: coal in the chest = %d", c.name, got)
		}
	}
}

func TestShaftStillTakesOreWithASafeDrop(t *testing.T) {
	w := mineWorld()
	ore := protocol.BlockPos{12, -1, 11}
	w[ore] = "coal_ore"
	// A hole under the ore, lava three cells further down and clear of every
	// cell the unit could stand in to take the drop.
	w[protocol.BlockPos{12, -2, 11}] = "air"
	w[protocol.BlockPos{12, -6, 11}] = "lava"
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), nil), 8000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[ore]; n != "air" {
		t.Errorf("a safe ore was left (%q)", n)
	}
}

// Done means the goal was met: a controller walking the ladder resubmits a
// task that ends Done with the goal unmet for ever.
func TestShaftFailsWhenTheDiveEndsShortOfItsGoal(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(w simWorld)
		kinds []OreKind
		goal  map[string]int
		want  string
	}{
		{"too little ore", func(w simWorld) { w[protocol.BlockPos{12, -1, 11}] = "coal_ore" },
			[]OreKind{OreCoal}, map[string]int{"coal": 3}, "the dive found 1 of 3 coal"},
		{"no ore at all", func(w simWorld) {},
			[]OreKind{OreCoal}, map[string]int{"coal": 1}, "the dive found 0 of 1 coal"},
		{"pickaxe too weak for the goal", func(w simWorld) { w[protocol.BlockPos{12, -1, 11}] = "diamond_ore" },
			[]OreKind{OreDiamond}, map[string]int{"diamond": 1}, "the dive found 0 of 1 diamond"},
	} {
		w := mineWorld()
		c.setup(w)
		r := shaftRig(t, w)
		spec := oreSpec(4, c.kinds...)
		spec.OreGoal = c.goal
		st, err := r.run(newShaft(t, w, r, spec, nil), 12000)
		if st != Failed || err == nil || err.Error() != c.want {
			t.Errorf("%s: status %v, err %v; want Failed %q", c.name, st, err, c.want)
		}
		if got := r.have("coal"); got != 0 {
			t.Errorf("%s: %d coal left on the unit after the deposit", c.name, got)
		}
	}
}

func TestShaftWithoutAGoalIsDoneWhateverItFinds(t *testing.T) {
	w := mineWorld()
	r := shaftRig(t, w)
	st, err := r.run(newShaft(t, w, r, oreSpec(4, OreCoal), nil), 12000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}
