package icons

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"regexp"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

// legacyColour is a colour as the samples' file names and language keys
// spell it: the game's older word for light grey.
func legacyColour(colour string) string {
	if colour == "light_gray" {
		return "silver"
	}
	return colour
}

var (
	// ContainerKinds is what a container marker can be: its k, and
	// trapped_chest for a chest marked t.
	ContainerKinds = []string{"chest", "trapped_chest", "barrel", "shulker"}
	// StructureKinds is every kind of structure the map marks.
	StructureKinds = []string{"fortress", "monument", "outpost", "witch_hut", "village", "stronghold", "trial_chamber",
		"desert_pyramid", "jungle_temple", "igloo", "trail_ruins", "abandoned_camp",
		"end_city", "end_gateway", "exit_portal", "bastion", "ruined_portal",
		"mansion", "ancient_city", "shipwreck", "ocean_ruins", "buried_treasure"}
)

// A picture's key is its group and its name within it: bed/red,
// shulker/undyed, container/chest, structure/monument, marker/waypoint.
var pictureKey = regexp.MustCompile(`^[a-z]{1,16}/[a-z0-9_]{1,32}$`)

// maxPictures is more than the 250 or so there are, and bounds what an index on
// the volume can make a start read.
const maxPictures = 800

// structureArt is what one kind of structure is drawn as. The first of
// these the samples hold is its picture: the game's own mark for the kind
// on an explorer's map, the face of the mob met there and nowhere else,
// and an item that could be nothing else's. The README says why each was
// chosen.
type structureArt struct {
	// icon is under resource_pack/textures/map, a mark 8 pixels a side
	// with its own dark outline.
	icon string
	// face is a mob's type.
	face string
	// item is under resource_pack, and is what a kind with neither is.
	item string
	// variants are the kind's other marks, by the word the map has for
	// where one stands: a village's by the biome it is built in. Each is
	// structure/<kind>_<variant>, beside structure/<kind>.
	variants []string
}

// structureArts is every kind of structure's picture, a line a kind. A
// kind added to StructureKinds is given its line here and nothing else.
var structureArts = map[string]structureArt{
	"fortress":      {face: "blaze", item: "textures/items/netherbrick"},
	"monument":      {face: "elder_guardian", item: "textures/items/prismarine_shard"},
	"outpost":       {face: "pillager", item: "textures/items/crossbow_standby"},
	"witch_hut":     {face: "witch", item: "textures/items/cauldron"},
	"village":       {icon: "textures/map/village_plains", item: "textures/items/villagebell", variants: []string{"desert", "savanna", "snowy", "taiga"}},
	"stronghold":    {item: "textures/items/ender_eye"},
	"trial_chamber": {face: "breeze", item: "textures/items/trial_key"},
	// The carved face only a desert pyramid's sandstone has, and the stone
	// a jungle temple is built of.
	"desert_pyramid": {item: "textures/blocks/sandstone_carved"},
	"jungle_temple":  {item: "textures/blocks/cobblestone_mossy"},
	"igloo":          {item: "textures/items/snowball"},
	"trail_ruins":    {item: "textures/items/brush"},
	"abandoned_camp": {item: "textures/items/campfire"},
	// What each is gone to for, or is made of and nothing else is.
	"end_city":        {face: "shulker", item: "textures/items/elytra"},
	"end_gateway":     {item: "textures/items/ender_pearl"},
	"exit_portal":     {item: "textures/blocks/dragon_egg"},
	"bastion":         {face: "piglin_brute", item: "textures/blocks/gilded_blackstone"},
	"ruined_portal":   {item: "textures/blocks/crying_obsidian"},
	"mansion":         {face: "evocation_illager", item: "textures/items/totem"},
	"ancient_city":    {face: "warden", item: "textures/items/echo_shard"},
	"shipwreck":       {item: "textures/items/boat_oak"},
	"ocean_ruins":     {face: "drowned", item: "textures/items/nautilus"},
	"buried_treasure": {item: "textures/items/heartofthesea_closed"},
}

// structureRecipes is the recipe for every structure's picture, given the
// faces there are recipes for.
func structureRecipes(faces map[string]Recipe) map[string]Recipe {
	out := map[string]Recipe{}
	for _, kind := range StructureKinds {
		art := structureArts[kind]
		recipe, faced := faces[faceKey(art.face)]
		switch {
		case art.icon != "":
			recipe = Recipe{Flat: art.icon, Else: art.item}
		case art.face != "" && faced:
			recipe.Else = art.item
		default:
			recipe = Recipe{Flat: art.item}
		}
		out["structure/"+kind] = recipe
		// A variant the samples do not hold is the kind's own mark.
		for _, variant := range art.variants {
			out["structure/"+kind+"_"+variant] = Recipe{Flat: "textures/map/" + kind + "_" + variant, Else: art.icon}
		}
	}
	return out
}

// markerPicture is one marker picture and where in the samples it comes from.
type markerPicture struct {
	// name is the picture's key: its group and its name within it.
	name string
	// path is under resource_pack, without the extension.
	path string
	// sheet marks a texture that is a model's unwrapped faces and not a
	// picture of the thing: a shulker box's.
	sheet bool
}

// pictures is every marker picture served as it came, and its source. Each is
// asked for by a path known ahead: none of these is listed anywhere the
// way a mob's spawn egg is, except a bed's, which the atlas lists by
// colour.
func pictures(items atlas) []markerPicture {
	out := []markerPicture{
		// A chest and a barrel have no item texture; the game draws the
		// block. The face with the latch, or the hoops, is the one that
		// says which block it is.
		{name: "container/chest", path: "textures/blocks/chest_front"},
		{name: "container/trapped_chest", path: "textures/blocks/trapped_chest_front"},
		{name: "container/barrel", path: "textures/blocks/barrel_side"},
		{name: "marker/waypoint", path: "textures/items/compass_item"},
	}
	// The atlas lists a bed's sixteen textures in the order the game
	// numbers the colours, which is the order of markers.Colours.
	for index, colour := range markers.Colours {
		if path := items.path("bed", index); path != "" {
			out = append(out, markerPicture{name: "bed/" + colour, path: path})
		}
		out = append(out, markerPicture{name: "shulker/" + colour, path: "textures/entity/shulker/shulker_" + legacyColour(colour), sheet: true})
	}
	out = append(out, markerPicture{name: "shulker/" + markers.Undyed, path: "textures/entity/shulker/shulker_" + markers.Undyed, sheet: true})
	return out
}

// pictureKeys is the key of every marker picture served as it came,
// whatever the atlas lists. A structure's is made from a recipe, with the
// faces and the blocks.
func pictureKeys() []string {
	keys := []string{"container/chest", "container/trapped_chest", "container/barrel", "marker/waypoint", "shulker/" + markers.Undyed}
	for _, colour := range markers.Colours {
		keys = append(keys, "bed/"+colour, "shulker/"+colour)
	}
	return keys
}

// A shulker box's texture is the faces of its model laid flat, 64 units a
// side: the lid a box 16 wide, 12 tall and 16 deep with its faces laid out
// from the corner, the base one 16 by 8 by 16 laid out from 28 units down.
// A box's faces are unwrapped as its top above a row of its four sides, so
// each front is 16 units in and as far down as the box is deep.
const (
	sheetUnits = 64
	// maxSheetSide allows a texture drawn at up to four times the usual
	// resolution, which is also the largest that gives an icon within
	// maxIconSide.
	maxSheetSide = sheetUnits * maxIconSide / boxSide

	boxSide  = 16
	lidTall  = 12
	baseTall = 8
)

var (
	lidFront  = image.Rect(16, 16, 16+boxSide, 16+lidTall)
	baseFront = image.Rect(16, 28+16, 16+boxSide, 28+16+baseTall)
)

// shulkerIcon draws a shulker box as it looks from the side when shut, out
// of the texture its model is wrapped in: the front of the base at the
// bottom and the front of the lid over it, the lid coming down over the
// top of the base as it does in the game. The result is this service's own
// encoding, as Clean's is.
func shulkerIcon(raw []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "png" {
		return nil, errNotPNG
	}
	if cfg.Width != cfg.Height || cfg.Width < sheetUnits || cfg.Width > maxSheetSide || cfg.Width%sheetUnits != 0 {
		return nil, fmt.Errorf("model texture is %dx%d, not a square of %d to %d a side", cfg.Width, cfg.Height, sheetUnits, maxSheetSide)
	}
	sheet, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errNotPNG
	}
	scale := cfg.Width / sheetUnits
	scaled := func(r image.Rectangle) image.Rectangle {
		return image.Rect(r.Min.X*scale, r.Min.Y*scale, r.Max.X*scale, r.Max.Y*scale).Add(sheet.Bounds().Min)
	}
	icon := image.NewNRGBA(image.Rect(0, 0, boxSide*scale, boxSide*scale))
	base, lid := scaled(baseFront), scaled(lidFront)
	draw.Draw(icon, image.Rect(0, (boxSide-baseTall)*scale, boxSide*scale, boxSide*scale), sheet, base.Min, draw.Src)
	draw.Draw(icon, image.Rect(0, 0, boxSide*scale, lidTall*scale), sheet, lid.Min, draw.Over)
	var out bytes.Buffer
	if err := png.Encode(&out, icon); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
