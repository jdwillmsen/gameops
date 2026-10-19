package skill

import (
	"context"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

var (
	srcA    = protocol.BlockPos{3, 1, 3}
	srcB    = protocol.BlockPos{3, 1, 9}
	storage = protocol.BlockPos{10, 1, 6}
)

func haulWorld() simWorld {
	w := flatWorld(14)
	for _, p := range []protocol.BlockPos{srcA, srcB, storage} {
		w[p] = "chest"
	}
	return w
}

func haulRig(t *testing.T, w simWorld) *rig {
	r := newRig(t, w, feetAt(6, 1, 6))
	r.give(0, idWoodenPick, 1)
	return r
}

func (r *rig) stored(pos protocol.BlockPos, item int32) int {
	n := 0
	for _, s := range r.chestFor(pos) {
		if s.item == item {
			n += s.count
		}
	}
	return n
}

func TestHaulWaitsForItemsToAppear(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	r.fillAt(300, srcA, 0, slotStack{idCobblestone, 16, 700})
	r.fillAt(300, srcB, 0, slotStack{idOakLog, 16, 701})
	h := NewHaul(w, r.self, []protocol.BlockPos{srcA, srcB}, storage, map[string]int{"cobblestone": 16, "oak_log": 16})
	if err := h.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(h, 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.stored(storage, idCobblestone); got != 16 {
		t.Errorf("stored cobblestone = %d", got)
	}
	if got := r.stored(storage, idOakLog); got != 16 {
		t.Errorf("stored logs = %d", got)
	}
	if r.ticks < 300 {
		t.Errorf("finished after %d ticks, before the items existed", r.ticks)
	}
	if open, _, _ := r.Container(); open || !r.chestClosed {
		t.Error("a chest was left open")
	}
	if p, ok := h.(interface{ Progress() int }); !ok || p.Progress() != 32 {
		t.Errorf("progress = %v, want 32", p)
	}
}

func TestHaulMakesSeveralTripsWhenItemsExceedOneLoad(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	for i := 1; i <= 26; i++ {
		r.give(i, idDirt, 1)
	}
	for i := 0; i < 5; i++ {
		r.chestFor(srcA)[i] = slotStack{idCobblestone, 64, int32(800 + i)}
		r.chestFor(srcB)[i] = slotStack{idOakLog, 64, int32(820 + i)}
	}
	h := NewHaul(w, r.self, []protocol.BlockPos{srcA, srcB}, storage, map[string]int{"cobblestone": 320, "oak_log": 320})
	st, err := r.run(h, 20000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.places < 2 {
		t.Errorf("deposits = %d, want at least two", r.places)
	}
	if r.stored(storage, idCobblestone) != 320 || r.stored(storage, idOakLog) != 320 {
		t.Errorf("stored %d cobblestone, %d logs", r.stored(storage, idCobblestone), r.stored(storage, idOakLog))
	}
	if r.have("dirt") != 26 || r.have("wooden_pickaxe") != 1 {
		t.Errorf("the unit's own items changed: dirt %d, pickaxe %d", r.have("dirt"), r.have("wooden_pickaxe"))
	}
}

func TestWithdrawRetriesAfterARejection(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	r.chestFor(srcA)[0] = slotStack{idCobblestone, 16, 700}
	r.takeStatus = []uint8{49}
	h := NewHaul(w, r.self, []protocol.BlockPos{srcA}, storage, map[string]int{"cobblestone": 16})
	st, err := r.run(h, 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.takes != 2 || r.stored(storage, idCobblestone) != 16 {
		t.Errorf("takes %d, stored %d", r.takes, r.stored(storage, idCobblestone))
	}
}

func TestHaulFailsAfterThreeRejectionsWithoutCoordinates(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	r.chestFor(srcA)[0] = slotStack{idCobblestone, 16, 700}
	r.takeStatus = []uint8{49, 49, 49}
	h := NewHaul(w, r.self, []protocol.BlockPos{srcA}, storage, map[string]int{"cobblestone": 16})
	st, err := r.run(h, 4000)
	if st != Failed || err == nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if strings.ContainsAny(err.Error(), "0123456789") {
		t.Errorf("reason %q carries digits", err)
	}
	if r.takes != 3 {
		t.Errorf("takes = %d, want 3", r.takes)
	}
	if open, _, _ := r.Container(); open {
		t.Error("a chest was left open")
	}
}

func TestWithdrawFromAnEmptyChestIsDone(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	log := &capture{}
	s := NewWithdrawChest(w, r.self, srcA, map[string]int{"cobblestone": 16}, WithLogger(log))
	st, err := r.run(s, 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.requests != 0 || !r.chestClosed {
		t.Errorf("requests %d, closed %v", r.requests, r.chestClosed)
	}
	if len(log.lines) != 1 || log.lines[0].event != "chest_contents" {
		t.Fatalf("log = %+v", log.lines)
	}
	for k := range log.lines[0].fields {
		if k != "items" {
			t.Errorf("logged field %q", k)
		}
	}
}

func TestWithdrawTakesOnlyWhatIsWantedIntoFreeSlots(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	r.give(1, idCobblestone, 60)
	r.chestFor(srcA)[0] = slotStack{idDirt, 9, 700}
	r.chestFor(srcA)[1] = slotStack{idCobblestone, 30, 701}
	s := NewWithdrawChest(w, r.self, srcA, map[string]int{"cobblestone": 20})
	if st, err := r.run(s, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("cobblestone") != 80 || r.have("dirt") != 0 || r.stored(srcA, idCobblestone) != 10 {
		t.Errorf("cobblestone %d, dirt %d, left %d", r.have("cobblestone"), r.have("dirt"), r.stored(srcA, idCobblestone))
	}
	if r.inv[0].item != idWoodenPick {
		t.Error("the tool slot was displaced")
	}
}

func TestHaulStopClosesAnOpenChest(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	r.chestFor(srcA)[0] = slotStack{idCobblestone, 16, 700}
	h := NewHaul(w, r.self, []protocol.BlockPos{srcA}, storage, map[string]int{"cobblestone": 16})
	for i := 0; i < 2000; i++ {
		if open, _, _ := r.Container(); open {
			break
		}
		if st, err := h.Step(context.Background(), r); st != Running {
			t.Fatalf("ended early: %v %v", st, err)
		}
	}
	if open, _, _ := r.Container(); !open {
		t.Fatal("the chest never opened")
	}
	h.(Stopper).Stop(r)
	if open, _, _ := r.Container(); open || !r.chestClosed {
		t.Error("the chest is still open after Stop")
	}
}

func TestWithdrawOneOfTakesTheBestPresentItemAndABudgetOfFood(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	c := r.chestFor(srcA)
	c[0] = slotStack{300, 1, 700} // wooden sword
	c[1] = slotStack{343, 6, 701} // stone swords
	c[2] = slotStack{idCobblestone, 5, 702}
	c[3] = slotStack{340, 64, 703} // bread
	swords := OneOf(1, "iron_sword", "stone_sword", "wooden_sword")
	s := NewWithdrawChest(w, r.self, srcA, nil, swords, OneOf(8, "bread"))
	if st, err := r.run(s, 900); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("stone_sword") != 1 || r.have("wooden_sword") != 0 || r.have("bread") != 8 || r.have("cobblestone") != 0 {
		t.Errorf("stone %d wooden %d bread %d cobble %d", r.have("stone_sword"), r.have("wooden_sword"), r.have("bread"), r.have("cobblestone"))
	}
	if r.stored(srcA, 343) != 5 || r.stored(srcA, 340) != 56 {
		t.Errorf("left %d stone swords, %d bread", r.stored(srcA, 343), r.stored(srcA, 340))
	}
}

func TestWithdrawOneOfSpendsItsBudgetDownTheRanking(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	c := r.chestFor(srcA)
	c[0] = slotStack{340, 10, 700} // bread, before the better food in slot order
	c[1] = slotStack{341, 2, 701}  // cooked beef
	s := NewWithdrawChest(w, r.self, srcA, nil, OneOf(5, "cooked_beef", "bread"))
	if st, err := r.run(s, 900); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("cooked_beef") != 2 || r.have("bread") != 3 {
		t.Errorf("beef %d bread %d, want 2 and 3", r.have("cooked_beef"), r.have("bread"))
	}
}

func TestWithdrawIntoHotbarNeverLandsOffTheHotbar(t *testing.T) {
	w := haulWorld()
	r := haulRig(t, w)
	for slot := 1; slot < 8; slot++ {
		r.give(slot, idCobblestone, 64)
	}
	r.give(20, 340, 10) // bread off the hotbar, which a plain withdraw would merge onto
	c := r.chestFor(srcA)
	c[0] = slotStack{343, 2, 700}  // stone swords
	c[1] = slotStack{340, 64, 701} // bread
	s := NewWithdrawChest(w, r.self, srcA, nil, OneOf(1, "stone_sword"), OneOf(8, "bread"), IntoHotbar())
	if st, err := r.run(s, 900); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.inv[8].item != 343 || r.inv[20].count != 10 || r.have("bread") != 10 {
		t.Errorf("slot 8 holds %d, off-hotbar bread %d, bread %d", r.inv[8].item, r.inv[20].count, r.have("bread"))
	}
	if !s.(*Withdraw).Full() || r.stored(srcA, 340) != 64 {
		t.Errorf("full=%v, bread left %d, want the bread left behind", s.(*Withdraw).Full(), r.stored(srcA, 340))
	}
}
