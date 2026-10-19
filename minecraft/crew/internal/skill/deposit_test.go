package skill

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

var chestPos = protocol.BlockPos{8, 1, 5}

func chestWorld() simWorld {
	w := flatWorld(14)
	w[chestPos] = "chest"
	return w
}

func depositRig(t *testing.T, w simWorld, x, z float32) *rig {
	r := newRig(t, w, feetAt(x, 1, z))
	r.give(0, idWoodenPick, 1)
	r.give(1, idCobblestone, 42)
	return r
}

func TestDepositMovesSixteenIntoTheChest(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 1, 1)
	log := &capture{}
	d := NewDepositChest(w, r.self, chestPos, "cobblestone", 16, WithLogger(log))
	if err := d.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(d, 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.chest[0].item != idCobblestone || r.chest[0].count != 16 {
		t.Errorf("chest slot 0 = %+v", r.chest[0])
	}
	if got := r.have("cobblestone"); got != 26 {
		t.Errorf("left in the inventory = %d, want 26", got)
	}
	if open, _, _ := r.Container(); open || !r.chestClosed {
		t.Error("the chest was left open")
	}
	if len(log.lines) != 1 || log.lines[0].event != "chest_contents" {
		t.Fatalf("log = %+v", log.lines)
	}
	items, _ := log.lines[0].fields["items"].(map[string]int)
	if len(items) != 1 || items["cobblestone"] != 16 {
		t.Errorf("logged items = %v", log.lines[0].fields)
	}
	for k := range log.lines[0].fields {
		if k != "items" {
			t.Errorf("logged field %q: only item names and counts belong in the log", k)
		}
	}
}

func TestDepositSpreadsOverStacksAndSkipsOtherItems(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	r.chest[0] = slotStack{idDirt, 5, 900}
	r.chest[1] = slotStack{idCobblestone, 60, 901}
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 10), 400)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.chest[0].item != idDirt || r.chest[0].count != 5 {
		t.Errorf("other item's slot changed: %+v", r.chest[0])
	}
	if r.chest[1].count != 64 || r.chest[2].item != idCobblestone || r.chest[2].count != 6 {
		t.Errorf("chest = %+v %+v", r.chest[1], r.chest[2])
	}
}

func TestDepositFailsWhenTheChestDoesNotOpen(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	r.ignoreOpen = true
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 16), 400)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "chest did not open") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.requests != 0 {
		t.Error("it deposited into a chest that never opened")
	}
}

func TestDepositFailsWhenTheChestIsFull(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	for i := range r.chest {
		r.chest[i] = slotStack{idDirt, 64, int32(800 + i)}
	}
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 16), 400)
	if st != Failed || err == nil || err.Error() != "chest is full" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.requests != 0 {
		t.Errorf("sent %d requests into a chest it could see was full", r.requests)
	}
	if !r.chestClosed {
		t.Error("the full chest was left open")
	}
}

func TestDepositFailsWhenTheServerDoesNotAnswer(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	r.dropResponses = true
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 16), 400)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if !r.chestClosed {
		t.Error("the chest was left open")
	}
}

func TestDepositFailsOnARejectedSource(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	r.rejectWith = 49
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 16), 400)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestDepositFailsWhenThereIsNoChest(t *testing.T) {
	w := chestWorld()
	w[chestPos] = "air"
	r := depositRig(t, w, 6, 5)
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 16), 100)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "no chest") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestDepositIsNotReadyWithTooLittle(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	err := NewDepositChest(w, r.self, chestPos, "cobblestone", 99).Ready(r.self())
	if err == nil || !strings.Contains(err.Error(), "cobblestone") {
		t.Fatalf("Ready = %v", err)
	}
}
