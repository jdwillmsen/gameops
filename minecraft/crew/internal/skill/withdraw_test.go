package skill

import (
	"errors"
	"strings"
	"testing"
)

func withdrawRig(t *testing.T) *rig {
	return newRig(t, chestWorld(), feetAt(6, 1, 5))
}

func TestWithdrawFailsWhenTheChestDoesNotOpen(t *testing.T) {
	r := withdrawRig(t)
	r.ignoreOpen = true
	wr := NewWithdrawChest(r.world, r.self, chestPos, map[string]int{"cobblestone": 8})
	if err := wr.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(wr, 400)
	if st != Failed || err == nil || !errors.Is(err, ErrChestNotOpened) {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := err.Error(); !strings.Contains(got, "chest did not open within") {
		t.Errorf("err = %q, want the open's own timeout named", got)
	}
	if open, _, _ := r.Container(); open {
		t.Error("a chest that never opened was left open")
	}
}
