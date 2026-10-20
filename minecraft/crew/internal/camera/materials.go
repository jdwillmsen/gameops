package camera

import (
	"hash/fnv"
	"strings"
	"sync"
)

// material is what the browser draws a block as when it has no atlas: a
// colour per block name, jittered per block in the browser.
type material struct {
	ID    uint8   `json:"id"`
	Name  string  `json:"name"`
	Color string  `json:"color"`
	Alpha float32 `json:"alpha"`
	// NonCube marks blocks the flat page leaves out and the textured page
	// draws from the atlas table's model kind.
	NonCube bool `json:"non_cube,omitempty"`
}

// unmappedBlock names a block state the decoder could not resolve; it is drawn
// as a visible placeholder rather than left out.
const unmappedBlock = "unmapped"

var known = map[string]string{
	"grass_block": "#5d9c3a", "dirt": "#866043", "coarse_dirt": "#775437", "stone": "#7d7d7d",
	"cobblestone": "#6e6e6e", "sand": "#dbd3a0", "sandstone": "#d6cb8f", "gravel": "#857f7c",
	"bedrock": "#2b2b2b", "water": "#3050c0", "flowing_water": "#3050c0", "lava": "#e0601a",
	"oak_log": "#6b5230", "birch_log": "#d5cfbf", "spruce_log": "#3b2a14", "oak_planks": "#a8864f",
	"oak_leaves": "#3b7a2b", "birch_leaves": "#6d9a49", "spruce_leaves": "#2e5a35",
	"snow": "#f4f8f8", "ice": "#8fb8f0", "clay": "#a0a6b4", "deepslate": "#4d4d52",
	unmappedBlock: "#b0b0b0", "granite": "#9a6b57", "diorite": "#c4c4c4", "andesite": "#888888", "coal_ore": "#444444",
	"iron_ore": "#a68a78", "grass_path": "#8f7a3f", "mycelium": "#6f5f72", "podzol": "#5a4020",
	"red_sand": "#b5622a", "terracotta": "#9a5d42", "tuff": "#5c5e55",
}

var translucent = map[string]float32{"water": .6, "flowing_water": .6, "ice": .8}

// notCube reports a block the textured page must not draw as a full cube
// even where the atlas table says it is one: the table has no model for these
// and falls back to a cube. A name that merely contains one of these words is
// not enough; fire_coral_block, brown_mushroom_block and dried_kelp_block are
// whole cubes and were hidden by a substring match.
func notCube(name string) bool {
	if strings.HasSuffix(name, "_block") || name == "budding_amethyst" {
		return false
	}
	switch name {
	case "grass", "short_grass", "tall_grass", "fern", "large_fern", "double_plant", "tallgrass", "dead_bush", "deadbush",
		"sapling", "web", "cobweb", "fire", "soul_fire", "campfire", "soul_campfire", "rail", "bush", "firefly_bush",
		"leaf_litter", "wildflowers", "pink_petals", "torchflower_crop", "golden_dandelion", "flower_pot", "bubble_column",
		"pointed_dripstone", "snow_layer", "lily_pad", "waterlily", "kelp", "seagrass", "glow_lichen", "sculk_vein",
		"vine", "twisting_vines", "weeping_vines", "cave_vines", "reeds", "sweet_berry_bush", "torch", "soul_torch",
		"redstone_torch", "lantern", "soul_lantern", "chain", "lever", "tripwire", "tripwire_hook", "wall_banner",
		"standing_banner", "ladder", "end_rod", "lightning_rod", "candle", "cake", "brewing_stand", "cauldron",
		"hopper", "end_portal_frame", "dragon_egg", "flower", "bamboo", "scaffolding", "chorus_flower", "chorus_plant":
		return true
	}
	for _, suffix := range []string{
		"_button", "_sign", "_rail", "_fan", "_bud", "_cluster", "_vines", "_sapling", "_banner", "_coral",
		"_stairs", "_trapdoor", "_fence", "_fence_gate", "_carpet", "_pressure_plate", "_candle", "_head", "_skull",
		"_tulip", "_orchid", "_allium", "_bluet", "_daisy", "_cornflower", "_lily_of_the_valley", "_rose", "_peony",
		"_lilac", "_dandelion", "_poppy", "_mushroom", "_roots", "_fungus", "_propagule", "_pane", "_bars", "_wall",
	} {
		if strings.HasSuffix(name, suffix) {
			// A wall sign is a sign; a stone wall is a wall. Both hide.
			return true
		}
	}
	// Corals that stand on their own are plants; the _block suffix returned
	// above keeps the cubes.
	return strings.Contains(name, "coral") || strings.Contains(name, "azalea") && !strings.HasSuffix(name, "_leaves") ||
		strings.HasPrefix(name, "potted_") || strings.HasPrefix(name, "tulip") || strings.HasPrefix(name, "azure_")
}

var airLike = map[string]bool{"air": true, "cave_air": true, "void_air": true, "unknown": true,
	"barrier": true, "structure_void": true, "light_block": true, "allow": true, "deny": true, "border_block": true}

// variantBudget is how many materials may exist before a block's shape
// variants stop getting ids of their own. The wire holds 255 ids, and a
// stair alone has forty states, so past this point a variant is drawn as the
// block's default shape instead of crowding out whole block types.
const variantBudget = 190

// splitVariant separates a block name from the shape suffix the decoder adds
// ("oak_stairs@0,0,0,16,8,16;0,8,8,16,16,16", boxes in sixteenths of a block).
func splitVariant(full string) (base, boxes string) {
	base, boxes, _ = strings.Cut(full, "@")
	return base, boxes
}

type materialTable struct {
	mu     sync.Mutex
	byName map[string]uint8
	list   []material
}

func newMaterialTable() *materialTable {
	return &materialTable{byName: map[string]uint8{}}
}

// id returns 0 for blocks that are not drawn. fresh is true when a new
// material was assigned, so the caller knows to tell viewers.
func (t *materialTable) id(full string) (id uint8, fresh bool) {
	name := strings.TrimPrefix(full, "minecraft:")
	base, boxes := splitVariant(name)
	if airLike[base] || strings.HasPrefix(base, "light_block_") {
		return 0, false
	}
	flat := notCube(base)
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.byName[name]; ok {
		return v, false
	}
	if boxes != "" && len(t.list) >= variantBudget {
		name = base
		if v, ok := t.byName[name]; ok {
			return v, false
		}
	}
	// 255 distinct drawn block names is far more than a lab scene has; the
	// last slot absorbs overflow instead of growing the wire format.
	if len(t.list) >= 255 {
		return 255, false
	}
	color, ok := known[base]
	if !ok {
		color, ok = family(base)
	}
	if !ok {
		h := fnv.New32a()
		_, _ = h.Write([]byte(base))
		s := h.Sum32()
		color = "#" + hex2(60+byte(s)%150) + hex2(60+byte(s>>8)%150) + hex2(60+byte(s>>16)%150)
	}
	alpha := float32(1)
	if a, ok := translucent[base]; ok {
		alpha = a
	}
	id = uint8(len(t.list) + 1)
	t.list = append(t.list, material{ID: id, Name: name, Color: color, Alpha: alpha, NonCube: flat})
	t.byName[name] = id
	return id, true
}

func (t *materialTable) all() []material {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]material(nil), t.list...)
}

func hex2(b byte) string {
	const d = "0123456789abcdef"
	return string([]byte{d[b>>4], d[b&15]})
}

// family colours the long tail of block names by what they are made of, so a
// scene is not speckled with hash colours.
func family(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, "_ore"):
		return "#7d7d7d", true
	case strings.HasSuffix(name, "leaves"):
		return "#3b7a2b", true
	case strings.HasSuffix(name, "planks"):
		return "#a8864f", true
	case strings.HasSuffix(name, "log"):
		return "#6b5230", true
	case strings.Contains(name, "copper"):
		return "#b87333", true
	}
	return "", false
}
