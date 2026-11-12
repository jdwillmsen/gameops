package skill

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func seekBox() Area {
	return Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{170, 30, 41}}
}

// leafWorld has a canopy no one can stand beside near the start and a reachable
// clump of leaves well past the mining radius.
func leafWorld() simWorld {
	w := simWorld{}
	w.platform(0, 170, 0, 41)
	w.fill("air", 0, 7, 0, 170, 30, 41)
	// A canopy ten blocks up, over the start.
	w.fill("oak_leaves", 3, 12, 18, 9, 14, 24)
	// Leaves on the ground, far east.
	w.fill("oak_leaves", 70, 1, 20, 73, 2, 22)
	return w
}

func TestSeekingMineLooksFurtherWhenNothingIsInReach(t *testing.T) {
	w := leafWorld()
	r, ww := exploreRig(t, w, 5, 1, 20)
	r.leafDropEvery = 3
	m := NewMineBlockWith(ww, r.self, "oak_leaves", "oak_sapling", "", 2, 0, Chancy(ChancyMisses), Seeking(seekBox(), nil))
	st, err := r.run(m, 40000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("oak_sapling"); got != 2 {
		t.Errorf("saplings = %d, want 2", got)
	}
	if got := w.count("oak_leaves"); got >= 7*3*7+4*2*3 {
		t.Error("no leaf was broken")
	}
	if w[protocol.BlockPos{5, 13, 21}] != "oak_leaves" {
		t.Error("the canopy nobody can reach was broken")
	}
}

func TestMineWithoutASearchBoxStillFailsWhereItStands(t *testing.T) {
	w := leafWorld()
	r, ww := exploreRig(t, w, 5, 1, 20)
	m := NewMineBlockWith(ww, r.self, "oak_leaves", "oak_sapling", "", 1, 0, Chancy(ChancyMisses))
	st, err := r.run(m, 4000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no reachable oak_leaves within 24 blocks") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestSeekingMineFailsOnceTheBoxHasNothingToReach(t *testing.T) {
	w := leafWorld()
	w.fill("air", 70, 1, 20, 73, 2, 22)
	r, ww := exploreRig(t, w, 5, 1, 20)
	m := NewMineBlockWith(ww, r.self, "oak_leaves", "oak_sapling", "", 1, 0, Chancy(ChancyMisses), Seeking(seekBox(), nil))
	st, err := r.run(m, 40000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no oak_leaves found in search area") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

// Leaves dying near a felled tree drop saplings the unit walks over; they
// count towards the quantity whether or not the unit broke a leaf for them.
func TestMineCountsASaplingPickedUpOnTheWay(t *testing.T) {
	w := flatWorld(30)
	w.fill("oak_leaves", 20, 1, 20, 22, 3, 22)
	r := newRig(t, w, feetAt(2, 1, 20))
	r.onTick = func() {
		if r.ticks == 6 {
			r.pickUp(idSapling)
		}
	}
	m := NewMineBlockWith(w, r.self, "oak_leaves", "oak_sapling", "", 1, 0, Chancy(ChancyMisses))
	st, err := r.run(m, 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.breaks != 0 {
		t.Errorf("broke %d leaves after the sapling was already in hand", r.breaks)
	}
}
