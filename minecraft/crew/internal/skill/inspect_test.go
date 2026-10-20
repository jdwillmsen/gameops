package skill

import (
	"strings"
	"testing"
)

func TestInspectReportsTheContentsAndTakesNothing(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 1, 1)
	r.chest[0] = slotStack{idDirt, 5, 900}
	r.chest[3] = slotStack{idCobblestone, 10, 901}
	r.chest[9] = slotStack{idDirt, 3, 902}
	in := NewInspect(w, r.self, chestPos)
	if err := in.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(in, 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	seen, ok := in.(Seer).Seen()
	if !ok || seen.Chest != chestPos || seen.At.IsZero() {
		t.Fatalf("seen = %+v, %v", seen, ok)
	}
	if len(seen.Items) != 2 || seen.Items["dirt"] != 8 || seen.Items["cobblestone"] != 10 {
		t.Errorf("items = %v", seen.Items)
	}
	if got := r.have("cobblestone"); got != 42 {
		t.Errorf("inventory changed: %d cobblestone", got)
	}
	if open, _, _ := r.Container(); open || !r.chestClosed {
		t.Error("the chest was left open")
	}
}

func TestInspectOfAnEmptyChestSeesNothing(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	in := NewInspect(w, r.self, chestPos)
	if st, err := r.run(in, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if seen, ok := in.(Seer).Seen(); !ok || len(seen.Items) != 0 {
		t.Errorf("seen = %+v, %v", seen, ok)
	}
}

func TestInspectFailsOnAirWithoutClicking(t *testing.T) {
	w := chestWorld()
	w[chestPos] = "air"
	r := depositRig(t, w, 6, 5)
	in := NewInspect(w, r.self, chestPos)
	st, err := r.run(in, 600)
	if st != Failed || err == nil || err.Error() != "not a chest" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.opens != 0 {
		t.Error("it clicked a block that is not a chest")
	}
	if _, ok := in.(Seer).Seen(); ok {
		t.Error("it reported contents it never saw")
	}
}

func TestInspectFailsWhenTheWindowNeverOpens(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	r.ignoreOpen = true
	st, err := r.run(NewInspect(w, r.self, chestPos), 600)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "not a chest") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestDepositReportsTheChestItClosed(t *testing.T) {
	w := chestWorld()
	r := depositRig(t, w, 6, 5)
	r.chest[0] = slotStack{idDirt, 5, 900}
	d := NewDepositChest(w, r.self, chestPos, "cobblestone", 16)
	if _, ok := d.(Seer).Seen(); ok {
		t.Fatal("seen before anything was closed")
	}
	if st, err := r.run(d, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	seen, ok := d.(Seer).Seen()
	if !ok || seen.Chest != chestPos || seen.Items["cobblestone"] != 16 || seen.Items["dirt"] != 5 {
		t.Errorf("seen = %+v, %v", seen, ok)
	}
}
