package skill

import (
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// OreKind is an ore the miner knows how to take: the blocks that hold it, the
// item it drops and the lowest pickaxe tier that harvests it. Breaking it with
// a lesser pickaxe removes the block and drops nothing.
type OreKind struct {
	Name   string
	Item   string
	Blocks []string
	Tier   int
}

// The ores of the progression ladder. Deepslate variants are separate blocks
// that drop the same item.
var (
	OreCoal    = OreKind{Name: "coal", Item: "coal", Blocks: []string{"coal_ore", "deepslate_coal_ore"}, Tier: 0}
	OreIron    = OreKind{Name: "iron", Item: "raw_iron", Blocks: []string{"iron_ore", "deepslate_iron_ore"}, Tier: 1}
	OreDiamond = OreKind{Name: "diamond", Item: "diamond", Blocks: []string{"diamond_ore", "deepslate_diamond_ore"}, Tier: 2}
)

// OreByName finds an ore kind by its short name: coal, iron or diamond.
func OreByName(name string) (OreKind, bool) {
	for _, k := range []OreKind{OreCoal, OreIron, OreDiamond} {
		if k.Name == name {
			return k, true
		}
	}
	return OreKind{}, false
}

// OreNames lists the short names OreByName accepts.
func OreNames() []string { return []string{OreCoal.Name, OreIron.Name, OreDiamond.Name} }

// pickaxeTier is the harvest level of a pickaxe: 0 wooden or golden, 1 stone
// or copper, 2 iron, 3 diamond or netherite. An ore needs a pickaxe at least
// as high as its Tier, so iron needs a stone pickaxe and diamond an iron one.
func pickaxeTier(name string) (int, bool) {
	material, ok := strings.CutSuffix(name, "_pickaxe")
	if !ok {
		return 0, false
	}
	switch material {
	case "wooden", "golden":
		return 0, true
	case "stone", "copper":
		return 1, true
	case "iron":
		return 2, true
	case "diamond", "netherite":
		return 3, true
	}
	return 0, false
}

// bestPickaxe finds the highest-tier pickaxe on the hotbar, the only place the
// server lets the unit select from. A pickaxe of a material it does not know
// ranks with the wooden one rather than being left out, so a modded tool still
// digs stone.
func bestPickaxe(s belief.Snapshot) (belief.Item, int, bool) {
	var best belief.Item
	bestTier, found := -1, false
	for _, it := range s.Inventory {
		if it.Slot >= hotbarSlots || !hasSuffix(it.Name, "pickaxe") {
			continue
		}
		tier, _ := pickaxeTier(it.Name)
		if tier > bestTier {
			best, bestTier, found = it, tier, true
		}
	}
	return best, bestTier, found
}
