package inventory

import (
	"reflect"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func it(slot int, name string, n int) belief.Item {
	return belief.Item{Slot: slot, Name: name, Count: n}
}

func worn(slot int, name string, pct int) belief.Item {
	i := it(slot, name, 1)
	i.DurabilityPct = &pct
	return i
}

func TestClassify(t *testing.T) {
	cases := map[string]class{
		"rotten_flesh": junk, "spider_eye": junk,
		"dirt": filler, "cobblestone": filler, "andesite": filler,
		"oak_planks": build, "birch_log": build, "stone_bricks": build,
		"bread": food, "cooked_beef": food,
		"iron_pickaxe": tool, "wooden_axe": tool, "stone_sword": tool, "diamond_shovel": tool,
		"iron_helmet": armour, "leather_boots": armour,
		"oak_sapling": protected, "torch": protected, "white_bed": protected, "shield": protected,
		"diamond": other, "coal": other, "raw_iron": other, "iron_ingot": other,
	}
	for name, want := range cases {
		if got := classify(name); got != want {
			t.Errorf("classify(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestPickaxeIsNotAnAxe(t *testing.T) {
	if k := ToolKind("iron_pickaxe"); k != "pickaxe" {
		t.Errorf("ToolKind(iron_pickaxe) = %q", k)
	}
	if k := ToolKind("iron_axe"); k != "axe" {
		t.Errorf("ToolKind(iron_axe) = %q", k)
	}
}

func TestPlan(t *testing.T) {
	pol := Policy{FullSlots: 4, KeepFood: 10, KeepBlocks: 64, KeepFiller: 32, SpareTools: 1}
	cases := []struct {
		name     string
		inv      []belief.Item
		drops    []Move
		deposits []Move
	}{
		{name: "empty", inv: nil},
		{
			name:  "junk is dropped whole",
			inv:   []belief.Item{it(0, "rotten_flesh", 12), it(1, "spider_eye", 3), it(2, "poisonous_potato", 1)},
			drops: []Move{{"poisonous_potato", 1}, {"rotten_flesh", 12}, {"spider_eye", 3}},
		},
		{
			name:  "filler beyond the reserve is dropped across stacks",
			inv:   []belief.Item{it(3, "cobblestone", 64), it(4, "cobblestone", 20), it(5, "dirt", 30)},
			drops: []Move{{"cobblestone", 52}},
		},
		{
			name:     "building blocks beyond the reserve are stored, not dropped",
			inv:      []belief.Item{it(0, "oak_planks", 64), it(1, "oak_planks", 64), it(2, "oak_planks", 30)},
			deposits: []Move{{"oak_planks", 94}},
		},
		{
			name:     "loot is stored whole, protected essentials stay",
			inv:      []belief.Item{it(0, "oak_sapling", 8), it(1, "torch", 40), it(2, "diamond", 5), it(3, "coal", 30), it(4, "crafting_table", 1)},
			deposits: []Move{{"coal", 30}, {"diamond", 5}},
		},
		{
			name:     "food beyond the reserve, poorest first",
			inv:      []belief.Item{it(0, "cooked_beef", 8), it(1, "bread", 10), it(2, "carrot", 6)},
			deposits: []Move{{"carrot", 6}, {"bread", 8}},
		},
		{
			name: "food within the reserve stays",
			inv:  []belief.Item{it(0, "cooked_beef", 4), it(1, "bread", 6)},
		},
		{
			name:     "spare tools: best plus one spare stay",
			inv:      []belief.Item{it(0, "wooden_pickaxe", 1), it(1, "iron_pickaxe", 1), it(2, "stone_pickaxe", 1), it(3, "diamond_pickaxe", 1)},
			deposits: []Move{{"stone_pickaxe", 1}, {"wooden_pickaxe", 1}},
		},
		{
			name:     "armour: only the best carried piece stays",
			inv:      []belief.Item{it(0, "iron_helmet", 1), it(1, "leather_helmet", 1), it(2, "iron_boots", 1)},
			deposits: []Move{{"leather_helmet", 1}},
		},
		{
			name:     "equal tools: the more worn one is the spare that goes",
			inv:      []belief.Item{worn(0, "iron_axe", 20), worn(1, "iron_axe", 90), worn(2, "iron_axe", 55)},
			deposits: []Move{{"iron_axe", 1}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := pol.Plan(c.inv)
			if !reflect.DeepEqual(p.Drops, c.drops) {
				t.Errorf("drops = %v, want %v", p.Drops, c.drops)
			}
			if !reflect.DeepEqual(p.Deposits, c.deposits) {
				t.Errorf("deposits = %v, want %v", p.Deposits, c.deposits)
			}
		})
	}
}

func TestPlanOrdersDepositsByFreedSlots(t *testing.T) {
	pol := Policy{FullSlots: 4}
	inv := []belief.Item{it(0, "coal", 3), it(1, "iron_ingot", 64), it(2, "iron_ingot", 64), it(3, "diamond", 1)}
	p := pol.Plan(inv)
	want := []Move{{"iron_ingot", 128}, {"coal", 3}, {"diamond", 1}}
	if !reflect.DeepEqual(p.Deposits, want) {
		t.Errorf("deposits = %v, want %v", p.Deposits, want)
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	pol := Default()
	inv := []belief.Item{it(5, "coal", 3), it(1, "raw_iron", 9), it(7, "gold_ingot", 2), it(2, "dirt", 90), it(9, "rotten_flesh", 4), it(30, "bread", 40)}
	first := pol.Plan(inv)
	for i := 0; i < 20; i++ {
		if got := pol.Plan(inv); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d = %+v, want %+v", i, got, first)
		}
	}
}

func TestFree(t *testing.T) {
	pol := Policy{FullSlots: 4}
	var inv []belief.Item
	for i := 0; i < 32; i++ {
		inv = append(inv, it(i, "coal", 1))
	}
	p := pol.Plan(inv)
	if p.Free != 4 || !p.Full(pol) {
		t.Errorf("free = %d full = %v, want 4 and full", p.Free, p.Full(pol))
	}
	if q := pol.Plan(inv[:31]); q.Full(pol) {
		t.Errorf("5 free slots counted as full")
	}
}

func TestBestTool(t *testing.T) {
	inv := []belief.Item{it(0, "wooden_pickaxe", 1), it(12, "diamond_pickaxe", 1), it(3, "iron_pickaxe", 1), it(4, "iron_axe", 1)}
	cases := []struct {
		kind   string
		hotbar bool
		want   string
		wantOK bool
	}{
		{"pickaxe", false, "diamond_pickaxe", true},
		{"pickaxe", true, "iron_pickaxe", true},
		{"axe", false, "iron_axe", true},
		{"shovel", false, "", false},
	}
	for _, c := range cases {
		got, ok := BestTool(inv, c.kind, c.hotbar)
		if ok != c.wantOK || got.Name != c.want {
			t.Errorf("BestTool(%s, hotbar=%v) = %q,%v want %q,%v", c.kind, c.hotbar, got.Name, ok, c.want, c.wantOK)
		}
	}
}

func TestBestToolPrefersTheLessWornOfTwoEqualTools(t *testing.T) {
	got, _ := BestTool([]belief.Item{worn(0, "iron_pickaxe", 10), worn(4, "iron_pickaxe", 80)}, "pickaxe", false)
	if got.Slot != 4 {
		t.Errorf("picked slot %d, want the 80%% one in slot 4", got.Slot)
	}
}

func TestToolFor(t *testing.T) {
	cases := map[string]string{
		"stone": "pickaxe", "iron_ore": "pickaxe", "deepslate": "pickaxe",
		"dirt": "shovel", "gravel": "shovel", "sand": "shovel",
		"oak_log": "axe", "birch_planks": "axe", "crafting_table": "axe",
		"oak_leaves": "hoe", "cobweb": "sword",
		"air": "", "water": "",
	}
	for block, want := range cases {
		if got := ToolFor(block); got != want {
			t.Errorf("ToolFor(%q) = %q, want %q", block, got, want)
		}
	}
}

func TestStacksTakeFromTheHighestSlotsFirst(t *testing.T) {
	inv := []belief.Item{it(2, "cobblestone", 64), it(20, "cobblestone", 10), it(5, "cobblestone", 40), it(1, "dirt", 3)}
	got := Stacks(inv, Move{"cobblestone", 70})
	want := []Take{{inv[1], 10}, {inv[2], 40}, {inv[0], 20}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stacks = %+v, want %+v", got, want)
	}
	if n := len(Stacks(inv, Move{"emerald", 5})); n != 0 {
		t.Errorf("stacks of an absent item = %d", n)
	}
}

func TestTripStopsOnceThereIsRoomAndBoundsTheVisits(t *testing.T) {
	pol := Policy{FullSlots: 2}
	var inv []belief.Item
	for i := 0; i < 36; i++ {
		inv = append(inv, it(i, "loot_"+string(rune('a'+i%26))+string(rune('a'+i/26)), 1))
	}
	p := pol.Plan(inv)
	if got := len(pol.Trip(p)); got != MaxTripMoves {
		t.Errorf("trip = %d moves, want the cap %d", got, MaxTripMoves)
	}
	big := []belief.Item{it(0, "iron_ingot", 64), it(1, "iron_ingot", 64), it(2, "iron_ingot", 64), it(3, "coal", 2)}
	for i := 4; i < 31; i++ {
		big = append(big, it(i, "stick_"+string(rune('a'+i)), 64))
	}
	trip := pol.Trip(pol.Plan(big))
	if len(trip) == 0 || trip[0].Name != "iron_ingot" {
		t.Fatalf("trip = %v, want the three-slot stack first", trip)
	}
	if len(trip) > 3 {
		t.Errorf("trip = %v, want it to stop once 2+8 slots are free", trip)
	}
}
