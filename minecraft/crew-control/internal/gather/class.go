// Package gather measures what a unit picks up while it gathers: the growth
// of its inventory by item class. The item-to-class table is this file and
// nothing else decides a class, so a name a unit sends can never become a
// label of its own.
package gather

import "strings"

// The item classes, a closed list. A name outside the table is Other.
const (
	Wood  = "wood"
	Stone = "stone"
	Ore   = "ore"
	Dirt  = "dirt"
	Crop  = "crop"
	Food  = "food"
	Other = "other"
)

// Classes lists every class, in the order the README and the series use.
var Classes = []string{Wood, Stone, Ore, Dirt, Crop, Food, Other}

// exact maps whole item names, with no namespace, to a class.
var exact = map[string]string{
	// wood: the legacy block names a Bedrock server still sends for logs.
	"log": Wood, "log2": Wood, "stick": Wood, "bamboo_block": Wood,
	// stone
	"stone": Stone, "cobblestone": Stone, "cobbled_deepslate": Stone, "deepslate": Stone,
	"andesite": Stone, "diorite": Stone, "granite": Stone, "tuff": Stone, "calcite": Stone,
	"blackstone": Stone, "basalt": Stone, "netherrack": Stone, "end_stone": Stone,
	"sandstone": Stone, "red_sandstone": Stone, "obsidian": Stone, "dripstone_block": Stone,
	"mossy_cobblestone": Stone, "smooth_basalt": Stone,
	// ore: what mining a vein drops, before any smelting
	"coal": Ore, "diamond": Ore, "emerald": Ore, "lapis_lazuli": Ore, "redstone": Ore,
	"quartz": Ore, "ancient_debris": Ore, "amethyst_shard": Ore, "netherite_scrap": Ore,
	// dirt: loose ground
	"dirt": Dirt, "grass_block": Dirt, "coarse_dirt": Dirt, "rooted_dirt": Dirt, "podzol": Dirt,
	"mycelium": Dirt, "mud": Dirt, "sand": Dirt, "red_sand": Dirt, "gravel": Dirt, "clay": Dirt,
	"clay_ball": Dirt, "soul_sand": Dirt, "soul_soil": Dirt,
	// crop: harvested plants, raw. A carrot or a potato is a crop until it is cooked.
	"wheat": Crop, "wheat_seeds": Crop, "carrot": Crop, "potato": Crop, "poisonous_potato": Crop,
	"beetroot": Crop, "beetroot_seeds": Crop, "melon_slice": Crop, "melon_block": Crop,
	"melon": Crop, "pumpkin": Crop, "melon_seeds": Crop, "pumpkin_seeds": Crop, "reeds": Crop,
	"sugar_cane": Crop, "cactus": Crop, "bamboo": Crop, "nether_wart": Crop, "sweet_berries": Crop,
	"glow_berries": Crop, "cocoa_beans": Crop, "kelp": Crop, "hay_block": Crop, "torchflower_seeds": Crop,
	// food: eaten as it is, or cooked
	"apple": Food, "golden_apple": Food, "bread": Food, "cookie": Food, "pumpkin_pie": Food,
	"cake": Food, "beef": Food, "porkchop": Food, "chicken": Food, "mutton": Food, "rabbit": Food,
	"cod": Food, "salmon": Food, "tropical_fish": Food, "pufferfish": Food, "rotten_flesh": Food,
	"baked_potato": Food, "beetroot_soup": Food, "mushroom_stew": Food, "rabbit_stew": Food,
	"dried_kelp": Food, "honey_bottle": Food, "chorus_fruit": Food, "spider_eye": Food,
}

// Classify returns the class of an item name. The namespace is dropped, so
// "minecraft:oak_log" and "oak_log" are one item.
func Classify(item string) string {
	name := strings.TrimPrefix(item, "minecraft:")
	if c, ok := exact[name]; ok {
		return c
	}
	switch {
	case hasAnySuffix(name, "_log", "_wood", "_stem", "_hyphae", "_planks"):
		return Wood
	case hasAnySuffix(name, "_ore") || strings.HasPrefix(name, "raw_"):
		return Ore
	case strings.HasPrefix(name, "cooked_"):
		return Food
	}
	return Other
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}
