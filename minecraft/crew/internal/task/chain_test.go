package task

import (
	"slices"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func snap(items map[string]int) belief.Snapshot {
	var s belief.Snapshot
	slot := 0
	names := make([]string, 0, len(items))
	for n := range items {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		s.Inventory = append(s.Inventory, belief.Item{Slot: slot, Name: n, Count: items[n]})
		slot++
	}
	return s
}

// Every recipe is reachable in craftOrder, and an item always comes before
// the items it is made from, or a demand would be settled too early.
func TestCraftOrderPutsConsumersFirst(t *testing.T) {
	at := map[string]int{}
	for i, item := range craftOrder {
		if _, dup := at[item]; dup {
			t.Fatalf("%s listed twice", item)
		}
		at[item] = i
	}
	for item, r := range chainRecipes {
		i, ok := at[item]
		if !ok {
			t.Errorf("%s has a recipe but is not in craftOrder", item)
			continue
		}
		for _, in := range r.in {
			if j, ok := at[in.item]; ok && j <= i {
				t.Errorf("%s is made from %s, which is settled before it", item, in.item)
			}
		}
	}
	if len(craftOrder) != len(chainRecipes) {
		t.Errorf("craftOrder has %d items, chainRecipes %d", len(craftOrder), len(chainRecipes))
	}
}

func TestDemandFor(t *testing.T) {
	cases := []struct {
		name    string
		targets map[string]int
		held    map[string]int
		logs    int
		cobble  int
		planks  int
		sticks  int
	}{
		{"table from nothing", map[string]int{"crafting_table": 1}, nil, 1, 0, 4, 0},
		{"chest takes two logs", map[string]int{"chest": 1}, nil, 2, 0, 8, 0},
		{"wooden pickaxe: 3 planks, one stick batch", map[string]int{"wooden_pickaxe": 1}, nil, 2, 0, 5, 2},
		{"a sword's handle is one stick", map[string]int{"wooden_sword": 1}, nil, 1, 0, 4, 1},
		{"planks held pay before logs", map[string]int{"crafting_table": 1}, map[string]int{"oak_planks": 4}, 0, 0, 4, 0},
		{"any planks pay", map[string]int{"crafting_table": 1}, map[string]int{"birch_planks": 2, "oak_planks": 2}, 0, 0, 4, 0},
		{"sticks held pay", map[string]int{"stone_pickaxe": 1}, map[string]int{"stick": 2}, 0, 3, 0, 2},
		{"stone pickaxe", map[string]int{"stone_pickaxe": 1}, nil, 1, 3, 2, 2},
		{"furnace is cobblestone only", map[string]int{"furnace": 1}, nil, 0, 8, 0, 0},
		{"a held tool needs nothing", map[string]int{"wooden_axe": 1}, map[string]int{"wooden_axe": 1}, 0, 0, 0, 0},
		{"any log pays", map[string]int{"crafting_table": 1}, map[string]int{"birch_log": 1}, 1, 0, 4, 0},
		{"sticks round up to batches of four", map[string]int{"stick": 5}, nil, 1, 0, 4, 5},
		{
			"shared planks add up before rounding",
			map[string]int{"crafting_table": 1, "chest": 1}, nil, 3, 0, 12, 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DemandFor(c.targets, Inventory(c.held))
			if d.Raw[logsPool] != c.logs || d.Raw[itemCobblestone] != c.cobble {
				t.Errorf("raw = %v, want %d logs and %d cobblestone", d.Raw, c.logs, c.cobble)
			}
			if d.Gross[planksPool] != c.planks || d.Gross[itemStick] != c.sticks {
				t.Errorf("gross = %v, want %d planks and %d sticks", d.Gross, c.planks, c.sticks)
			}
		})
	}
}

// Everything the bootstrap sets out to make, from logs alone, costs the
// logs the docs promise: 4+8 for table and chest, 8 for wooden heads, 6 for
// ten sticks, and eight cobblestone each for the stone heads and furnace.
func TestBootstrapDemandFromNothing(t *testing.T) {
	d := DemandFor(bootstrapTargets(false, false, true), Inventory{})
	if d.Gross[planksPool] != 26 || d.Raw[logsPool] != 7 {
		t.Errorf("planks %d logs %d, want 26 and 7", d.Gross[planksPool], d.Raw[logsPool])
	}
	if d.Gross[itemStick] != 10 {
		t.Errorf("sticks = %d, want 10", d.Gross[itemStick])
	}
	if d.Raw[itemCobblestone] != 16 {
		t.Errorf("cobblestone = %d, want 16", d.Raw[itemCobblestone])
	}
}

func TestPlankCrafts(t *testing.T) {
	cases := map[string]struct {
		held   map[string]int
		felled int
		need   int
		want   []plankCraft
	}{
		"nothing held, oak felled":    {nil, 7, 26, []plankCraft{{"oak_planks", 26}}},
		"nothing needed":              {map[string]int{"oak_planks": 40}, 0, 0, nil},
		"held oak logs only":          {map[string]int{"oak_log": 7}, 0, 26, []plankCraft{{"oak_planks", 26}}},
		"held planks count in total":  {map[string]int{"oak_log": 7, "oak_planks": 4}, 0, 22, []plankCraft{{"oak_planks", 26}}},
		"birch held, birch enough":    {map[string]int{"birch_log": 7}, 0, 26, []plankCraft{{"birch_planks", 26}}},
		"stray birch log, oak felled": {map[string]int{"birch_log": 1}, 6, 26, []plankCraft{{"oak_planks", 24}, {"birch_planks", 2}}},
		"the most plentiful first":    {map[string]int{"birch_log": 5, "oak_log": 2}, 0, 26, []plankCraft{{"birch_planks", 20}, {"oak_planks", 6}}},
		"a tie goes to the first":     {map[string]int{"spruce_log": 3, "birch_log": 3}, 0, 8, []plankCraft{{"birch_planks", 8}}},
		"a stem makes planks":         {map[string]int{"crimson_stem": 4}, 0, 8, []plankCraft{{"crimson_planks", 8}}},
		"stripped wood is its wood":   {map[string]int{"stripped_oak_log": 2}, 0, 8, []plankCraft{{"oak_planks", 8}}},
		"other items are ignored":     {map[string]int{"cobblestone": 30, "jungle_log": 1}, 0, 4, []plankCraft{{"jungle_planks", 4}}},
		"planks of another wood held": {map[string]int{"birch_planks": 5}, 7, 21, []plankCraft{{"oak_planks", 21}}},
	}
	for name, c := range cases {
		got := plankCrafts(Inventory(c.held), c.felled, c.need)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func TestBestCraftable(t *testing.T) {
	cases := []struct {
		name string
		held map[string]int
		kind string
		want string
		ok   bool
	}{
		{"nothing", nil, "pickaxe", "", false},
		{"logs alone make a wooden pickaxe", map[string]int{"oak_log": 2}, "pickaxe", "wooden_pickaxe", true},
		{"one log is not enough for a pickaxe", map[string]int{"oak_log": 1}, "pickaxe", "", false},
		{"one log makes a sword", map[string]int{"oak_log": 1}, "sword", "wooden_sword", true},
		{"planks and sticks", map[string]int{"oak_planks": 3, "stick": 2}, "axe", "wooden_axe", true},
		{"cobblestone and a log reach stone", map[string]int{"cobblestone": 3, "oak_log": 1}, "pickaxe", "stone_pickaxe", true},
		{"stone needs its handle", map[string]int{"cobblestone": 3}, "pickaxe", "", false},
		{"stone with sticks held", map[string]int{"cobblestone": 3, "stick": 2}, "axe", "stone_axe", true},
		{"two cobblestone make a sword, not an axe", map[string]int{"cobblestone": 2, "stick": 2}, "axe", "", false},
		{"two cobblestone make a sword", map[string]int{"cobblestone": 2, "stick": 1}, "sword", "stone_sword", true},
		{"iron beats stone", map[string]int{"iron_ingot": 3, "cobblestone": 9, "stick": 2}, "pickaxe", "iron_pickaxe", true},
		{"a held tool counts", map[string]int{"wooden_axe": 1}, "axe", "wooden_axe", true},
		{"other kinds do not", map[string]int{"wooden_axe": 1}, "pickaxe", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := BestCraftable(snap(c.held), c.kind)
			if got != c.want || ok != c.ok {
				t.Errorf("got %q, %v; want %q, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestNextUpgrade(t *testing.T) {
	cases := []struct {
		name string
		held map[string]int
		kind string
		want string
		ok   bool
	}{
		{"nothing held, logs", map[string]int{"oak_log": 2}, "pickaxe", "wooden_pickaxe", true},
		{"wooden held, stone affordable", map[string]int{"wooden_pickaxe": 1, "cobblestone": 3, "stick": 2}, "pickaxe", "stone_pickaxe", true},
		{"wooden held, only wooden affordable", map[string]int{"wooden_pickaxe": 1, "oak_log": 4}, "pickaxe", "", false},
		{"stone held, cobblestone spare", map[string]int{"stone_axe": 1, "cobblestone": 9, "stick": 4}, "axe", "", false},
		{"stone held, iron affordable", map[string]int{"stone_axe": 1, "iron_ingot": 3, "stick": 2}, "axe", "iron_axe", true},
		{"a better tool is not downgraded", map[string]int{"stone_sword": 1, "oak_log": 3}, "sword", "", false},
		{"jumps over a tier", map[string]int{"iron_ingot": 2, "stick": 1}, "sword", "iron_sword", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := NextUpgrade(snap(c.held), c.kind)
			if got != c.want || ok != c.ok {
				t.Errorf("got %q, %v; want %q, %v", got, ok, c.want, c.ok)
			}
		})
	}
}
