package atlas

import (
	"strings"
)

// The pack names textures per block but says nothing about shape or tint:
// stairs, slabs, torches and cross-plants are built by the game engine, and
// which blocks take the biome colour is hard-coded there too. These tables
// stand in for that engine knowledge and are matched by block name.

type nameRule struct {
	exact    []string
	suffix   []string
	contains []string
	value    string
}

func (r nameRule) matches(name string) bool {
	for _, e := range r.exact {
		if name == e {
			return true
		}
	}
	for _, s := range r.suffix {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	for _, c := range r.contains {
		if strings.Contains(name, c) {
			return true
		}
	}
	return false
}

// shapeRules are ordered: the first match wins, so exceptions come first.
var shapeRules = []nameRule{
	{exact: []string{"air", "cave_air", "void_air", "barrier", "structure_void", "light_block", "allow", "deny", "border_block"}, value: ModelNone},
	{exact: []string{"water", "flowing_water", "lava", "flowing_lava"}, value: ModelLiquid},
	{suffix: []string{"_double_slab"}, value: ModelCube},
	{suffix: []string{"_slab"}, value: ModelSlab},
	{suffix: []string{"_stairs"}, value: ModelStairs},
	{suffix: []string{"torch"}, value: ModelTorch},
	{suffix: []string{"_carpet", "_pressure_plate"}, exact: []string{"snow_layer", "carpet"}, value: ModelLayer},
	{suffix: []string{"_fence", "_fence_gate"}, exact: []string{"fence", "fence_gate"}, value: ModelFence},
	{suffix: []string{"_wall"}, value: ModelWall},
	{suffix: []string{"_pane"}, exact: []string{"iron_bars", "glass_pane"}, value: ModelPane},
	{suffix: []string{"_door", "_trapdoor"}, exact: []string{"trapdoor"}, value: ModelDoor},
	{
		exact: []string{
			"short_grass", "tall_grass", "tallgrass", "fern", "large_fern", "double_plant", "deadbush", "dead_bush",
			"short_dry_grass", "tall_dry_grass", "seagrass", "kelp", "reeds", "vine", "bamboo_sapling",
			"brown_mushroom", "red_mushroom", "crimson_fungus", "warped_fungus", "crimson_roots", "warped_roots",
			"nether_sprouts", "hanging_roots", "sweet_berry_bush", "wheat", "carrots", "potatoes", "beetroot",
			"yellow_flower", "red_flower", "dandelion", "poppy", "cornflower", "allium", "azure_bluet", "blue_orchid",
			"oxeye_daisy", "lily_of_the_valley", "wither_rose", "torchflower", "pitcher_plant", "rose_bush", "peony",
			"lilac", "sunflower", "spore_blossom", "waterlily", "glow_lichen", "cobweb", "web", "melon_stem", "pumpkin_stem",
		},
		suffix:   []string{"_sapling", "_tulip", "_roots"},
		contains: []string{"_flower"},
		value:    ModelCross,
	},
}

var foliageTintExceptions = []string{"cherry_leaves", "azalea_leaves", "azalea_leaves_flowered", "pale_oak_leaves"}

var tintRules = []nameRule{
	{exact: []string{"water", "flowing_water"}, value: TintWater},
	{exact: []string{"grass", "grass_block", "short_grass", "tall_grass", "tallgrass", "fern", "large_fern", "double_plant"}, value: TintGrass},
	{exact: []string{"leaves", "leaves2", "vine"}, suffix: []string{"_leaves"}, value: TintFoliage},
}

// materialRules pick a colour class from the name when the pack gives no
// sound class, or when the name is more specific than the sound.
var materialRules = []nameRule{
	{exact: []string{"water", "flowing_water"}, value: "water"},
	{exact: []string{"lava", "flowing_lava"}, value: "lava"},
}

// materialDefaults are used for classes no textured block populates.
var materialDefaults = map[string]string{
	"default": "#808080",
	"water":   "#3f76e4",
	"lava":    "#cf5b13",
}

// blockAliases maps names the pack still uses to the names the server sends.
var blockAliases = map[string]string{
	"grass":      "grass_block",
	"grass_path": "dirt_path",
}

func firstMatch(rules []nameRule, name, def string) string {
	for _, r := range rules {
		if r.matches(name) {
			return r.value
		}
	}
	return def
}

func modelFor(name string) string { return firstMatch(shapeRules, name, ModelCube) }

func tintFor(name string) string {
	for _, e := range foliageTintExceptions {
		if name == e {
			return TintNone
		}
	}
	return firstMatch(tintRules, name, TintNone)
}

func fallbackClass(name string) string {
	name = strings.TrimPrefix(name, "minecraft:")
	return firstMatch(materialRules, name, "default")
}
