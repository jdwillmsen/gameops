package skill

import (
	"errors"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// walledChest is a chest packed into a block of stone, as a chest set against
// a bed or a wall is: the only standable cell nearest the unit is a pocket
// with walls all round it. open adds a way in from the west, and the unit
// starts east of the block.
//
//	z=7  S S S S S
//	z=6  S S [P] S S     P: sealed pocket at (9,1,6), next to the chest
//	z=5  S S C  S S      C: the chest at (8,1,5)
//	z=4  S S S S S
//	    x=6 7 8 9 10
func walledChest(open bool) simWorld {
	w := flatWorld(16)
	w.fill("stone", 6, 1, 3, 10, 3, 7)
	w[chestPos] = "chest"
	w.fill("air", 9, 1, 6, 9, 2, 6)
	w.fill("air", 8, 2, 5, 8, 2, 5)
	if open {
		// A one-wide corridor to the chest's west side.
		w.fill("air", 6, 1, 5, 7, 2, 5)
	}
	return w
}

func walledRig(t *testing.T, open bool) (*rig, simWorld) {
	w := walledChest(open)
	r := newRig(t, w, feetAt(12, 1, 6))
	r.chest[0] = slotStack{idCobblestone, 20, 900}
	return r, w
}

func newWithdraw(t *testing.T, r *rig, w simWorld) Skill {
	t.Helper()
	sk := NewWithdrawChest(w, r.self, chestPos, map[string]int{"cobblestone": 8})
	if err := sk.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	return sk
}

// The standable cell nearest the unit is sealed in; the visit tries the next
// instead of failing with "no path".
func TestWithdrawTriesAnotherStandingCellWhenTheNearestHasNoPath(t *testing.T) {
	r, w := walledRig(t, true)
	st, err := r.run(newWithdraw(t, r, w), 3000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("cobblestone"); got != 8 {
		t.Errorf("took %d cobblestone, want 8", got)
	}
}

// The way in is not loaded yet: the visit waits and looks again, and gets in
// once the terrain arrives.
func TestWithdrawRetriesAsChunksLoad(t *testing.T) {
	r, w := walledRig(t, false)
	r.onTick = func() {
		if r.ticks == 70 {
			w.fill("air", 6, 1, 5, 7, 2, 5)
		}
	}
	st, err := r.run(newWithdraw(t, r, w), 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("cobblestone"); got != 8 {
		t.Errorf("took %d cobblestone, want 8", got)
	}
}

// A chest that stays walled in ends the visit, with the reason, after a
// bounded number of attempts.
func TestWithdrawGivesUpOnAChestItCannotGetTo(t *testing.T) {
	r, w := walledRig(t, false)
	st, err := r.run(newWithdraw(t, r, w), 4000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "getting to the chest") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.ticks > chestRetries*(chestRetryTicks+5)+200 {
		t.Errorf("gave up after %d ticks: the retries are not bounded", r.ticks)
	}
	if open, _, _ := r.Container(); open {
		t.Error("the chest was left open")
	}
}

func TestDepositTriesAnotherStandingCellWhenTheNearestHasNoPath(t *testing.T) {
	r, w := walledRig(t, true)
	r.give(1, idCobblestone, 30)
	sk := NewDepositChest(w, r.self, chestPos, "cobblestone", 16)
	if err := sk.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(sk, 3000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := r.have("cobblestone"); got != 14 {
		t.Errorf("left %d cobblestone, want 14", got)
	}
}

func TestDepositGivesUpOnAChestItCannotGetTo(t *testing.T) {
	r, w := walledRig(t, false)
	r.give(1, idCobblestone, 30)
	st, err := r.run(NewDepositChest(w, r.self, chestPos, "cobblestone", 16), 4000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "getting to the chest") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

// A unit that cannot send is a dead connection, not an unreachable chest.
func TestWithdrawDoesNotRetryAWriteFailure(t *testing.T) {
	r, w := walledRig(t, true)
	r.failWriteAt = 5
	st, err := r.run(newWithdraw(t, r, w), 3000)
	if st != Failed || !errors.Is(err, errWire) {
		t.Fatalf("status %v, err %v", st, err)
	}
}

var _ = protocol.BlockPos{}
