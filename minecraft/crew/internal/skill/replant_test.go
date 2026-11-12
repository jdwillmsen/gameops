package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func fellAndReplant(t *testing.T, w simWorld, r *rig, qty int) (Skill, Skill) {
	t.Helper()
	f := newFell(w, r, qty)
	if err := f.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	if st, err := r.run(f, 6000); st != Done || err != nil {
		t.Fatalf("fell: status %v, err %v", st, err)
	}
	return f, NewReplant(w, r.self, f.(Cutter), "oak_sapling")
}

func TestReplantSetsASaplingAtTheFootOfTheFelledTree(t *testing.T) {
	w := oak(5)
	r := fellRig(t, w, 3)
	r.give(2, idSapling, 4)
	_, rp := fellAndReplant(t, w, r, 5)
	if st, err := r.run(rp, 4000); st != Done || err != nil {
		t.Fatalf("replant: status %v, err %v", st, err)
	}
	base := protocol.BlockPos{treeX, 1, treeZ}
	if got := w[base]; got != "oak_sapling" {
		t.Errorf("base holds %q, want a sapling", got)
	}
	if got := w.count("oak_sapling"); got != 1 {
		t.Errorf("%d saplings in the world: logs cut high up are not trunk bases", got)
	}
	if got := r.have("oak_sapling"); got != 3 {
		t.Errorf("saplings left = %d, want 3", got)
	}
	if got := rp.(Progresser).Progress(); got != 1 {
		t.Errorf("progress = %d, want 1", got)
	}
	if text, ok := rp.(Reporter).Report(); !ok || text != "replanted 1 oak_sapling" {
		t.Errorf("report = %q, %v", text, ok)
	}
}

func TestReplantWithNoSaplingsIsDoneAtOnce(t *testing.T) {
	w := oak(4)
	r := fellRig(t, w, 3)
	_, rp := fellAndReplant(t, w, r, 4)
	st, err := r.run(rp, 10)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w.count("oak_sapling") != 0 {
		t.Error("planted a sapling the unit did not have")
	}
	if _, ok := rp.(Reporter).Report(); ok {
		t.Error("reported a replanting that did not happen")
	}
}

// Ground that is not grass or dirt, or a base that something else has taken,
// is left alone and does not fail the task.
func TestReplantSkipsBasesItCannotUse(t *testing.T) {
	w := oak(4)
	w[protocol.BlockPos{treeX, 0, treeZ}] = "stone"
	r := fellRig(t, w, 3)
	r.give(2, idSapling, 2)
	_, rp := fellAndReplant(t, w, r, 4)
	st, err := r.run(rp, 100)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w.count("oak_sapling") != 0 || r.have("oak_sapling") != 2 {
		t.Errorf("planted on stone: %d saplings in the world", w.count("oak_sapling"))
	}
}

func TestMineRecordsWhereItCut(t *testing.T) {
	w := flatWorld(18)
	w[protocol.BlockPos{8, 1, 8}] = "oak_log"
	r := fellRig(t, w, 0)
	m := NewMineBlockWith(w, r.self, "oak_log", "oak_log", "axe", 1, 0)
	if st, err := r.run(m, 2000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	cut := m.(Cutter).Cut()
	if len(cut) != 1 || w[cut[0]] != "air" {
		t.Errorf("cut = %v", cut)
	}
}
