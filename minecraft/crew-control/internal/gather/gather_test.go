package gather

import (
	"slices"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

func inv(pairs ...any) []telemetry.LiveItem {
	var out []telemetry.LiveItem
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, telemetry.LiveItem{Slot: i / 2, Name: pairs[i].(string), Count: pairs[i+1].(int)})
	}
	return out
}

func TestClassifyMapsNamesToTheClosedClasses(t *testing.T) {
	for item, want := range map[string]string{
		"oak_log": Wood, "minecraft:oak_log": Wood, "log": Wood, "log2": Wood, "stripped_birch_log": Wood,
		"crimson_stem": Wood, "spruce_planks": Wood, "stick": Wood,
		"cobblestone": Stone, "stone": Stone, "deepslate": Stone, "andesite": Stone,
		"iron_ore": Ore, "deepslate_diamond_ore": Ore, "raw_iron": Ore, "coal": Ore, "diamond": Ore, "lapis_lazuli": Ore,
		"dirt": Dirt, "sand": Dirt, "gravel": Dirt, "grass_block": Dirt,
		"wheat": Crop, "carrot": Crop, "potato": Crop, "sugar_cane": Crop, "beetroot_seeds": Crop,
		"bread": Food, "cooked_beef": Food, "baked_potato": Food, "apple": Food,
		"iron_pickaxe": Other, "totem_of_undying": Other, "": Other, "OAK_LOG": Other,
		"a_plank_of_steve_at_1_2_3": Other,
	} {
		if got := Classify(item); got != want {
			t.Errorf("Classify(%q) = %q, want %q", item, got, want)
		}
	}
}

func TestEveryClassifiedNameLandsInTheClosedList(t *testing.T) {
	for name, c := range exact {
		if !slices.Contains(Classes, c) || c == Other {
			t.Errorf("%q maps to %q, outside the listed classes", name, c)
		}
	}
	if Classes[len(Classes)-1] != Other {
		t.Error("other must stay last, where the series and the docs put it")
	}
}

func TestGainCountsPositiveGrowthByClassWhileGathering(t *testing.T) {
	tr := NewTracker()
	tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 3, "iron_axe", 1))
	g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 7, "iron_axe", 1, "stick", 2, "cobblestone", 1))
	if g.ByClass[Wood] != 6 || g.ByClass[Stone] != 1 || len(g.ByClass) != 2 || g.Total() != 7 {
		t.Fatalf("by class = %v", g.ByClass)
	}
	if g.Items["oak_log"] != 4 || g.Items["stick"] != 2 || g.Items["cobblestone"] != 1 {
		t.Errorf("items = %v", g.Items)
	}
	// Nothing new, and a loss, are both zero: deposits are not negative yield.
	if g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 7, "iron_axe", 1, "stick", 2, "cobblestone", 1)); g.Total() != 0 {
		t.Errorf("an unchanged inventory gained %v", g.ByClass)
	}
	if g := tr.Observe("u", true, "gather_and_deposit", inv("iron_axe", 1)); g.Total() != 0 {
		t.Errorf("a deposit gained %v", g.ByClass)
	}
}

func TestStacksSplitAcrossSlotsAreOneTotal(t *testing.T) {
	tr := NewTracker()
	tr.Observe("u", true, "mine_shaft", inv("cobblestone", 60))
	g := tr.Observe("u", true, "mine_shaft", inv("cobblestone", 64, "cobblestone", 3))
	if g.ByClass[Stone] != 7 {
		t.Errorf("stone = %d, want 7 across both stacks", g.ByClass[Stone])
	}
}

func TestAWithdrawalOrCraftIsNeverYieldAndNeverCountedLater(t *testing.T) {
	tr := NewTracker()
	tr.Observe("u", true, "haul", inv("oak_log", 1))
	// A withdrawal fills the inventory: not a gathering task, so no gain...
	for _, tpl := range []string{"withdraw", "haul", "craft", "deposit", "goto", ""} {
		if g := tr.Observe("u", true, tpl, inv("oak_log", 1, "cobblestone", 40)); g.Total() != 0 {
			t.Errorf("%q gained %v", tpl, g.ByClass)
		}
	}
	// ...and what it brought is in the baseline, so the next gather poll does
	// not see it arrive.
	if g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 1, "cobblestone", 40)); g.Total() != 0 {
		t.Errorf("a withdrawn stack was credited once the unit started gathering: %v", g.ByClass)
	}
	if g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 2, "cobblestone", 40)); g.ByClass[Wood] != 1 || g.Total() != 1 {
		t.Errorf("real pickup after it = %v", g.ByClass)
	}
}

func TestAFirstSightAnOfflineGapAndADeathOnlySetABaseline(t *testing.T) {
	tr := NewTracker()
	// The first sight after crew-control starts: the whole inventory is not yield.
	if g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 500)); g.Total() != 0 {
		t.Fatalf("first sight gained %v", g.ByClass)
	}
	// Offline, then back with more: the gap is unknown.
	tr.Observe("u", false, "", nil)
	if g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 900)); g.Total() != 0 {
		t.Fatalf("a return from offline gained %v", g.ByClass)
	}
	if g := tr.Observe("u", true, "gather_and_deposit", inv("oak_log", 901)); g.Total() != 1 {
		t.Fatalf("growth after the new baseline = %v", g.ByClass)
	}
	// A death empties the inventory and respawn can hand a kit back.
	tr.Observe("u", false, "", nil)
	tr.Observe("u", true, "gather_and_deposit", inv("iron_pickaxe", 1))
	if g := tr.Observe("u", true, "gather_and_deposit", inv("iron_pickaxe", 1, "bread", 5)); g.ByClass[Food] != 5 {
		t.Errorf("growth after a respawn baseline = %v", g.ByClass)
	}
}

func TestUnitsAreTrackedApart(t *testing.T) {
	tr := NewTracker()
	tr.Observe("a", true, "gather_and_deposit", inv("dirt", 1))
	tr.Observe("b", true, "gather_and_deposit", inv("dirt", 100))
	if g := tr.Observe("a", true, "gather_and_deposit", inv("dirt", 4)); g.ByClass[Dirt] != 3 {
		t.Errorf("a gained %v", g.ByClass)
	}
}

func TestOnePollCannotCreditMoreThanAStackOfAnItem(t *testing.T) {
	tr := NewTracker()
	tr.Observe("u", true, "gather_and_deposit", inv("cobblestone", 1))
	g := tr.Observe("u", true, "gather_and_deposit", inv("cobblestone", 1000))
	if g.ByClass[Stone] != maxPerItem {
		t.Errorf("stone = %d, want the cap %d", g.ByClass[Stone], maxPerItem)
	}
}

func TestOnlyGatheringTemplatesCount(t *testing.T) {
	for tpl, want := range map[string]bool{"gather_and_deposit": true, "mine_shaft": true, "haul": false, "withdraw": false, "craft": false, "": false} {
		if Gathering(tpl) != want {
			t.Errorf("Gathering(%q) = %v", tpl, !want)
		}
	}
}

func TestAnUnreadInventoryDoesNotMakeTheNextReadAGain(t *testing.T) {
	tr := NewTracker()
	tr.Observe("crew-01", true, "mine_shaft", inv("cobblestone", 40, "oak_log", 30))
	// One poll that could not read the inventory, then a full read: nothing
	// was picked up in between, so nothing may be counted.
	if g := tr.Observe("crew-01", true, "mine_shaft", nil); g.Total() != 0 {
		t.Fatalf("an unread inventory gained %v", g)
	}
	if g := tr.Observe("crew-01", true, "mine_shaft", inv("cobblestone", 40, "oak_log", 30)); g.Total() != 0 {
		t.Fatalf("a full read after an unread one gained %v", g)
	}
	// Growth after the new baseline still counts.
	if g := tr.Observe("crew-01", true, "mine_shaft", inv("cobblestone", 45, "oak_log", 30)); g.Total() != 5 {
		t.Fatalf("real growth counted %v, want 5", g)
	}
}
