package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Where an item's picture is worked out from: the list of every item the
// game has, the blocks file that says which texture a block is drawn with,
// and the textures themselves. The item texture atlas is keyed by the
// game's older names and is no list of items, so none of the three can be
// left out.
const (
	itemListPath = "metadata/vanilladata_modules/mojang-items.json"
	blocksPath   = "resource_pack/blocks.json"

	// The real list is 0.4 MB of about 1,600 items and the blocks file
	// 0.2 MB of about 1,350; the limits are several times that.
	maxItemListBytes = 2 << 20
	maxBlocksBytes   = 2 << 20
	maxItems         = 4000
	maxBlocks        = 6000
	// maxListedTextures bounds the item and block textures a listing may
	// name, of which the real one has some 3,600.
	maxListedTextures = 30_000
)

var itemID = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// itemKey is the key of an item's picture.
func itemKey(id string) string { return "item/" + id }

// parseItemIDs reads the ids of the items out of the game's list of them,
// without the minecraft: prefix. An entry that is not a plain id is passed
// over.
func parseItemIDs(raw []byte) ([]string, error) {
	var list struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"data_items"`
	}
	if err := json.Unmarshal(stripComments(raw), &list); err != nil {
		return nil, err
	}
	if len(list.Items) > maxItems {
		return nil, fmt.Errorf("the item list holds %d items, over the limit of %d", len(list.Items), maxItems)
	}
	seen := map[string]bool{}
	for _, item := range list.Items {
		if id, ok := strings.CutPrefix(item.Name, "minecraft:"); ok && itemID.MatchString(id) {
			seen[id] = true
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("the item list holds no items; it is not laid out as expected")
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// blockEntry is the part of a block's entry in the blocks file that says
// which textures it is drawn with: the name of one entry of the terrain
// atlas for all its sides, or a name for each side.
type blockEntry struct {
	Textures json.RawMessage `json:"textures"`
}

func parseBlocks(raw []byte) (map[string]blockEntry, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(stripComments(raw), &entries); err != nil {
		return nil, err
	}
	if len(entries) > maxBlocks {
		return nil, fmt.Errorf("the blocks file holds %d blocks, over the limit of %d", len(entries), maxBlocks)
	}
	blocks := make(map[string]blockEntry, len(entries))
	for id, entry := range entries {
		// The file opens with its format version, which is no block.
		var block blockEntry
		if itemID.MatchString(id) && json.Unmarshal(entry, &block) == nil && block.Textures != nil {
			blocks[id] = block
		}
	}
	return blocks, nil
}

// itemSources is everything an item's picture is worked out from.
type itemSources struct {
	ids    []string
	blocks map[string]blockEntry
	// items and terrain are the atlases of item and block textures.
	items, terrain atlas
	// textures is every item and block texture the samples hold, as a path
	// under resource_pack without the extension.
	textures map[string]bool
}

// itemTextures are the items whose texture is named nothing like them: a
// name under textures/items, or a whole path where it is a block's. The game
// keeps this knowledge in itself and not in the samples.
var itemTextures = map[string]string{
	"beef": "beef_raw", "chicken": "chicken_raw", "porkchop": "porkchop_raw", "mutton": "mutton_raw",
	"cod": "fish_raw", "salmon": "fish_salmon_raw", "tropical_fish": "fish_clownfish_raw", "pufferfish": "fish_pufferfish_raw",
	"cooked_cod": "fish_cooked", "cooked_salmon": "fish_salmon_cooked",
	"rabbit": "rabbit_raw", "cooked_rabbit": "rabbit_cooked",
	"lapis_lazuli": "dye_powder_blue", "bone_meal": "dye_powder_white", "ink_sac": "dye_powder_black", "cocoa_beans": "dye_powder_brown",
	"glow_ink_sac": "dye_powder_glow", "redstone": "redstone_dust", "slime_ball": "slimeball", "turtle_scute": "turtle_shell_piece",
	"sugar_cane": "reeds", "melon_slice": "melon", "glistering_melon_slice": "melon_speckled",
	"totem_of_undying": "totem", "iron_chain": "chain", "fire_charge": "fireball", "firework_rocket": "fireworks",
	"glass_bottle": "potion_bottle_empty", "potion": "potion_bottle_drinkable", "splash_potion": "potion_bottle_splash", "lingering_potion": "potion_bottle_lingering",
	"bucket": "bucket_empty", "lodestone_compass": "lodestonecompass_item", "compass": "compass_item", "clock": "clock_item",
	"minecart": "minecart_normal", "tnt_minecart": "minecart_tnt", "bow": "bow_standby", "crossbow": "crossbow_standby", "fishing_rod": "fishing_rod_uncast",
	"book": "book_normal", "writable_book": "book_writable", "empty_map": "map_empty", "filled_map": "map_filled",
	"tripwire_hook":          "textures/blocks/trip_wire_source",
	"enchanted_golden_apple": "apple_golden", "golden_apple": "apple_golden", "popped_chorus_fruit": "chorus_fruit_popped",
	"oak_sign": "sign", "dark_oak_sign": "sign_darkoak", "tropical_fish_bucket": "bucket_tropical", "music_disc_5": "record_5",
	"white_dye": "dye_powder_white_new", "black_dye": "dye_powder_black_new", "blue_dye": "dye_powder_blue_new", "brown_dye": "dye_powder_brown_new",
	"orange_dye": "dye_powder_orange", "magenta_dye": "dye_powder_magenta", "light_blue_dye": "dye_powder_light_blue", "yellow_dye": "dye_powder_yellow",
	"lime_dye": "dye_powder_lime", "pink_dye": "dye_powder_pink", "gray_dye": "dye_powder_gray", "light_gray_dye": "dye_powder_silver",
	"cyan_dye": "dye_powder_cyan", "purple_dye": "dye_powder_purple", "green_dye": "dye_powder_green", "red_dye": "dye_powder_red",
}

// notPictured are the items the samples hold a texture for that is not a
// picture of them: a head is drawn from the sand it is a particle of, and a
// legacy bed or banner is the plank or the pattern it is made of. Leather
// armour is dyed, and its texture is the grey base the dye is laid over, which
// is not the colour of any piece a player wears.
func notPictured(id string) bool {
	return id == "bed" || id == "banner" || id == "grindstone" || id == "beacon" || strings.HasPrefix(id, "leather_") ||
		strings.HasSuffix(id, "_head") || strings.HasSuffix(id, "_skull") || strings.HasSuffix(id, "_spawn_egg")
}

// itemSpellings are the names an item's texture may go by: the id, the id
// with a word moved from one end to the other (cooked_beef is beef_cooked,
// oak_boat is boat_oak), and each of those with the game's older word for
// the material (wooden is wood, golden is gold).
func itemSpellings(id string) []string {
	words := strings.Split(id, "_")
	out := []string{id}
	if disc, ok := strings.CutPrefix(id, "music_disc_"); ok {
		out = append(out, "record_"+disc)
	}
	if len(words) > 1 {
		out = append(out,
			strings.Join(append(slices.Clone(words[1:]), words[0]), "_"),
			strings.Join(append([]string{words[len(words)-1]}, words[:len(words)-1]...), "_"))
	}
	for _, spelling := range slices.Clone(out) {
		for _, older := range [][2]string{{"wooden_", "wood_"}, {"golden_", "gold_"}} {
			if rest, ok := strings.CutPrefix(spelling, older[0]); ok {
				out = append(out, older[1]+rest)
			}
		}
	}
	return out
}

// tinted are the block textures the game colours by where they stand, which
// are drawn grey in the samples and are multiplied by a colour to be a picture.
var tinted = regexp.MustCompile(`leaves|tallgrass|fern|vine|grass_top|double_plant|waterlily`)

// Colours the game gives a block that is coloured by its biome, taken as a
// plains biome's, since a slot has no biome to ask. A leaf the game does not
// colour is drawn as it is.
const (
	plainsGrass   = "#79c05a"
	plainsFoliage = "#48b518"
	birchFoliage  = "#80a755"
	spruceFoliage = "#619961"
	lilyPad       = "#208030"
)

// tintOf is the colour a block texture is multiplied by to be a picture, or
// "" where the texture is already in its own colours.
func tintOf(id, path string) string {
	if !tinted.MatchString(path) {
		return ""
	}
	switch {
	case strings.Contains(id, "cherry") || strings.Contains(id, "azalea") || strings.Contains(id, "pale_oak") || strings.Contains(id, "poplar"):
		return ""
	case strings.HasPrefix(id, "birch"):
		return birchFoliage
	case strings.HasPrefix(id, "spruce"):
		return spruceFoliage
	case id == "waterlily":
		return lilyPad
	case id == "sunflower" || id == "lilac" || id == "rose_bush" || id == "peony":
		// The game leaves a flower's stalk in its own green.
		return ""
	case strings.Contains(path, "grass") || strings.Contains(path, "fern"):
		return plainsGrass
	}
	return plainsFoliage
}

// single is the texture an atlas entry names when it names exactly one and
// that is a texture this service would ask for.
func (a atlas) single(name string, allowed *regexp.Regexp) string {
	entry, ok := a.Textures[name]
	if !ok {
		return ""
	}
	var one string
	if json.Unmarshal(entry.Textures, &one) != nil || !allowed.MatchString(one) {
		return ""
	}
	return one
}

// held is the texture file the samples hold for a path, as the path or as a
// TGA's, or "" where there is none.
func (src itemSources) held(path string) string {
	switch {
	case path == "":
		return ""
	case src.textures[path]:
		return path
	case src.textures[path+tgaSuffix]:
		return path + tgaSuffix
	}
	return ""
}

// blockTexture is the texture a block is drawn flat with: the one it has
// for all its sides, or its side where it has one for each.
func (src itemSources) blockTexture(id string) string {
	entry, ok := src.blocks[id]
	if !ok {
		return ""
	}
	var names []string
	var one string
	var sides map[string]string
	switch {
	case json.Unmarshal(entry.Textures, &one) == nil:
		names = []string{one}
	case json.Unmarshal(entry.Textures, &sides) == nil:
		order := []string{"side", "south", "north", "east", "west", "up", "down"}
		// A tall plant's side is the face of its flower; the stalk is below.
		if strings.Contains(sides["side"], "additional") {
			order = []string{"down", "up"}
		}
		for _, side := range order {
			if sides[side] != "" {
				names = append(names, sides[side])
			}
		}
	}
	for _, name := range names {
		if path := src.held(src.terrain.pathIn(name, 0, modelTexture)); path != "" {
			return path
		}
	}
	return ""
}

// texture is the texture an item is drawn with, or "" where the samples do
// not hold one. The item's own texture is preferred to its block's, since
// a door in the hand is not the door in the world.
func (src itemSources) texture(id string) string {
	spellings := itemSpellings(id)
	if name, ok := itemTextures[id]; ok {
		if strings.HasPrefix(name, "textures/") {
			if src.textures[name] {
				return name
			}
		} else {
			spellings = append([]string{name}, spellings...)
		}
	}
	for _, name := range spellings {
		if path := "textures/items/" + name; src.textures[path] {
			return path
		}
		if path := src.items.single(name, texturePath); path != "" && src.textures[path] {
			return path
		}
	}
	if path := src.blockTexture(id); path != "" {
		return path
	}
	// A block the blocks file does not list is still often an entry of the
	// terrain atlas by its own id.
	if _, listed := src.blocks[id]; !listed {
		if path := src.held(src.terrain.single(id, modelTexture)); path != "" {
			return path
		}
	}
	return ""
}

// recipes is a picture for every item whose texture is known, as the
// texture itself. A spawn egg is left to the mob it hatches, which the
// page draws as the mob's own icon.
func (src itemSources) recipes() map[string]Recipe {
	out := map[string]Recipe{}
	for _, id := range src.ids {
		if notPictured(id) {
			continue
		}
		if recipe, ok := src.special(id); ok {
			out[itemKey(id)] = recipe
		} else if path := src.texture(id); path != "" {
			out[itemKey(id)] = flatOrTinted(id, path)
		}
	}
	return out
}

// flatOrTinted is the picture of a texture drawn as it is, or multiplied by
// the colour the game lays on it. A coloured one is a layer of a 16 unit
// square, mostly air where it is a plant.
func flatOrTinted(id, path string) Recipe {
	tint := tintOf(id, path)
	if tint == "" {
		return Recipe{Flat: path}
	}
	return Recipe{
		W: boxSide, H: boxSide, Main: [4]int{0, 0, boxSide, boxSide}, Sparse: true,
		Layers: []Layer{{Texture: path, Units: [2]int{boxSide, boxSide}, Src: [4]int{0, 0, boxSide, boxSide}, Dst: [4]int{0, 0, boxSide, boxSide}, Tint: tint}},
	}
}

// shieldSheet is the model texture a shield is drawn from. The item texture
// atlas names it for the shield, which is a model, not a flat texture.
const shieldSheet = "textures/entity/shield"

// special is the picture of an item the game draws from a model or a tinted
// cube, which no single flat texture is a picture of.
func (src itemSources) special(id string) (Recipe, bool) {
	switch id {
	case "shield":
		// The front of the shield is the face of its plate: 12 wide and 22
		// tall, one unit in from the corner of a 64 unit texture.
		if src.items.single("shield", modelTexture) != shieldSheet {
			return Recipe{}, false
		}
		return Recipe{
			W: 12, H: 22, Main: [4]int{0, 0, 12, 22},
			Layers: []Layer{{Texture: shieldSheet, Units: [2]int{64, 64}, Src: [4]int{1, 1, 12, 22}, Dst: [4]int{0, 0, 12, 22}}},
		}, true
	case "grass_block":
		// The side as the game draws it in a hand is a PNG with its fringe
		// already green; the world's side is a TGA laid over dirt.
		top, side := src.terrain.pathIn("grass_top", 0, modelTexture), "textures/blocks/grass_side_carried"
		if src.held(top) == "" || !src.textures[side] {
			return Recipe{}, false
		}
		face := func(path, tint string) []Layer {
			return []Layer{{Texture: path, Units: [2]int{boxSide, boxSide}, Src: [4]int{0, 0, boxSide, boxSide}, Dst: [4]int{0, 0, boxSide, boxSide}, Tint: tint}}
		}
		return Recipe{Block: &Block{W: boxSide, H: boxSide, D: boxSide, Top: face(top, plainsGrass), Left: face(side, ""), Right: face(side, "")}}, true
	}
	return Recipe{}, false
}

// addItems adds the items' recipes to the rest, in the order of their keys.
// They come last and are the first left out: every recipe makes a picture,
// which with those served as they came must stay within what an index is read
// back with, or the whole set is refused and fetched again at every start;
// and one making may name only so many textures, over which none of the faces
// and blocks would be made either.
func addItems(recipes map[string]Recipe, rejected map[string]string, items map[string]Recipe) {
	room := min(maxRecipes, maxPictures-len(pictureKeys()))
	textures := map[string]bool{}
	for _, r := range recipes {
		for _, path := range append(r.textures(), r.Else) {
			if path != "" {
				textures[path] = true
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(items)) {
		// A tinted layer or a cube names more than the one texture a flat
		// picture does, and every one of them counts.
		var fresh []string
		for _, path := range items[key].textures() {
			if !textures[path] {
				fresh = append(fresh, path)
			}
		}
		switch {
		case len(recipes) >= room:
			rejected[key] = fmt.Sprintf("over the limit of %d made pictures", room)
		case len(textures)+len(fresh) > maxModelTextures:
			rejected[key] = fmt.Sprintf("over the limit of %d textures", maxModelTextures)
		default:
			recipes[key] = items[key]
			for _, path := range fresh {
				textures[path] = true
			}
		}
	}
}

// itemSources reads what the items' pictures are worked out from. It says
// whether the source could be asked: one that could not leaves the whole
// plan for another try. A list or file the pin does not hold, or holds in
// a form this does not read, leaves the items without pictures and nothing
// else.
func (s *Source) itemSources(ctx context.Context, ls listing, items, terrain atlas, total *budget) (itemSources, bool) {
	src := itemSources{items: items, terrain: terrain, textures: ls.textures}
	// Without the listing of textures there is no telling which exist.
	if ls.textures == nil {
		return src, true
	}
	list, err := s.get(ctx, s.raw(itemListPath), maxItemListBytes, total)
	if err != nil {
		return src, errors.Is(err, errSettled)
	}
	if src.ids, err = parseItemIDs(list); err != nil {
		s.logger().Warn("the list of items could not be read; no item has a picture", "error", tidyReason(err.Error()))
		return src, true
	}
	raw, err := s.get(ctx, s.raw(blocksPath), maxBlocksBytes, total)
	switch {
	case err == nil:
		// One that does not parse lists no block, which leaves the items
		// that are blocks without a picture, as ones the file did not name.
		if src.blocks, err = parseBlocks(raw); err != nil {
			s.logger().Warn("the blocks file could not be read; items that are blocks have no picture", "error", tidyReason(err.Error()))
		}
	case !errors.Is(err, errSettled):
		return src, false
	}
	return src, true
}
