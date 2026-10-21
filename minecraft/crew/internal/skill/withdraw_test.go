package skill

import (
	"errors"
	"strings"
	"testing"
)

func withdrawRig(t *testing.T) *rig {
	return newRig(t, chestWorld(), feetAt(6, 1, 5))
}

func withdrawSkill(t *testing.T, r *rig, log Logger) *Withdraw {
	t.Helper()
	wr := NewWithdrawChest(r.world, r.self, chestPos, map[string]int{"cobblestone": 8}, WithLogger(log)).(*Withdraw)
	if err := wr.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	return wr
}

func TestWithdrawFailsWhenTheChestDoesNotOpen(t *testing.T) {
	r := withdrawRig(t)
	r.ignoreOpen = true
	wr := withdrawSkill(t, r, nil)
	st, err := r.run(wr, 400)
	if st != Failed || err == nil || !errors.Is(err, ErrChestNotOpened) {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := err.Error(); !strings.Contains(got, "chest did not open within") {
		t.Errorf("err = %q, want the open's own timeout named", got)
	}
	if r.clicks != openClicks {
		t.Errorf("clicks = %d, want %d, the first and its re-sends", r.clicks, openClicks)
	}
	if open, _, _ := r.Container(); open {
		t.Error("a chest that never opened was left open")
	}
}

func TestWithdrawReSendsAClickTheServerSwallows(t *testing.T) {
	r := withdrawRig(t)
	r.ignoreFirstOpens = 1
	log := &capture{}
	wr := withdrawSkill(t, r, log)
	st, err := r.run(wr, 400)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.clicks != 2 {
		t.Errorf("clicks = %d, want the first click and its re-send", r.clicks)
	}
	if open, _, _ := r.Container(); open {
		t.Error("the chest was left open")
	}
	if len(log.lines) != 2 || log.lines[0].event != "chest_open_clicks" || log.lines[1].event != "chest_contents" {
		t.Fatalf("log = %+v", log.lines)
	}
	clicks, _ := log.lines[0].fields["clicks"].(int)
	if clicks != 2 {
		t.Errorf("logged clicks = %v, want 2", log.lines[0].fields)
	}
	for k := range log.lines[0].fields {
		if k != "clicks" {
			t.Errorf("logged field %q: no coordinates or contents belong here", k)
		}
	}
}

func TestWithdrawOpensOnTheFirstClick(t *testing.T) {
	r := withdrawRig(t)
	log := &capture{}
	wr := withdrawSkill(t, r, log)
	st, err := r.run(wr, 400)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.clicks != 1 {
		t.Errorf("clicks = %d, want 1", r.clicks)
	}
	for _, l := range log.lines {
		if l.event == "chest_open_clicks" {
			t.Error("a single-click open was logged as needing re-sends")
		}
	}
}
