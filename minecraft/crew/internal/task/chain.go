package task

import (
	"slices"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/craft"
)

// The crafting chain is a fixed table of what each step of the wood-to-stone
// ladder costs. The server's recipe book decides how a craft is laid out and
// whether it needs a table; this table exists so the bootstrap can know,
// before it starts, how many logs and how much cobblestone to fetch, and so
// the gear rung can ask which tool the unit could make from what it holds.

const (
	// planksPool and logsPool stand for every wood species: a recipe takes
	// any planks, and any log makes planks of its own species.
	planksPool = "planks"
	logsPool   = "log"

	itemStick       = "stick"
	itemTable       = "crafting_table"
	itemChest       = "chest"
	itemFurnace     = "furnace"
	itemCobblestone = "cobblestone"
	itemIronIngot   = "iron_ingot"
)

type chainIngredient struct {
	item string
	n    int
}

// chainRecipe is what one craft consumes and how many of the item it yields.
type chainRecipe struct {
	out int
	in  []chainIngredient
}

var chainRecipes = map[string]chainRecipe{
	planksPool:       {4, []chainIngredient{{logsPool, 1}}},
	itemStick:        {4, []chainIngredient{{planksPool, 2}}},
	itemTable:        {1, []chainIngredient{{planksPool, 4}}},
	itemChest:        {1, []chainIngredient{{planksPool, 8}}},
	itemFurnace:      {1, []chainIngredient{{itemCobblestone, 8}}},
	"wooden_pickaxe": toolRecipe(planksPool, 3),
	"wooden_axe":     toolRecipe(planksPool, 3),
	"wooden_sword":   toolRecipe(planksPool, 2),
	"stone_pickaxe":  toolRecipe(itemCobblestone, 3),
	"stone_axe":      toolRecipe(itemCobblestone, 3),
	"stone_sword":    toolRecipe(itemCobblestone, 2),
	"iron_pickaxe":   toolRecipe(itemIronIngot, 3),
	"iron_axe":       toolRecipe(itemIronIngot, 3),
	"iron_sword":     toolRecipe(itemIronIngot, 2),
}

// toolRecipe is a tool: the head material and a handle of sticks, one stick
// less for a sword.
func toolRecipe(material string, n int) chainRecipe {
	sticks := 2
	if n == 2 {
		sticks = 1
	}
	return chainRecipe{1, []chainIngredient{{material, n}, {itemStick, sticks}}}
}

// craftOrder lists the craftable items consumers first: an item's demand is
// settled only after everything that uses it has added to it.
var craftOrder = []string{
	"iron_pickaxe", "iron_axe", "iron_sword",
	"stone_pickaxe", "stone_axe", "stone_sword",
	"wooden_pickaxe", "wooden_axe", "wooden_sword",
	itemFurnace, itemChest, itemTable, itemStick, planksPool,
}

// ToolTiers is the upgrade ladder from the worst tier to the best.
var ToolTiers = []string{"wooden", "stone", "iron"}

// ToolKinds are the tools the ladder covers.
var ToolKinds = []string{"pickaxe", "axe", "sword"}

// Inventory counts what a unit holds by item name.
type Inventory map[string]int

// InventoryOf totals a snapshot's stacks.
func InventoryOf(s belief.Snapshot) Inventory {
	inv := Inventory{}
	for _, it := range s.Inventory {
		inv[it.Name] += it.Count
	}
	return inv
}

// held counts an item, a pool counting every item it stands for.
func (inv Inventory) held(item string) int {
	var tag craft.Tag
	switch item {
	case planksPool:
		tag = "planks"
	case logsPool:
		tag = "logs"
	default:
		return inv[item]
	}
	n := 0
	for name, c := range inv {
		if tag.Matches(name) {
			n += c
		}
	}
	return n
}

// Demand is what a set of targets costs from the inventory it starts in.
// Gross counts every item, craftable or raw, the targets need in total
// (including what is already held); Raw is the part of it that no craft
// makes: logs, cobblestone and iron ingots, again in total.
type Demand struct {
	Gross map[string]int
	Raw   map[string]int
}

// DemandFor works out what must be made and gathered to hold every target,
// crediting what the inventory already has at each step. Crafts come in
// whole batches, so a stick order rounds up to a multiple of four.
func DemandFor(targets map[string]int, inv Inventory) Demand {
	gross := map[string]int{}
	for item, n := range targets {
		gross[item] = n
	}
	for _, item := range craftOrder {
		need := gross[item] - inv.held(item)
		if need <= 0 {
			continue
		}
		r := chainRecipes[item]
		crafts := (need + r.out - 1) / r.out
		for _, in := range r.in {
			gross[in.item] += in.n * crafts
		}
	}
	raw := map[string]int{}
	for _, leaf := range []string{logsPool, itemCobblestone, itemIronIngot} {
		if gross[leaf] > 0 {
			raw[leaf] = gross[leaf]
		}
	}
	return Demand{Gross: gross, Raw: raw}
}

// Short is how much of each raw material the inventory lacks for the demand.
func (d Demand) Short(inv Inventory) map[string]int {
	short := map[string]int{}
	for leaf, n := range d.Raw {
		if lack := n - inv.held(leaf); lack > 0 {
			short[leaf] = lack
		}
	}
	return short
}

// toolItem names a tool of a tier.
func toolItem(tier, kind string) string { return tier + "_" + kind }

// canMake says whether the inventory pays for one of the item, from raw
// materials if it must; an item already held counts. It does not ask whether
// a table stands within reach.
func canMake(inv Inventory, item string) bool {
	d := DemandFor(map[string]int{item: 1}, inv)
	return len(d.Short(inv)) == 0
}

// heldTier is the index in ToolTiers of the best tool of the kind the unit
// holds, or -1.
func heldTier(inv Inventory, kind string) int {
	best := -1
	for i, tier := range ToolTiers {
		if inv[toolItem(tier, kind)] > 0 {
			best = i
		}
	}
	return best
}

// BestCraftable is the best tool of the kind the unit has or could make now:
// a held tool counts as made. ok is false when it has none and can make none.
func BestCraftable(s belief.Snapshot, kind string) (item string, ok bool) {
	inv := InventoryOf(s)
	for i := len(ToolTiers) - 1; i >= 0; i-- {
		if it := toolItem(ToolTiers[i], kind); canMake(inv, it) {
			return it, true
		}
	}
	return "", false
}

// NextUpgrade is the tool of the kind worth crafting now: the best tier the
// unit could make that is better than the best it holds. ok is false when
// the unit already holds the best it can make.
func NextUpgrade(s belief.Snapshot, kind string) (item string, ok bool) {
	inv := InventoryOf(s)
	have := heldTier(inv, kind)
	for i := len(ToolTiers) - 1; i > have; i-- {
		if it := toolItem(ToolTiers[i], kind); canMake(inv, it) {
			return it, true
		}
	}
	return "", false
}

// plankItem picks the planks to craft: those of the log the unit holds most
// of, so a unit holding birch logs does not go looking for oak. Ties go to
// the name that sorts first, and a unit with no logs makes oak.
func plankItem(inv Inventory) string {
	best, bestN := "oak", 0
	names := make([]string, 0, len(inv))
	for name := range inv {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !craft.Tag("logs").Matches(name) || inv[name] <= bestN {
			continue
		}
		species := name
		for _, suffix := range []string{"_log", "_wood", "_stem"} {
			species = strings.TrimSuffix(species, suffix)
		}
		best, bestN = species, inv[name]
	}
	return best + "_planks"
}
