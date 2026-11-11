package skill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func groveArea() Area {
	return Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{11, 5, 11}}
}

func TestPlantSetsASaplingOnEveryGridCell(t *testing.T) {
	w := flatWorld(14)
	w.fill("grass_block", 0, 0, 0, 13, 0, 13)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 20)
	p := NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 20)
	if err := p.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(p, 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	for _, x := range []int32{0, 4, 8} {
		for _, z := range []int32{0, 4, 8} {
			if got := w[protocol.BlockPos{x, 1, z}]; got != "oak_sapling" {
				t.Errorf("(%d,%d) holds %q", x, z, got)
			}
		}
	}
	if got := w.count("oak_sapling"); got != 9 {
		t.Errorf("%d saplings in the world, want 9", got)
	}
	if got := r.have("oak_sapling"); got != 11 {
		t.Errorf("saplings left = %d, want 11", got)
	}
	if got := p.(Progresser).Progress(); got != 9 {
		t.Errorf("progress = %d, want 9", got)
	}
}

func TestPlantSkipsCellsThatAreTakenOrHaveNoSky(t *testing.T) {
	w := flatWorld(14)
	// Dirt under leaves, under a log, and a cell with a sapling already.
	w[protocol.BlockPos{4, 3, 4}] = "oak_leaves"
	w[protocol.BlockPos{0, 1, 4}] = "oak_log"
	w[protocol.BlockPos{8, 1, 8}] = "oak_sapling"
	// Ground that is not grass or dirt.
	w[protocol.BlockPos{8, 0, 0}] = "stone"
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 20)
	st, err := r.run(NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 20), 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("oak_sapling"); got != 1+5 {
		t.Errorf("%d saplings in the world, want the old one and 5 new", got)
	}
	for _, c := range []protocol.BlockPos{{4, 1, 4}, {0, 1, 4}, {8, 1, 0}} {
		if got := w[c]; got == "oak_sapling" {
			t.Errorf("%v was planted", c)
		}
	}
}

func TestPlantStopsAtQty(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 20)
	p := NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 3)
	st, err := r.run(p, 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("oak_sapling"); got != 3 {
		t.Errorf("%d saplings planted, want 3", got)
	}
	if got := p.(Progresser).Progress(); got != 3 {
		t.Errorf("progress = %d, want 3", got)
	}
}

func TestPlantEndsWhenTheInventoryRunsOut(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 2)
	st, err := r.run(NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 20), 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("oak_sapling"); got != 2 {
		t.Errorf("%d saplings planted, want 2", got)
	}
}

func TestPlantNeedsSaplingsToStart(t *testing.T) {
	w := flatWorld(14)
	p := NewPlant(w, nil, "oak_sapling", groveArea(), 4, 3)
	if err := p.Ready(belief.Snapshot{}); err == nil || !strings.Contains(err.Error(), "no oak_sapling in inventory") {
		t.Errorf("err = %v", err)
	}
}

// A grove with nothing to plant in is done, not failed: a full grove is the
// work loop's goal.
func TestPlantOnAFullGroveIsDone(t *testing.T) {
	w := flatWorld(14)
	for _, x := range []int32{0, 4, 8} {
		for _, z := range []int32{0, 4, 8} {
			w[protocol.BlockPos{x, 1, z}] = "oak_sapling"
		}
	}
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 5)
	st, err := r.run(NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 5), 100)
	if st != Done || err != nil || r.placeClicks != 0 {
		t.Fatalf("status %v, err %v, %d placements", st, err, r.placeClicks)
	}
}

func TestPlaceConfirmsASaplingUnderEitherBlockName(t *testing.T) {
	for _, tc := range []struct {
		item, block string
		want        bool
	}{
		{"oak_sapling", "oak_sapling", true},
		{"oak_sapling", "sapling", true},
		{"oak_sapling", "oak_leaves", false},
		{"dirt", "sapling", false},
		{"cobblestone", "cobblestone", true},
	} {
		if got := placedAs(tc.item, tc.block); got != tc.want {
			t.Errorf("placedAs(%q, %q) = %v", tc.item, tc.block, got)
		}
	}
}

// A dead connection ends the planting at once; it is not one more cell to
// skip, which would end the run as done.
func TestPlantFailsOnADeadConnection(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 20)
	r.failWriteAt = 40
	st, err := r.run(NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 20), 6000)
	if st != Failed || !errors.Is(err, errWire) {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestPlantFailsOnACancelledRun(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.give(0, idSapling, 20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st, err := NewPlant(w, r.self, "oak_sapling", groveArea(), 4, 20).Step(ctx, r)
	if st != Failed || !errors.Is(err, context.Canceled) {
		t.Fatalf("status %v, err %v", st, err)
	}
}
