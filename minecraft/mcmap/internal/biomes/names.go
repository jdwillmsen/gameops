package biomes

import (
	"fmt"
	"strconv"
	"strings"
)

// Biome is one kind of biome, as the page and the API name it.
type Biome struct {
	// ID is the number the world stores.
	ID uint32 `json:"id"`
	// Name is the game's own identifier without the minecraft: prefix. The
	// Bedrock names are older than the ones players use: mushroom_island
	// is what the game now calls Mushroom Fields.
	Name string `json:"name"`
	// Label is the name the game shows a player.
	Label string `json:"label"`
	// Color is what the overlay draws the biome in, as #rrggbb.
	Color string `json:"color"`
	// Known is false for an id this version's list does not have. It is
	// still drawn, counted and searchable, under a name made from the id.
	Known bool `json:"known"`
}

type known struct {
	name, label string
	rgb         uint32
}

// overflowID stands for every id past the number of kinds one dimension
// can hold. No world stores it: ids are small.
const overflowID = ^uint32(0)

// The biomes of the 1.26 line, from the biome list in Mojang's published
// bedrock-samples (metadata/vanilladata_modules/mojang-biomes.json) at the
// revision the mob icons are pinned to. The ids are the game's and are
// what is stored; the labels are the names the game shows, which the list
// does not carry.
var vanilla = map[uint32]known{
	0:   {"ocean", "Ocean", 0x000070},
	1:   {"plains", "Plains", 0x8DB360},
	2:   {"desert", "Desert", 0xFA9418},
	3:   {"extreme_hills", "Windswept Hills", 0x606060},
	4:   {"forest", "Forest", 0x056621},
	5:   {"taiga", "Taiga", 0x0B6659},
	6:   {"swampland", "Swamp", 0x07F9B2},
	7:   {"river", "River", 0x0000FF},
	8:   {"hell", "Nether Wastes", 0xBF3B3B},
	9:   {"the_end", "The End", 0x8080FF},
	10:  {"legacy_frozen_ocean", "Legacy Frozen Ocean", 0x9090A0},
	11:  {"frozen_river", "Frozen River", 0xA0A0FF},
	12:  {"ice_plains", "Snowy Plains", 0xFFFFFF},
	13:  {"ice_mountains", "Snowy Mountains", 0xA0A0A0},
	14:  {"mushroom_island", "Mushroom Fields", 0xFF00FF},
	15:  {"mushroom_island_shore", "Mushroom Field Shore", 0xA000FF},
	16:  {"beach", "Beach", 0xFADE55},
	17:  {"desert_hills", "Desert Hills", 0xD25F12},
	18:  {"forest_hills", "Forest Hills", 0x22551C},
	19:  {"taiga_hills", "Taiga Hills", 0x163933},
	20:  {"extreme_hills_edge", "Mountain Edge", 0x72789A},
	21:  {"jungle", "Jungle", 0x537B09},
	22:  {"jungle_hills", "Jungle Hills", 0x2C4205},
	23:  {"jungle_edge", "Sparse Jungle", 0x628B17},
	24:  {"deep_ocean", "Deep Ocean", 0x000030},
	25:  {"stone_beach", "Stony Shore", 0xA2A284},
	26:  {"cold_beach", "Snowy Beach", 0xFAF0C0},
	27:  {"birch_forest", "Birch Forest", 0x307444},
	28:  {"birch_forest_hills", "Birch Forest Hills", 0x1F5F32},
	29:  {"roofed_forest", "Dark Forest", 0x40511A},
	30:  {"cold_taiga", "Snowy Taiga", 0x31554A},
	31:  {"cold_taiga_hills", "Snowy Taiga Hills", 0x243F36},
	32:  {"mega_taiga", "Old Growth Pine Taiga", 0x596651},
	33:  {"mega_taiga_hills", "Giant Tree Taiga Hills", 0x454F3E},
	34:  {"extreme_hills_plus_trees", "Windswept Forest", 0x507050},
	35:  {"savanna", "Savanna", 0xBDB25F},
	36:  {"savanna_plateau", "Savanna Plateau", 0xA79D64},
	37:  {"mesa", "Badlands", 0xD94515},
	38:  {"mesa_plateau_stone", "Wooded Badlands", 0xB09765},
	39:  {"mesa_plateau", "Badlands Plateau", 0xCA8C65},
	40:  {"warm_ocean", "Warm Ocean", 0x0000AC},
	41:  {"deep_warm_ocean", "Deep Warm Ocean", 0x000050},
	42:  {"lukewarm_ocean", "Lukewarm Ocean", 0x000090},
	43:  {"deep_lukewarm_ocean", "Deep Lukewarm Ocean", 0x000040},
	44:  {"cold_ocean", "Cold Ocean", 0x202070},
	45:  {"deep_cold_ocean", "Deep Cold Ocean", 0x202038},
	46:  {"frozen_ocean", "Frozen Ocean", 0x7070D6},
	47:  {"deep_frozen_ocean", "Deep Frozen Ocean", 0x404090},
	48:  {"bamboo_jungle", "Bamboo Jungle", 0x768E14},
	49:  {"bamboo_jungle_hills", "Bamboo Jungle Hills", 0x3B470A},
	129: {"sunflower_plains", "Sunflower Plains", 0xB5DB88},
	130: {"desert_mutated", "Desert Lakes", 0xFFBC40},
	131: {"extreme_hills_mutated", "Windswept Gravelly Hills", 0x888888},
	132: {"flower_forest", "Flower Forest", 0x2D8E49},
	133: {"taiga_mutated", "Taiga Mountains", 0x338E81},
	134: {"swampland_mutated", "Swamp Hills", 0x2FFFDA},
	140: {"ice_plains_spikes", "Ice Spikes", 0xB4DCDC},
	149: {"jungle_mutated", "Modified Jungle", 0x7BA331},
	151: {"jungle_edge_mutated", "Modified Jungle Edge", 0x8AB33F},
	155: {"birch_forest_mutated", "Old Growth Birch Forest", 0x589C6C},
	156: {"birch_forest_hills_mutated", "Tall Birch Hills", 0x47875A},
	157: {"roofed_forest_mutated", "Dark Forest Hills", 0x687942},
	158: {"cold_taiga_mutated", "Snowy Taiga Mountains", 0x597D72},
	160: {"redwood_taiga_mutated", "Old Growth Spruce Taiga", 0x818E79},
	161: {"redwood_taiga_hills_mutated", "Giant Spruce Taiga Hills", 0x6D7766},
	162: {"extreme_hills_plus_trees_mutated", "Gravelly Mountains+", 0x789878},
	163: {"savanna_mutated", "Windswept Savanna", 0xE5DA87},
	164: {"savanna_plateau_mutated", "Shattered Savanna Plateau", 0xCFC58C},
	165: {"mesa_bryce", "Eroded Badlands", 0xFF6D3D},
	166: {"mesa_plateau_stone_mutated", "Modified Wooded Badlands Plateau", 0xD8BF8D},
	167: {"mesa_plateau_mutated", "Modified Badlands Plateau", 0xF2B48D},
	178: {"soulsand_valley", "Soul Sand Valley", 0x5E3830},
	179: {"crimson_forest", "Crimson Forest", 0xDD0808},
	180: {"warped_forest", "Warped Forest", 0x49907B},
	181: {"basalt_deltas", "Basalt Deltas", 0x403636},
	182: {"jagged_peaks", "Jagged Peaks", 0xDCDCC8},
	183: {"frozen_peaks", "Frozen Peaks", 0xB0B3CE},
	184: {"snowy_slopes", "Snowy Slopes", 0xC4C4C4},
	185: {"grove", "Grove", 0x47726C},
	186: {"meadow", "Meadow", 0x60A445},
	187: {"lush_caves", "Lush Caves", 0x283C00},
	188: {"dripstone_caves", "Dripstone Caves", 0x4E3012},
	189: {"stony_peaks", "Stony Peaks", 0x7B8F74},
	190: {"deep_dark", "Deep Dark", 0x031F29},
	191: {"mangrove_swamp", "Mangrove Swamp", 0x2CCC8E},
	192: {"cherry_grove", "Cherry Grove", 0xFF91C8},
	193: {"pale_garden", "Pale Garden", 0x696D95},
	194: {"sulfur_caves", "Sulfur Caves", 0xC8C832},
	195: {"dappled_forest", "Dappled Forest", 0x4C8A3A},
}

// Lookup describes a biome id. An id the list does not have is described
// by its number, so that a later game version's biome shows up as itself
// and not as a neighbour or a hole.
func Lookup(id uint32) Biome {
	if b, ok := vanilla[id]; ok {
		return Biome{ID: id, Name: b.name, Label: b.label, Color: hex(b.rgb), Known: true}
	}
	if id == overflowID {
		return Biome{ID: id, Name: "unknown", Label: "Unknown biomes", Color: hex(unknownRGB(id))}
	}
	n := strconv.FormatUint(uint64(id), 10)
	return Biome{ID: id, Name: "unknown_" + n, Label: "Unknown biome " + n, Color: hex(unknownRGB(id))}
}

func rgbOf(id uint32) uint32 {
	if b, ok := vanilla[id]; ok {
		return b.rgb
	}
	return unknownRGB(id)
}

// unknownRGB spreads unlisted ids over purples and pinks, which no listed
// land biome uses, so two of them side by side are still told apart.
func unknownRGB(id uint32) uint32 {
	h := id * 2654435761
	r, g, b := 0xC0|h>>26, h>>20&0x3F, 0xC0|h>>14&0x3F
	return r<<16 | g<<8 | b
}

func hex(rgb uint32) string { return fmt.Sprintf("#%06x", rgb&0xFFFFFF) }

// fold makes names comparable however they were typed: "Mushroom Fields",
// "mushroom_fields" and "minecraft:mushroom_island" all have to find the
// same biome.
func fold(s string) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "minecraft:")
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == ' ' || r == '-' }), " ")
}

var byName = func() map[string]uint32 {
	m := make(map[string]uint32, 2*len(vanilla))
	for id, b := range vanilla {
		m[fold(b.label)] = id
	}
	// The game's identifier wins where a label of one biome spells the
	// identifier of another.
	for id, b := range vanilla {
		m[fold(b.name)] = id
	}
	return m
}()

// Resolve finds the id a name stands for: the game's identifier, the name
// the game shows, the name Lookup gives an unlisted id, or the id itself.
func Resolve(name string) (uint32, bool) {
	f := fold(name)
	if id, ok := byName[f]; ok {
		return id, true
	}
	if f == "unknown" {
		return overflowID, true
	}
	// The number is read as typed: folding would make -4 into 4.
	raw := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "unknown_")
	if n, err := strconv.ParseUint(raw, 10, 32); err == nil && raw[0] != '+' {
		return uint32(n), true
	}
	return 0, false
}

// Matches reports whether a biome's names contain the folded query.
func (b Biome) Matches(foldedQuery string) bool {
	return strings.Contains(fold(b.Name), foldedQuery) || strings.Contains(fold(b.Label), foldedQuery)
}

// Fold is the form Matches compares in.
func Fold(s string) string { return fold(s) }
