package icons

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

var (
	lidColour   = color.NRGBA{120, 40, 160, 255}
	baseColour  = color.NRGBA{60, 20, 80, 255}
	otherColour = color.NRGBA{255, 0, 255, 255}
	clear       = color.NRGBA{}
)

// bedColour is the synthetic colour of the bed texture at an index of the
// atlas's list, so a test can tell which one a colour was given.
func bedColour(index int) color.NRGBA { return color.NRGBA{uint8(10 + 15*index), 90, 30, 255} }

func bedTextures() string {
	var paths []string
	for _, colour := range markers.Colours {
		paths = append(paths, fmt.Sprintf("%q", "textures/items/bed_"+legacyColour(colour)))
	}
	return strings.Join(paths, ", ")
}

// modelSheet is a synthetic stand-in for a shulker box's model texture:
// the two faces the icon is made of in their own colours, every other part
// of the sheet in a third, and one column of the lid's face left clear, as
// the real lid is where the base shows through it.
func modelSheet(t testing.TB, scale int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64*scale, 64*scale))
	fill := func(r image.Rectangle, c color.NRGBA) {
		r = image.Rect(r.Min.X*scale, r.Min.Y*scale, r.Max.X*scale, r.Max.Y*scale)
		draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(image.Rect(0, 0, 64, 64), otherColour)
	fill(image.Rect(16, 16, 32, 28), lidColour)
	fill(image.Rect(16, 44, 32, 52), baseColour)
	fill(image.Rect(19, 16, 20, 28), clear)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const syntheticLang = `## A synthetic language file. Nothing here is the game's own text.
entity.cow.name=Synthetic Cow
entity.villager_v2.name=Synthetic Villager
entity.evocation_illager.name=Synthetic Evoker
entity.cow.hint=Not a name
feature.fortress=Synthetic Fortress
feature.pillager_outpost=Synthetic Outpost
feature.village=Synthetic Village
tile.chest.name=Synthetic Chest
tile.trapped_chest.name=Synthetic Trapped Chest
tile.barrel.name=Synthetic Barrel
tile.bed.name=Synthetic Bed
tile.shulkerBox.name=Synthetic Shulker Box
tile.shulkerBoxSilver.name=Synthetic Light Gray Shulker Box
tile.shulkerBoxLightBlue.name=Synthetic Light Blue Shulker Box
item.bed.silver.name=Synthetic Light Gray Bed
item.bed.lightBlue.name=Synthetic Light Blue Bed
item.bed.red.name=Synthetic Red Bed	## a trailing comment
menu.play=Play
`

// syntheticTerrain names a block's sides the way the terrain atlas does:
// one path, or a list of them of which the first is the block at rest.
const syntheticTerrain = `// header comment
{"texture_data": {
  "chest_inventory_top": {"textures": ["textures/blocks/chest_top"]},
  "chest_inventory_side": {"textures": ["textures/blocks/chest_side"]},
  "chest_inventory_front": {"textures": ["textures/blocks/chest_front"]},
  "trapped_chest_inventory_front": {"textures": ["textures/blocks/trapped_chest_front"]},
  "ender_chest_inventory_top": {"textures": ["textures/blocks/ender_chest_top"]},
  "ender_chest_inventory_side": {"textures": ["textures/blocks/ender_chest_side"]},
  "ender_chest_inventory_front": {"textures": ["textures/blocks/ender_chest_front"]},
  "barrel_top": {"textures": ["textures/blocks/barrel_top", "textures/blocks/barrel_top_open"]},
  "barrel_side": {"textures": ["textures/blocks/barrel_side", "textures/blocks/barrel_side"]},
  "mob_spawner": {"textures": "textures/blocks/mob_spawner"},
  "vault_top": {"textures": ["textures/blocks/vault_top"]},
  "vault_front": {"textures": ["textures/blocks/vault_front_off", "textures/blocks/vault_front_on"]},
  "vault_side": {"textures": ["textures/blocks/vault_side_off"]},
  "bell_carried": {"textures": "textures/items/villagebell"}
}}`

// markerFiles is everything a fetch reads beyond the mob icons, all of it
// synthetic.
func markerFiles(t testing.TB) map[string][]byte {
	t.Helper()
	files := map[string][]byte{
		"resource_pack/textures/blocks/chest_front.png":         picture(t, 16, 16, yellow),
		"resource_pack/textures/blocks/trapped_chest_front.png": picture(t, 16, 16, red),
		"resource_pack/textures/blocks/barrel_side.png":         picture(t, 16, 16, green),
		"resource_pack/textures/items/compass_item.png":         picture(t, 16, 16, blue),
		langPath: []byte(syntheticLang),
	}
	for index, colour := range markers.Colours {
		files["resource_pack/textures/items/bed_"+legacyColour(colour)+".png"] = picture(t, 16, 16, bedColour(index))
		files["resource_pack/textures/entity/shulker/shulker_"+legacyColour(colour)+".png"] = modelSheet(t, 1)
	}
	files["resource_pack/textures/entity/shulker/shulker_undyed.png"] = modelSheet(t, 1)
	for kind, art := range structureArts {
		files["resource_pack/"+art.item+".png"] = picture(t, 16, 16, yellow)
		if art.icon != "" {
			// A sheet of marks is 64 a side, and a mark of its own 8.
			side := 8
			if art.cut != [4]int{} {
				side = mapSheet
			}
			files["resource_pack/"+art.icon+".png"] = picture(t, side, side, green)
		}
		for _, variant := range art.variants {
			files["resource_pack/textures/map/"+kind+"_"+variant+".png"] = picture(t, 8, 8, blue)
		}
	}
	// A bell is drawn from its item, which must look like something.
	files["resource_pack/textures/items/villagebell.png"] = asPNG(t, painted(16, 16))
	// What the blocks are drawn from: the atlas that names each side's
	// texture, those textures, and a bed's model texture in each colour.
	files[terrainPath] = []byte(syntheticTerrain)
	for _, name := range []string{"chest_top", "chest_side", "ender_chest_top", "ender_chest_side", "ender_chest_front", "barrel_top", "mob_spawner", "vault_top", "vault_front_off", "vault_side_off"} {
		files["resource_pack/textures/blocks/"+name+".png"] = picture(t, 16, 16, green)
	}
	for _, colour := range markers.Colours {
		files["resource_pack/textures/entity/bed/"+legacyColour(colour)+".png"] = picture(t, 64, 64, yellow)
	}
	return files
}

func pixel(t testing.TB, raw []byte, x, y int) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

// everyPicture is the key of each picture a whole fetch gives.
func everyPicture() []string {
	keys := []string{"container/chest", "container/trapped_chest", "container/barrel", "marker/waypoint", "shulker/undyed"}
	for _, colour := range markers.Colours {
		keys = append(keys, "bed/"+colour, "shulker/"+colour)
	}
	for _, kind := range StructureKinds {
		keys = append(keys, "structure/"+kind)
		for _, variant := range structureArts[kind].variants {
			keys = append(keys, "structure/"+kind+"_"+variant)
		}
	}
	keys = append(keys, blockKeys()...)
	slices.Sort(keys)
	return keys
}

func TestFetchReadsAPictureForEveryMarkerAndStructure(t *testing.T) {
	s := newSamples(t)
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := keys(set.Pictures), everyPicture(); !slices.Equal(slices.Sorted(slices.Values(got)), want) {
		t.Errorf("pictures = %v\nwant       %v", slices.Sorted(slices.Values(got)), want)
	}
	if len(set.Missing) != 0 {
		t.Errorf("missing = %v, want nothing", set.Missing)
	}
	// A bed's colour is its place in the atlas's list, which is the number
	// the world stores for it: red is 14 and light grey, spelt silver, 8.
	for colour, index := range map[string]int{"white": 0, "light_gray": 8, "red": 14, "black": 15} {
		if got := colourOf(t, set.Pictures["bed/"+colour]); got != bedColour(index) {
			t.Errorf("bed/%s is the texture coloured %v, want the one at index %d", colour, got, index)
		}
	}
	if got := colourOf(t, set.Pictures["container/trapped_chest"]); got != red {
		t.Errorf("the trapped chest is %v, want its own front", got)
	}
	if len(set.Mobs) != 3 {
		t.Errorf("mob icons = %v", keys(set.Mobs))
	}
}

// The listing is rationed by address, and everything a fetch reads beyond
// the mob icons is asked for by a path known ahead.
func TestFetchAsksForOneListingOnly(t *testing.T) {
	s := newSamples(t)
	var listings, files int
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if strings.HasPrefix(r.URL.Path, "/list/") {
			listings++
		} else {
			files++
		}
		s.mu.Unlock()
		inner.ServeHTTP(w, r)
	})
	if _, err := s.source().Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The atlas, ten definitions and the three textures they name, the
	// pictures served as they come and the language file, then the
	// terrain atlas and each texture a made picture is made from: one
	// request each, and no model, since the listing names none.
	plain := len(pictureKeys())
	// A kind's mark, and each of its others, is fetched beside its item.
	marks := 0
	for _, art := range structureArts {
		if art.icon != "" {
			marks += 1 + len(art.variants)
		}
	}
	made := len(structureArts) + marks + 13 + 17 + 16
	if want := 1 + 10 + 3 + plain + 1 + 1 + made; listings != 1 || files != want {
		t.Errorf("%d listing requests and %d file requests, want 1 and %d", listings, files, want)
	}
}

func TestShulkerIconIsTheBoxSeenFromTheSideNotItsModelTexture(t *testing.T) {
	for _, scale := range []int{1, 2, 4} {
		icon, err := shulkerIcon(modelSheet(t, scale))
		if err != nil {
			t.Fatalf("scale %d: %v", scale, err)
		}
		img, err := png.Decode(bytes.NewReader(icon))
		if err != nil {
			t.Fatal(err)
		}
		if got := img.Bounds(); got != image.Rect(0, 0, 16*scale, 16*scale) {
			t.Fatalf("scale %d: icon is %v, want %d a side", scale, got, 16*scale)
		}
		for name, at := range map[string]struct {
			x, y int
			want color.NRGBA
		}{
			"the lid at the top":                      {0, 0, lidColour},
			"the lid where it comes over the base":    {0, 11, lidColour},
			"the base below the lid":                  {0, 12, baseColour},
			"the base at the bottom":                  {15, 15, baseColour},
			"nothing above the base behind a gap":     {3, 0, clear},
			"the base through a gap in the lid":       {3, 9, baseColour},
			"the far edge of the lid":                 {15, 0, lidColour},
			"the base under the last row of the lid ": {3, 11, baseColour},
		} {
			if got := pixel(t, icon, at.x*scale, at.y*scale); got != at.want {
				t.Errorf("scale %d: %s is %v, want %v", scale, name, got, at.want)
			}
		}
		for y := range 16 * scale {
			for x := range 16 * scale {
				if pixel(t, icon, x, y) == otherColour {
					t.Fatalf("scale %d: the icon holds a part of the sheet that is neither face, at %d,%d", scale, x, y)
				}
			}
		}
	}
}

func TestShulkerIconRefusesWhatIsNotAModelTexture(t *testing.T) {
	bomb := declaring(modelSheet(t, 1), 60000, 60000)
	for name, raw := range map[string][]byte{
		"rubbish":                                []byte("<html>rate limited</html>"),
		"a flat item texture":                    picture(t, 16, 16, red),
		"not square":                             picture(t, 64, 32, red),
		"not a whole multiple of the model":      picture(t, 96, 96, red),
		"larger than gives a small icon":         picture(t, maxSheetSide+64, maxSheetSide+64, red),
		"a declared size the data does not back": bomb,
		"empty":                                  {},
	} {
		if icon, err := shulkerIcon(raw); err == nil {
			t.Errorf("%s gave an icon of %d bytes", name, len(icon))
		}
	}
}

func TestMarkerPicturesAreReencodedToo(t *testing.T) {
	const smuggled = "<script>alert(1)</script>"
	s := newSamples(t)
	flat := withText(t, picture(t, 16, 16, yellow), smuggled)
	sheet := withText(t, modelSheet(t, 1), smuggled)
	s.set("resource_pack/textures/blocks/chest_front.png", flat)
	s.set("resource_pack/textures/entity/shulker/shulker_red.png", sheet)
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, original := range map[string][]byte{"container/chest": flat, "shulker/red": sheet} {
		got := set.Pictures[key]
		if len(got) == 0 || bytes.Contains(got, []byte(smuggled)) || bytes.Equal(got, original) {
			t.Errorf("%s was kept as it came, text chunk and all", key)
		}
	}
}

// A pin can lack a file, or hold something that is not the picture it
// should be. That costs the one picture and nothing else: the mob icons
// and every other picture are still served, and the page keeps its ring
// for the one that is not.
func TestAMarkerPictureThePinDoesNotHoldIsLeftOutAlone(t *testing.T) {
	var jpgLike = []byte("\xff\xd8\xff\xe0 not a png")
	for name, harm := range map[string]func(*samples){
		"absent":    func(s *samples) { delete(s.files, "resource_pack/textures/items/compass_item.png") },
		"not a PNG": func(s *samples) { s.files["resource_pack/textures/items/compass_item.png"] = jpgLike },
		"too large a side": func(s *samples) {
			s.files["resource_pack/textures/items/compass_item.png"] = picture(t, maxIconSide+1, 16, red)
		},
		"over the byte limit": func(s *samples) {
			s.files["resource_pack/textures/items/compass_item.png"] = append(picture(t, 16, 16, red), make([]byte, maxTextureBytes)...)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSamples(t)
			s.mu.Lock()
			harm(s)
			s.mu.Unlock()
			set, err := s.source().Fetch(t.Context())
			if err != nil {
				t.Fatalf("the fetch failed for one picture: %v", err)
			}
			if _, held := set.Pictures["marker/waypoint"]; held {
				t.Error("a picture was kept that the source did not give")
			}
			if !slices.Equal(set.Missing, []string{"marker/waypoint"}) {
				t.Errorf("missing = %v, want the one picture", set.Missing)
			}
			if len(set.Pictures) != len(everyPicture())-1 || len(set.Mobs) != 3 || len(set.Lang) == 0 {
				t.Errorf("%d pictures, %d mob icons, %d names: the rest did not survive", len(set.Pictures), len(set.Mobs), len(set.Lang))
			}
		})
	}
}

func TestABedTheAtlasDoesNotListIsLeftOut(t *testing.T) {
	s := newSamples(t)
	atlas := s.files["resource_pack/textures/item_texture.json"]
	s.set("resource_pack/textures/item_texture.json", bytes.Replace(atlas, []byte(`"bed":`), []byte(`"cot":`), 1))
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Missing) != len(markers.Colours) || set.Missing[0] != "bed/black" {
		t.Errorf("missing = %v, want every bed", set.Missing)
	}
	if _, held := set.Pictures["bed/red"]; held {
		t.Error("a bed has a picture the atlas named no texture for")
	}
}

// Unreachable is not the same as absent, and neither is a reason to throw
// away the mob icons the same fetch has just read: the fetch succeeds,
// and what could not be asked for is named as that, to be tried again.
func TestAPictureOrTheNamesTheSourceFailsOnDoesNotCostTheMobIcons(t *testing.T) {
	for path, what := range map[string]string{"/textures/blocks/barrel_side.png": "container/barrel", "/texts/en_US.lang": langPath} {
		s := newSamples(t)
		inner := s.srv.Config.Handler
		s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, path) {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			inner.ServeHTTP(w, r)
		})
		set, err := s.source().Fetch(t.Context())
		if err != nil {
			t.Fatalf("a fetch that could not reach %s failed whole: %v", path, err)
		}
		if len(set.Mobs) != 3 {
			t.Errorf("%d mob icons kept when %s could not be reached, want all 3", len(set.Mobs), path)
		}
		if !slices.Contains(set.Unreached, what) || !slices.Contains(set.Missing, what) {
			t.Errorf("%s is not named as unreached: missing %v, unreached %v", what, set.Missing, set.Unreached)
		}
		if _, held := set.Pictures[what]; held {
			t.Errorf("%s is held without having been fetched", what)
		}
		for _, other := range set.Unreached {
			if !slices.Contains(set.Missing, other) {
				t.Errorf("%s is unreached and not missing", other)
			}
		}
	}
}

// A village is the game's own mark for one, and a kind with none is its
// mob's face or its item: which of the three is one line of the table.
func TestAStructureIsDrawnAsTheFirstOfItsMarkItsMobsFaceAndItsItem(t *testing.T) {
	face := Recipe{W: 8, H: 8, Main: [4]int{0, 0, 8, 8}, Layers: []Layer{{Texture: "textures/entity/made_up", Src: [4]int{0, 0, 8, 8}, Dst: [4]int{0, 0, 8, 8}}}}
	got := structureRecipes(map[string]Recipe{"face/blaze": face, "face/villager_v2": face})
	if r := got["structure/village"]; r.Flat != "textures/map/village_plains" || r.Else != "textures/items/villagebell" || len(r.Layers) != 0 {
		t.Errorf("a village is drawn by %+v, not as the map's own mark with its bell to fall back on", r)
	}
	for _, biome := range []string{"desert", "savanna", "snowy", "taiga"} {
		if r := got["structure/village_"+biome]; r.Flat != "textures/map/village_"+biome || r.Else != "textures/map/village_plains" {
			t.Errorf("a %s village is drawn by %+v", biome, r)
		}
	}
	if r := got["structure/fortress"]; len(r.Layers) != 1 || r.Else != "textures/items/netherbrick" {
		t.Errorf("a fortress is drawn by %+v, not as its mob's face with its item to fall back on", r)
	}
	// A mansion's mark is one of a sheet of them.
	if r := got["structure/mansion"]; len(r.Layers) != 1 || r.Layers[0].Texture != "textures/map/map_icons" || r.Layers[0].Src != [4]int{32, 48, 16, 16} || !r.Sparse || r.Else != "textures/items/totem" {
		t.Errorf("a mansion is drawn by %+v, not as its mark cut from the sheet", r)
	}
	if _, head := got["structure/mansion"].head(16); head {
		t.Error("a mark cut from the sheet is taken for a face with a head")
	}
	for kind, mark := range map[string]string{"witch_hut": "swamp_hut", "trial_chamber": "trial_chambers", "desert_pyramid": "desert_pyramid", "jungle_temple": "jungle_temple", "ancient_city": "ancient_city"} {
		if r := got["structure/"+kind]; r.Flat != "textures/map/"+mark || r.Else == "" {
			t.Errorf("%s is drawn by %+v, not as the map's mark for it", kind, r)
		}
	}
	// No face was made for a pillager, and a stronghold has no mob.
	for kind, item := range map[string]string{"outpost": "textures/items/crossbow_standby", "stronghold": "textures/items/ender_eye"} {
		if r := got["structure/"+kind]; r.Flat != item || r.Else != "" {
			t.Errorf("%s is drawn by %+v, not as its item", kind, r)
		}
	}
	for _, kind := range StructureKinds {
		if _, listed := structureArts[kind]; !listed {
			t.Errorf("%s has no line in the table", kind)
		}
		if err := got["structure/"+kind].check(); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if len(got) != len(StructureKinds)+4 {
		t.Errorf("%d recipes for %d kinds and a village's four other marks", len(got), len(StructureKinds))
	}
}
