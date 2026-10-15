package layers

import (
	"strings"
	"sync"
)

// ID is a block's number in a Registry. Sub-chunks name their blocks in a
// palette of their own, and a palette is rebuilt into these so that a
// column of blocks from different slices compares by number.
type ID uint16

// AirID is the one ID every Registry gives to air before any other block.
const AirID ID = 0

// Flags say what a block is to a view from above.
type Flags uint8

const (
	// Air is a block that holds nothing to look at: air in its three
	// kinds, and the blocks that exist only to be placed and not seen.
	Air Flags = 1 << iota
	// Water and Lava are the two liquids.
	Water
	Lava
	// Plant is a block as thin as grass: a map of the ground shows what is
	// under it, and a person walking is not stopped by it.
	Plant
	// Feature is passable and thin like a plant, but is something built or
	// made and not the ground's cover: a rail, a torch, a ladder.
	Feature
)

// Liquid is either liquid.
const Liquid = Water | Lava

// Passable says a person can stand in the block's place.
const Passable = Air | Plant | Feature

// Block is what the renderer needs to know of a block by name.
type Block struct {
	Name  string
	Flags Flags
	RGB   [3]uint8
	// Source says where the colour came from, for a report of the blocks
	// the table does not know.
	Source ColourSource
}

// Solid says the block is ground to stand on: neither a liquid nor
// something a person walks through.
func (b *Block) Solid() bool { return b.Flags&(Liquid|Passable) == 0 }

// Registry numbers the blocks of a world as they are met. It is safe for
// concurrent use; a block once numbered never changes.
type Registry struct {
	mu     sync.RWMutex
	ids    map[string]ID
	blocks []Block
}

// NewRegistry starts a registry that holds air.
func NewRegistry() *Registry {
	r := &Registry{ids: map[string]ID{}}
	r.add("minecraft:air")
	return r
}

// intern is the ID of the block named by name, which is the bytes of a
// palette entry's name and is copied only the first time it is seen.
func (r *Registry) intern(name []byte) ID {
	r.mu.RLock()
	id, ok := r.ids[string(name)]
	r.mu.RUnlock()
	if ok {
		return id
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.ids[string(name)]; ok {
		return id
	}
	return r.add(string(name))
}

func (r *Registry) add(full string) ID {
	name := strings.TrimPrefix(full, "minecraft:")
	// A world has some hundreds of kinds of block. A palette that names
	// more than a 16-bit number can hold is damaged, and everything past
	// the limit is drawn as the last one.
	if len(r.blocks) >= 1<<16-1 {
		return ID(len(r.blocks) - 1)
	}
	flags := classify(name)
	rgb, source := colourOf(name)
	// What is never drawn needs no colour.
	if flags&Air != 0 {
		source = Listed
	}
	id := ID(len(r.blocks))
	r.blocks = append(r.blocks, Block{Name: name, Flags: flags, RGB: rgb, Source: source})
	r.ids[full] = id
	return id
}

// Blocks is every block numbered so far, indexed by ID. The slice is a
// snapshot: later blocks are not in it, and the entries in it never change.
func (r *Registry) Blocks() []Block {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.blocks[:len(r.blocks):len(r.blocks)]
}

// classify decides what a block is from its name alone. Bedrock names the
// shape of a block in its name for the flattened blocks, and what a block
// is made of in the rest, which is enough to tell what can be stood on.
func classify(name string) Flags {
	switch name {
	case "air", "cave_air", "void_air":
		return Air
	case "water", "flowing_water", "bubble_column":
		return Water
	case "lava", "flowing_lava":
		return Lava
	}
	if invisible(name) {
		return Air
	}
	if _, ok := features[name]; ok || strings.Contains(name, "lightning_rod") {
		return Feature
	}
	if _, ok := plants[name]; ok {
		return Plant
	}
	for _, s := range featureSuffixes {
		if strings.HasSuffix(name, s) {
			return Feature
		}
	}
	for _, s := range plantSuffixes {
		if strings.HasSuffix(name, s) {
			return Plant
		}
	}
	return 0
}

func invisible(name string) bool {
	switch name {
	case "structure_void", "barrier", "allow", "deny", "border_block", "moving_block", "light_block", "piston_arm_collision":
		return true
	}
	return strings.HasPrefix(name, "light_block_") || strings.HasSuffix(name, "_arm_collision")
}

// The thin things a map of the ground sees through.
var plants = setOf(
	"short_grass", "tall_grass", "fern", "large_fern", "deadbush", "seagrass", "tall_seagrass", "kelp",
	"vine", "glow_lichen", "sculk_vein", "hanging_roots", "spore_blossom", "waterlily", "bush", "firefly_bush",
	"short_dry_grass", "tall_dry_grass", "leaf_litter", "wildflowers", "cactus_flower", "pink_petals",
	"dandelion", "poppy", "blue_orchid", "allium", "azure_bluet", "red_tulip", "orange_tulip", "white_tulip",
	"pink_tulip", "oxeye_daisy", "cornflower", "lily_of_the_valley", "wither_rose", "torchflower",
	"sunflower", "lilac", "rose_bush", "peony", "pitcher_plant", "pitcher_crop", "torchflower_crop",
	"red_mushroom", "brown_mushroom", "crimson_roots", "warped_roots", "nether_sprouts", "crimson_fungus",
	"warped_fungus", "weeping_vines", "twisting_vines", "cave_vines", "cave_vines_body_with_berries",
	"cave_vines_head_with_berries", "sweet_berry_bush", "sea_pickle", "snow_layer", "web", "wheat", "carrots",
	"potatoes", "beetroot", "nether_wart", "melon_stem", "pumpkin_stem", "reeds", "bamboo_sapling", "big_dripleaf_stem",
	"small_dripleaf_block", "mangrove_propagule", "fire", "soul_fire", "frogspawn", "moss_carpet", "pale_moss_carpet",
	"pale_hanging_moss", "resin_clump", "closed_eyeblossom", "open_eyeblossom", "amethyst_cluster", "large_amethyst_bud",
	"medium_amethyst_bud", "small_amethyst_bud", "tallgrass", "double_plant", "red_flower", "yellow_flower", "sapling",
	"coral_fan", "coral_fan_hang", "coral_fan_hang2", "coral_fan_hang3", "shelf_mushroom", "red_shrub", "golden_dandelion",
	"cocoa", "azure_bluet",
)

var plantSuffixes = []string{"_sapling", "_carpet", "_coral_fan", "_coral_wall_fan", "_coral_fan_dead"}

// The thin things somebody put there, which a map of a mine or a cave
// should keep: they show where people have been.
var features = setOf(
	"redstone_wire", "rail", "golden_rail", "detector_rail", "activator_rail", "ladder", "torch", "soul_torch",
	"redstone_torch", "unlit_redstone_torch", "copper_torch", "lever", "tripwire", "tripwire_hook", "string",
	"end_rod", "chain", "iron_chain", "lantern", "soul_lantern", "lightning_rod", "repeater", "unpowered_repeater",
	"powered_repeater", "comparator", "unpowered_comparator", "powered_comparator", "scaffolding", "trip_wire", "candle", "flower_pot", "skull", "skeleton_skull", "frame", "glow_frame",
	"standing_sign", "wall_sign",
)

var featureSuffixes = []string{"_button", "_pressure_plate", "_sign", "_wall_sign", "_hanging_sign", "_candle", "_rail", "_torch", "_banner", "_wall_banner"}

func setOf(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}
