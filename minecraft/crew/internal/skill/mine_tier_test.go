package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func ironPatch() simWorld {
	w := quarry()
	for i := int32(0); i < 4; i++ {
		w[protocol.BlockPos{6 + i, 1, 3}] = "iron_ore"
	}
	return w
}

func TestMineIronNeedsAStonePickaxeAndSaysSo(t *testing.T) {
	for _, c := range []struct {
		pick int32
		want string
	}{
		{idWoodenPick, "no stone_pickaxe or better in inventory"},
		{idStonePick, ""},
		{idIronPick, ""},
	} {
		w := ironPatch()
		r := newRig(t, w, feetAt(2, 1, 2))
		r.give(0, c.pick, 1)
		m := NewMineBlockWith(w, r.self, "iron_ore", "raw_iron", "pickaxe", 2, 0, NeedTier(1))
		err := m.Ready(r.self())
		if c.want != "" {
			if err == nil || err.Error() != c.want {
				t.Errorf("pick %d: Ready = %v, want %q", c.pick, err, c.want)
			}
			continue
		}
		if err != nil {
			t.Fatalf("pick %d: %v", c.pick, err)
		}
		st, err := r.run(m, 1500)
		if st != Done || err != nil {
			t.Fatalf("pick %d: status %v, err %v", c.pick, st, err)
		}
		if got := r.have("raw_iron"); got != 2 {
			t.Errorf("pick %d: raw iron = %d, want 2", c.pick, got)
		}
	}
}

func TestMineWithoutATierTakesAnyPickaxe(t *testing.T) {
	w := quarry()
	r := newRig(t, w, feetAt(2, 1, 2))
	r.give(0, idWoodenPick, 1)
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 1, 0)
	if err := m.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
}

func TestMineHoldsTheBestPickaxe(t *testing.T) {
	w := quarry()
	r := newRig(t, w, feetAt(2, 1, 2))
	r.give(0, idWoodenPick, 1)
	r.give(1, idStonePick, 1)
	var held []int
	r.onTick = func() {
		if r.breaking != nil {
			held = append(held, r.held)
		}
	}
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 2, 0)
	if st, err := r.run(m, 1500); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if len(held) == 0 {
		t.Fatal("nothing was broken")
	}
	for _, h := range held {
		if h != 1 {
			t.Fatalf("broke with slot %d held, want the stone pickaxe in 1", h)
		}
	}
}

func TestRequireNamesTheFirstMissingItem(t *testing.T) {
	snap := func(items map[string]int) func() belief.Snapshot {
		return func() belief.Snapshot {
			var s belief.Snapshot
			i := 0
			for n, c := range items {
				s.Inventory = append(s.Inventory, belief.Item{Slot: i, Name: n, Count: c})
				i++
			}
			return s
		}
	}
	want := map[string]int{"obsidian": 4, "diamond": 2, "book": 1}
	cases := []struct {
		name string
		have map[string]int
		err  string
	}{
		{"all there", map[string]int{"obsidian": 4, "diamond": 2, "book": 1}, ""},
		{"more than enough", map[string]int{"obsidian": 9, "diamond": 9, "book": 9}, ""},
		{"one diamond short", map[string]int{"obsidian": 4, "diamond": 1, "book": 1}, "no diamond in inventory"},
		{"first of several, sorted", map[string]int{}, "no book in inventory"},
	}
	for _, c := range cases {
		s := NewRequire(snap(c.have), want)
		readyErr := s.Ready(snap(c.have)())
		st, stepErr := s.Step(t.Context(), nil)
		for _, err := range []error{readyErr, stepErr} {
			if (err == nil) != (c.err == "") || (err != nil && err.Error() != c.err) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
			}
		}
		if wantSt := map[bool]Status{true: Done, false: Failed}[c.err == ""]; st != wantSt {
			t.Errorf("%s: status %v, want %v", c.name, st, wantSt)
		}
	}
}
