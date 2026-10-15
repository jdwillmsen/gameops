package camera

import (
	"hash/fnv"
	"strings"
	"sync"
)

// material is what the browser draws a block as. The spike has no texture
// atlas: a colour per block name, jittered per block in the browser, is enough
// to judge whether the view reads as a first-person scene.
type material struct {
	ID    uint8   `json:"id"`
	Name  string  `json:"name"`
	Color string  `json:"color"`
	Alpha float32 `json:"alpha"`
}

var known = map[string]string{
	"grass_block": "#5d9c3a", "dirt": "#866043", "coarse_dirt": "#775437", "stone": "#7d7d7d",
	"cobblestone": "#6e6e6e", "sand": "#dbd3a0", "sandstone": "#d6cb8f", "gravel": "#857f7c",
	"bedrock": "#2b2b2b", "water": "#3050c0", "flowing_water": "#3050c0", "lava": "#e0601a",
	"oak_log": "#6b5230", "birch_log": "#d5cfbf", "spruce_log": "#3b2a14", "oak_planks": "#a8864f",
	"oak_leaves": "#3b7a2b", "birch_leaves": "#6d9a49", "spruce_leaves": "#2e5a35",
	"snow": "#f4f8f8", "ice": "#8fb8f0", "clay": "#a0a6b4", "deepslate": "#4d4d52",
	"granite": "#9a6b57", "diorite": "#c4c4c4", "andesite": "#888888", "coal_ore": "#444444",
	"iron_ore": "#a68a78", "grass_path": "#8f7a3f", "mycelium": "#6f5f72", "podzol": "#5a4020",
	"red_sand": "#b5622a", "terracotta": "#9a5d42", "tuff": "#5c5e55",
}

var translucent = map[string]float32{"water": .6, "flowing_water": .6, "ice": .8}

// Block names that are not full cubes. Drawing them as cubes would bury the
// scene in solid green, so the spike leaves them out.
var nonCube = []string{"grass", "fern", "flower", "sapling", "vine", "dead_bush", "deadbush", "torch",
	"carpet", "tallgrass", "double_plant", "rail", "button", "sign", "fire", "web", "mushroom", "sweet_berry",
	"kelp", "seagrass", "litter", "bush", "peony", "lilac", "bluet", "reeds", "bud", "cluster", "lichen", "bubble_column", "pointed", "stairs", "trapdoor", "fence", "wall", "lily", "snow_layer", "tulip", "poppy", "dandelion", "rose", "allium"}

var airLike = map[string]bool{"air": true, "cave_air": true, "void_air": true, "unknown": true}

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
	if airLike[name] {
		return 0, false
	}
	for _, s := range nonCube {
		if strings.Contains(name, s) && name != "grass_block" && name != "snow" {
			return 0, false
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.byName[name]; ok {
		return v, false
	}
	// 255 distinct drawn block names is far more than a lab scene has; the
	// last slot absorbs overflow instead of growing the wire format.
	if len(t.list) >= 255 {
		return 255, false
	}
	color, ok := known[name]
	if !ok {
		color, ok = family(name)
	}
	if !ok {
		h := fnv.New32a()
		_, _ = h.Write([]byte(name))
		s := h.Sum32()
		color = "#" + hex2(60+byte(s)%150) + hex2(60+byte(s>>8)%150) + hex2(60+byte(s>>16)%150)
	}
	alpha := float32(1)
	if a, ok := translucent[name]; ok {
		alpha = a
	}
	id = uint8(len(t.list) + 1)
	t.list = append(t.list, material{ID: id, Name: name, Color: color, Alpha: alpha})
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
