package layers

import (
	"hash/fnv"
	"strings"
)

// ColourSource is how a block came by its colour.
type ColourSource uint8

const (
	// Listed blocks are in the table by name.
	Listed ColourSource = iota
	// Derived blocks are a dye, a wood or a shape of a listed block.
	Derived
	// Guessed blocks are in no table, and are drawn in a colour made from
	// their name, so that they are at least not all alike.
	Guessed
)

// The colours are this service's own estimates of the average colour of
// each block seen from above, written down as numbers. No texture of the
// game, and nothing derived from one by a program, is in the repository.
// A colour for water and grass is the one the game gives a plains biome,
// since the biome of a column is not read here.
//
// 0xRRGGBB.
var table = map[string]uint32{
	// Stone and what grows from it.
	"stone": 0x7d7d7d, "granite": 0x956755, "diorite": 0xbcbcbc, "andesite": 0x888889,
	"polished_granite": 0x9a6a59, "polished_diorite": 0xc1c1c3, "polished_andesite": 0x848587,
	"cobblestone": 0x7f7f7f, "mossy_cobblestone": 0x6e765e, "stone_bricks": 0x7a797a,
	"mossy_stone_bricks": 0x737969, "cracked_stone_bricks": 0x767676, "chiseled_stone_bricks": 0x777777,
	"smooth_stone": 0x9e9e9e, "infested_stone": 0x7d7d7d, "infested_deepslate": 0x505052,
	"deepslate": 0x505052, "cobbled_deepslate": 0x4d4d50, "polished_deepslate": 0x484849,
	"deepslate_bricks": 0x464647, "cracked_deepslate_bricks": 0x434344, "deepslate_tiles": 0x363637,
	"cracked_deepslate_tiles": 0x343435, "chiseled_deepslate": 0x383839, "reinforced_deepslate": 0x505550,
	"tuff": 0x6c6d66, "polished_tuff": 0x6f706a, "tuff_bricks": 0x6e6f69, "chiseled_tuff": 0x6d6e68,
	"chiseled_tuff_bricks": 0x6d6e68, "calcite": 0xdfe0dc, "dripstone_block": 0x86695c,
	"pointed_dripstone": 0x82655a, "bedrock": 0x555555, "obsidian": 0x14121d, "crying_obsidian": 0x280a55,
	"basalt": 0x494850, "smooth_basalt": 0x484850, "polished_basalt": 0x58585d, "blackstone": 0x2a2429,
	"polished_blackstone": 0x353038, "gilded_blackstone": 0x382e2c, "polished_blackstone_bricks": 0x302c33,
	"cracked_polished_blackstone_bricks": 0x2e2a31, "chiseled_polished_blackstone": 0x353038,
	// Ground.
	"dirt": 0x866043, "coarse_dirt": 0x77553b, "rooted_dirt": 0x90674c, "dirt_with_roots": 0x90674c,
	"grass_block": 0x5f9a3c, "grass": 0x5f9a3c, "grass_path": 0x947b3e, "dirt_path": 0x947b3e,
	"farmland": 0x6b4a2c, "podzol": 0x5b3f18, "mycelium": 0x6f6369, "mud": 0x3c393d, "packed_mud": 0x8a6b55,
	"mud_bricks": 0x89674f, "clay": 0xa0a6b3, "gravel": 0x837f7e, "sand": 0xdbcfa3, "suspicious_sand": 0xd9cc9f,
	"suspicious_gravel": 0x817c7b, "red_sand": 0xbe6621, "sandstone": 0xd8cb9b, "smooth_sandstone": 0xdace9f,
	"cut_sandstone": 0xd7c99b, "chiseled_sandstone": 0xd6c99a, "red_sandstone": 0xba631d,
	"smooth_red_sandstone": 0xb9631f, "cut_red_sandstone": 0xb9631f, "chiseled_red_sandstone": 0xb8621d,
	"moss_block": 0x596d2d, "pale_moss_block": 0x6b7561, "sculk": 0x0d1e24, "sculk_catalyst": 0x0f2a30,
	"sculk_shrieker": 0x1b3b3a, "sculk_sensor": 0x074654, "calibrated_sculk_sensor": 0x0b5560,
	"snow": 0xf9fefe, "powder_snow": 0xf3f9f9, "ice": 0x91b7fd, "packed_ice": 0x8db4fa, "blue_ice": 0x74a7fd,
	"frosted_ice": 0x8fb3f5, "amethyst_block": 0x8561bf, "budding_amethyst": 0x8864c2,
	// Liquids.
	"water": 0x3f76e4, "flowing_water": 0x3f76e4, "lava": 0xcf5c0f, "flowing_lava": 0xcf5c0f,
	// Ores, as the average of the stone and its specks.
	"coal_ore": 0x696969, "iron_ore": 0x887368, "copper_ore": 0x7c7d72, "gold_ore": 0x8f8c7d,
	"redstone_ore": 0x856b6b, "lit_redstone_ore": 0x956b6b, "lapis_ore": 0x66708a, "diamond_ore": 0x798d8c,
	"emerald_ore": 0x6c8873, "deepslate_coal_ore": 0x4a4a4c, "deepslate_iron_ore": 0x6a635e,
	"deepslate_copper_ore": 0x5c5d59, "deepslate_gold_ore": 0x73664e, "deepslate_redstone_ore": 0x684a4a,
	"lit_deepslate_redstone_ore": 0x784a4a, "deepslate_lapis_ore": 0x4f5a73, "deepslate_diamond_ore": 0x536a6a,
	"deepslate_emerald_ore": 0x4e6857, "nether_gold_ore": 0x73362a, "quartz_ore": 0x75413e,
	"ancient_debris": 0x5e423a,
	// Blocks of metals and stones.
	"coal_block": 0x100f0f, "iron_block": 0xdcdcdc, "gold_block": 0xf9ec4f, "diamond_block": 0x62ede4,
	"emerald_block": 0x2acb57, "lapis_block": 0x1f438c, "redstone_block": 0xaf1805, "netherite_block": 0x423d3f,
	"copper_block": 0xc06b4f, "exposed_copper": 0xa17d67, "weathered_copper": 0x6c9972, "oxidized_copper": 0x52a284,
	"cut_copper": 0xc06b4f, "exposed_cut_copper": 0xa17d67, "weathered_cut_copper": 0x6c9972,
	"oxidized_cut_copper": 0x52a284, "raw_copper_block": 0x9a694f, "raw_iron_block": 0xa6876b,
	"raw_gold_block": 0xdda92e, "quartz_block": 0xebe5de, "smooth_quartz": 0xebe6df, "quartz_bricks": 0xeae4dd,
	"quartz_pillar": 0xece6e0, "chiseled_quartz_block": 0xe8e2da,
	// The nether.
	"netherrack": 0x612626, "nether_bricks": 0x2c151a, "red_nether_bricks": 0x450709, "cracked_nether_bricks": 0x2a1418,
	"chiseled_nether_bricks": 0x2d171b, "soul_sand": 0x513e32, "soul_soil": 0x4b392e, "glowstone": 0xab8354,
	"shroomlight": 0xf09246, "magma": 0x8e3f1f, "nether_wart_block": 0x720202, "warped_wart_block": 0x167779,
	"crimson_nylium": 0x822020, "warped_nylium": 0x2b7265, "crimson_stem": 0x5c191d, "warped_stem": 0x3a8e8c,
	"crimson_hyphae": 0x5c191d, "warped_hyphae": 0x3a8e8c, "stripped_crimson_stem": 0x7a3752,
	"stripped_warped_stem": 0x3a8e8c, "bone_block": 0xe5e1cf, "honeycomb_block": 0xe5941d, "honey_block": 0xf9b834,
	"slime": 0x70c05a, "respawn_anchor": 0x2a1f3b, "lodestone": 0x939699, "netherreactor": 0x2a6e5d,
	// The end.
	"end_stone": 0xdbde9e, "end_bricks": 0xdae0a2, "purpur_block": 0xa97da9, "purpur_pillar": 0xab80ab,
	"chorus_plant": 0x5d395d, "chorus_flower": 0x977897, "end_portal_frame": 0x5a7b66, "end_portal": 0x0c0c16,
	"end_gateway": 0x0c0c16, "dragon_egg": 0x0d0a12,
	// The sea.
	"prismarine": 0x639c97, "prismarine_bricks": 0x63ab9e, "dark_prismarine": 0x335b4b, "sea_lantern": 0xacc7be,
	"sponge": 0xc3c04a, "wet_sponge": 0xabb546, "tube_coral_block": 0x3157ce, "brain_coral_block": 0xcf5b9f,
	"bubble_coral_block": 0xa51aa2, "fire_coral_block": 0xa4232f, "horn_coral_block": 0xd8c742,
	"dead_tube_coral_block": 0x827b78, "dead_brain_coral_block": 0x827b78, "dead_bubble_coral_block": 0x827b78,
	"dead_fire_coral_block": 0x827b78, "dead_horn_coral_block": 0x827b78, "kelp": 0x578c39, "seagrass": 0x2d8a24,
	// Built and stored.
	"bricks": 0x96615a, "brick_block": 0x96615a, "bookshelf": 0x745a3b, "chiseled_bookshelf": 0x7b6141,
	"tnt": 0xdb441a, "hay_block": 0xa68b0c, "pumpkin": 0xc67618, "carved_pumpkin": 0xc67618, "lit_pumpkin": 0xd89a30,
	"melon_block": 0x6f911f, "cactus": 0x557f2b, "bamboo_block": 0x798a3b, "stripped_bamboo_block": 0xc1ad50,
	"bamboo": 0x5d9013, "sugar_cane": 0x94c065, "reeds": 0x94c065, "mangrove_roots": 0x4d3d28,
	"muddy_mangrove_roots": 0x453b2e, "crafting_table": 0x774928, "furnace": 0x6e6e6e, "lit_furnace": 0x7a6e60,
	"blast_furnace": 0x6a6a6e, "smoker": 0x6a6a68, "chest": 0xa27329, "trapped_chest": 0xa27329,
	"ender_chest": 0x163a35, "barrel": 0x86643a, "spawner": 0x1b2a37, "mob_spawner": 0x1b2a37,
	"trial_spawner": 0x3b4b57, "vault": 0x3b4b57, "enchanting_table": 0x5a1a1f, "anvil": 0x3c3c3c,
	"cauldron": 0x3d3d3d, "hopper": 0x3a3a3a, "dispenser": 0x777777, "dropper": 0x777777, "observer": 0x6e6e6e,
	"piston": 0x8c7852, "sticky_piston": 0x8e8a55, "note_block": 0x6a4a35, "jukebox": 0x6a4a35,
	"glass": 0xafd5db, "glass_pane": 0xafd5db, "tinted_glass": 0x2c262e, "iron_bars": 0x8a8b8b,
	"web": 0xe4e9ea, "cobweb": 0xe4e9ea, "torch": 0xffc850, "soul_torch": 0x6fd4d6, "redstone_torch": 0xb02a1a,
	"redstone_wire": 0xaa0000, "rail": 0x877049, "golden_rail": 0xb89b2e, "detector_rail": 0x7e6247,
	"activator_rail": 0x7a5a45, "ladder": 0x7c6339, "fire": 0xdc821e, "soul_fire": 0x3ebcd0, "scaffolding": 0xb69a58,
	"white_wool": 0xe9ecec, "iron_door": 0xc4c4c4, "heavy_core": 0x4a4d52, "crafter": 0x7a7a7a, "lantern": 0xc78b38,
	"soul_lantern": 0x5ea6a7, "campfire": 0x8a5b31, "soul_campfire": 0x4a8b8d, "bell": 0xfdeb6e, "beacon": 0x74dbd3,
	"conduit": 0x9f8f7a, "bee_nest": 0xc79a45, "beehive": 0xb3925f, "composter": 0x7d5a35, "grindstone": 0x8e8e8e,
	"stonecutter": 0x7a7a7a, "loom": 0x8d7449, "cartography_table": 0x6e5636, "fletching_table": 0xb2956a,
	"smithing_table": 0x3a3a46, "lectern": 0xa5845a, "target": 0xe3b1a4, "slime_block": 0x70c05a,
	"azalea": 0x657f2f, "flowering_azalea": 0x717a49, "glow_lichen": 0x7a9d82, "moss_carpet": 0x596d2d,
	"big_dripleaf": 0x6a9a31, "cherry_leaves": 0xe8b1cb, "azalea_leaves": 0x657c2f, "flowering_azalea_leaves": 0x717a49,
	"short_grass": 0x5f9a3c, "tall_grass": 0x5f9a3c, "fern": 0x5a8a3c, "large_fern": 0x5a8a3c, "deadbush": 0x7a5d2a,
	"vine": 0x4a8030, "waterlily": 0x2a7a2a, "snow_layer": 0xf9fefe, "sweet_berry_bush": 0x4a6e30,
	"mushroom_stem": 0xcbc4ba, "red_mushroom_block": 0xc22b27, "brown_mushroom_block": 0x93725a,
	"sculk_vein": 0x0d1e24, "creaking_heart": 0x5b4a37, "resin_block": 0xd4741f, "resin_bricks": 0xc2681e,
	// Names the game kept from before its blocks were renamed one by one;
	// a world that has been played across versions still holds some.
	"leaves": 0x48782a, "leaves2": 0x50802a, "log": 0x6d5532, "log2": 0x67604f, "wood": 0x6d5532, "planks": 0xa2824e,
	"stonebrick": 0x7a797a, "hardened_clay": 0x985e43, "terracotta": 0x985e43, "wooden_slab": 0xa2824e,
	"double_wooden_slab": 0xa2824e, "wooden_door": 0xa2824e, "wooden_button": 0xa2824e, "wooden_pressure_plate": 0xa2824e,
	"trapdoor": 0x7c6339, "fence_gate": 0xa2824e, "normal_stone_slab": 0x7d7d7d, "stone_block_slab": 0x7d7d7d,
	"double_stone_block_slab": 0x7d7d7d, "stone_block_slab2": 0x9c7a60, "double_stone_block_slab2": 0x9c7a60,
	"stone_block_slab3": 0x888889, "stone_block_slab4": 0x7a797a, "monster_egg": 0x7d7d7d, "noteblock": 0x6a4a35,
	"coral_block": 0xcf5b9f, "tallgrass": 0x5f9a3c, "double_plant": 0x5f9a3c, "red_flower": 0xc23a2e, "yellow_flower": 0xe8d23c,
	"sapling": 0x4a7a2a, "azalea_leaves_flowered": 0x717a49, "portal": 0x5a1fa8,
	// Small built things.
	"redstone_lamp": 0x6f4a2f, "lit_redstone_lamp": 0xf0b46a, "lit_blast_furnace": 0x6a6a6e, "brewing_stand": 0xa0a0a0,
	"cake": 0xf0e6d8, "bed": 0xa12722, "straw_bed": 0xc9a53a, "decorated_pot": 0x9a5a3a, "daylight_detector": 0x8e8068,
	"daylight_detector_inverted": 0x6e6a5a, "flower_pot": 0x8a4f3a, "frame": 0xa5845a, "glow_frame": 0xa5845a,
	"skull": 0xcfcfc0, "skeleton_skull": 0xcfcfc0, "chain": 0x3b4350, "iron_chain": 0x3b4350, "chipped_anvil": 0x3c3c3c,
	"damaged_anvil": 0x3c3c3c, "candle": 0xe8d9b9, "standing_sign": 0xa2824e, "wall_sign": 0xa2824e,
	"standing_banner": 0xe9ecec, "wall_banner": 0xe9ecec, "trip_wire": 0xd8d8d8, "tripwire_hook": 0x8a7a5a,
	"lever": 0x7d7d7d, "unlit_redstone_torch": 0x5a2a1a, "end_rod": 0xe8e8e8, "lightning_rod": 0xc06b4f,
	"unpowered_repeater": 0x9a9a9a, "powered_repeater": 0xb0a0a0, "unpowered_comparator": 0x9a9a9a, "powered_comparator": 0xb0a0a0,
	"copper_bulb": 0xa57a65, "copper_grate": 0xc06b4f, "chiseled_copper": 0xc06b4f, "copper_door": 0xc06b4f,
	"copper_trapdoor": 0xc06b4f, "copper_chest": 0xc06b4f, "copper_lantern": 0xc78b38, "bubble_column": 0x3f76e4,
	// Blocks this service has only ever seen by name, with colours that
	// are guesses from their names.
	"sulfur": 0xcbbf55, "potent_sulfur": 0xd7c93a, "sulfur_bricks": 0xbfb350, "polished_sulfur": 0xc9bd53,
	"sulfur_spike": 0xcbbf55, "cinnabar": 0x9c3b34, "cinnabar_bricks": 0x943731, "polished_cinnabar": 0x9c3b34,
	"orange_poplar_leaves": 0xd88a30, "red_poplar_leaves": 0xb83a2a, "yellow_poplar_leaves": 0xd8c040,
	"dried_ghast": 0xa09c92, "sniffer_egg": 0x9a3a2e, "turtle_egg": 0xe6e2cc,
}

// The sixteen dyes, by the names the dyed blocks carry.
var dyes = map[string]uint32{
	"white": 0xe9ecec, "orange": 0xf07613, "magenta": 0xbd44b3, "light_blue": 0x3aafd9, "yellow": 0xf8c627,
	"lime": 0x70b919, "pink": 0xed8dac, "gray": 0x3e4447, "light_gray": 0x8e8e86, "silver": 0x8e8e86,
	"cyan": 0x158991, "purple": 0x792aac, "blue": 0x35399d, "brown": 0x724728, "green": 0x546d1b,
	"red": 0xa12722, "black": 0x141519,
}

// Dyed families and how strongly each shows its dye, 0 to 256: terracotta
// is the dye greyed with clay, glass the dye faint in a pane.
var dyedFamilies = map[string]int{
	"wool": 256, "concrete": 256, "concrete_powder": 240, "terracotta": 150, "stained_glass": 190,
	"stained_glass_pane": 190, "carpet": 256, "candle": 256, "shulker_box": 230, "bed": 256,
	"glazed_terracotta": 230, "banner": 256, "candle_cake": 256,
}

var clay = [3]uint8{0x98, 0x5e, 0x43}

// The woods and what each is, by the part of a name that comes first.
type wood struct{ planks, log, leaves uint32 }

var woods = map[string]wood{
	"oak":      {0xa2824e, 0x6d5532, 0x48782a},
	"spruce":   {0x72542f, 0x3a2510, 0x3e5f3c},
	"birch":    {0xc0af79, 0xd8d7d2, 0x69864a},
	"jungle":   {0xa07351, 0x55431a, 0x2d7d14},
	"acacia":   {0xa85a32, 0x67604f, 0x50802a},
	"dark_oak": {0x422b14, 0x3c2e1a, 0x345c1c},
	"mangrove": {0x75362f, 0x543f24, 0x5a7d28},
	"cherry":   {0xe2b2ac, 0x36212c, 0xe8b1cb},
	"pale_oak": {0xe3dad2, 0xdcd6d0, 0x8a9a82},
	"bamboo":   {0xc1ad50, 0x798a3b, 0x5d9013},
	"poplar":   {0xb8a47a, 0x7a6a50, 0x8aa040},
	"darkoak":  {0x422b14, 0x3c2e1a, 0x345c1c},
	"crimson":  {0x653046, 0x5c191d, 0x822020},
	"warped":   {0x2b6863, 0x3a8e8c, 0x167779},
}

// colourOf is the colour of a block from its name: by name where the table
// has it, by its dye or its wood or the block it is a shape of where it
// does not, and a colour of the name's own as the last resort.
func colourOf(name string) ([3]uint8, ColourSource) {
	if v, ok := table[name]; ok {
		return rgbOf(v), Listed
	}
	if rgb, ok := derive(name, 0); ok {
		return rgb, Derived
	}
	return guess(name), Guessed
}

func rgbOf(v uint32) [3]uint8 { return [3]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v)} }

func scale(c [3]uint8, k, of int) [3]uint8 {
	return [3]uint8{uint8(int(c[0]) * k / of), uint8(int(c[1]) * k / of), uint8(int(c[2]) * k / of)}
}

func mixRGB(a, b [3]uint8, bShare int) [3]uint8 {
	var out [3]uint8
	for i := range out {
		out[i] = uint8((int(a[i])*(256-bShare) + int(b[i])*bShare) / 256)
	}
	return out
}

// shapes are the words a name gains to say what shape a block of some
// material has; stripping one leaves the material.
var shapes = []string{
	"_stairs", "_slab", "_wall", "_fence_gate", "_fence", "_double_slab", "_button", "_pressure_plate",
	"_trapdoor", "_door", "_sign", "_wall_sign", "_hanging_sign", "_pillar", "_bricks", "_brick",
}

var shapePrefixes = []string{"double_", "polished_", "chiseled_", "smooth_", "cut_", "cracked_", "mossy_", "waxed_", "stripped_", "infested_"}

func derive(name string, depth int) ([3]uint8, bool) {
	if depth > 3 {
		return [3]uint8{}, false
	}
	// Dyed: a dye then a family, or the old order, family then dye.
	for dye, v := range dyes {
		if rest, ok := strings.CutPrefix(name, dye+"_"); ok {
			if k, ok := dyedFamilies[rest]; ok {
				return dyed(rgbOf(v), rest, k), true
			}
		}
	}
	if strings.HasPrefix(name, "undyed_shulker_box") || name == "shulker_box" {
		return rgbOf(0x8b618b), true
	}
	// Copper in its stages of weathering, whatever it has been made into.
	if strings.Contains(name, "copper") || strings.Contains(name, "lightning_rod") {
		switch {
		case strings.Contains(name, "oxidized"):
			return rgbOf(table["oxidized_copper"]), true
		case strings.Contains(name, "weathered"):
			return rgbOf(table["weathered_copper"]), true
		case strings.Contains(name, "exposed"):
			return rgbOf(table["exposed_copper"]), true
		}
		return rgbOf(table["copper_block"]), true
	}
	// Wooden.
	for kind, w := range woods {
		rest, ok := strings.CutPrefix(name, kind+"_")
		if !ok {
			continue
		}
		switch {
		case rest == "leaves":
			return rgbOf(w.leaves), true
		case rest == "log" || rest == "wood" || rest == "stem" || rest == "hyphae":
			return rgbOf(w.log), true
		case rest == "planks" || rest == "stairs" || rest == "slab" || rest == "fence" || rest == "fence_gate" ||
			rest == "door" || rest == "trapdoor" || rest == "button" || rest == "pressure_plate" || rest == "sign" ||
			rest == "wall_sign" || rest == "hanging_sign" || rest == "mosaic" || rest == "chest_boat" || rest == "boat" ||
			rest == "double_slab" || rest == "shelf":
			return rgbOf(w.planks), true
		}
	}
	if rest, ok := strings.CutPrefix(name, "stripped_"); ok {
		for kind, w := range woods {
			if strings.HasPrefix(rest, kind+"_") {
				return rgbOf(w.planks), true
			}
		}
	}
	// A shape of another material: take the shape off and try again, in
	// the plural and the singular, and as a block.
	for _, s := range shapes {
		if base, ok := strings.CutSuffix(name, s); ok && base != "" {
			for _, candidate := range []string{base, base + "s", base + "_block", base + "_bricks", base + "_planks"} {
				if v, ok := table[candidate]; ok {
					return rgbOf(v), true
				}
			}
			if rgb, ok := derive(base, depth+1); ok {
				return rgb, true
			}
		}
	}
	for _, p := range shapePrefixes {
		if base, ok := strings.CutPrefix(name, p); ok && base != "" {
			if v, ok := table[base]; ok {
				return rgbOf(v), true
			}
			if rgb, ok := derive(base, depth+1); ok {
				return rgb, true
			}
		}
	}
	for _, s := range []string{"_ore", "_block", "_leaves"} {
		if base, ok := strings.CutSuffix(name, s); ok {
			if v, ok := table[base]; ok {
				return rgbOf(v), true
			}
		}
	}
	// Coral and its fans and walls are the colour of the coral.
	for _, kind := range []string{"tube", "brain", "bubble", "fire", "horn"} {
		if strings.Contains(name, kind+"_coral") {
			if strings.HasPrefix(name, "dead_") {
				return rgbOf(0x827b78), true
			}
			return rgbOf(table[kind+"_coral_block"]), true
		}
	}
	return [3]uint8{}, false
}

func dyed(dye [3]uint8, family string, strength int) [3]uint8 {
	switch family {
	case "terracotta", "concrete_powder":
		return mixRGB(clay, dye, strength)
	case "stained_glass", "stained_glass_pane":
		return mixRGB([3]uint8{0xaf, 0xd5, 0xdb}, dye, strength)
	}
	return scale(dye, strength, 256)
}

// guess makes a muted colour from the name, the same for the same name, so
// a block the table lacks is neither invisible nor the colour of its
// neighbour.
func guess(name string) [3]uint8 {
	h := fnv.New32a()
	h.Write([]byte(name))
	v := h.Sum32()
	grey := [3]uint8{0x80, 0x80, 0x80}
	hue := [3]uint8{uint8(v), uint8(v >> 8), uint8(v >> 16)}
	return mixRGB(grey, hue, 110)
}
