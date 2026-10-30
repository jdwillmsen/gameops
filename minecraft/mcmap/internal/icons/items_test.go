package icons

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// syntheticItems is the game's list of its items, with entries that are
// no item's id among them.
const syntheticItems = `{"data_items": [
  {"name": "minecraft:zinc_blade"},
  {"name": "minecraft:glimmer_orb"},
  {"name": "minecraft:wooden_zap"},
  {"name": "minecraft:golden_zap"},
  {"name": "minecraft:cooked_gloop"},
  {"name": "minecraft:moss_boat"},
  {"name": "minecraft:music_disc_hum"},
  {"name": "minecraft:beef"},
  {"name": "minecraft:rubble"},
  {"name": "minecraft:striped_log"},
  {"name": "minecraft:lush_fern"},
  {"name": "minecraft:glow_cube"},
  {"name": "minecraft:ghost_item"},
  {"name": "minecraft:silly_head"},
  {"name": "minecraft:cobble_spawn_egg"},
  {"name": "minecraft:ZINC"},
  {"name": "elsewhere:zinc_blade"},
  {"name": "minecraft:../../etc"}
]}`

const syntheticBlocks = `{"format_version": "1.1.0",
  "rubble": {"sound": "stone", "textures": "rubble_flat"},
  "striped_log": {"textures": {"up": "striped_top", "down": "striped_top", "side": "striped_side"}},
  "lush_fern": {"textures": "lush_fern_leaf"},
  "glow_cube": {"textures": "glow_cube"},
  "silly_head": {"textures": "rubble_flat"},
  "ghost_item": {"textures": "not_in_the_atlas"}
}`

const itemTerrain = `{"texture_data": {
  "rubble_flat": {"textures": "textures/blocks/rubble"},
  "striped_top": {"textures": "textures/blocks/striped_top"},
  "striped_side": {"textures": ["textures/blocks/striped_side", "textures/blocks/striped_side_wet"]},
  "lush_fern_leaf": {"textures": "textures/blocks/lush_fern_leaf"},
  "glow_cube": {"textures": "textures/blocks/glow_cube"}
}}`

// withItems adds to the synthetic samples what an item's picture is made
// from. Every name here is made up.
func withItems(t *testing.T, s *samples) {
	t.Helper()
	s.set(itemListPath, []byte(syntheticItems))
	s.set(blocksPath, []byte(syntheticBlocks))
	s.set(terrainPath, []byte(itemTerrain))
	atlas := string(s.files[atlasPath])
	s.set(atlasPath, []byte(strings.Replace(atlas, `{"texture_data": {`, `{"texture_data": {
  "glimmer_orb": {"textures": "textures/items/orb_of_glimmer"},`, 1)))
	for path, c := range map[string]color.NRGBA{
		"items/zinc_blade": red, "items/orb_of_glimmer": green, "items/wood_zap": blue, "items/gold_zap": yellow,
		"items/gloop_cooked": red, "items/boat_moss": green, "items/record_hum": blue, "items/beef_raw": yellow,
		"blocks/rubble": red, "blocks/striped_top": green, "blocks/striped_side": blue, "blocks/lush_fern_leaf": green,
	} {
		s.set("resource_pack/textures/"+path+".png", picture(t, 16, 16, c))
	}
	// An animated texture is its frames one above the next.
	frames := image.NewNRGBA(image.Rect(0, 0, 16, 48))
	for y := range 48 {
		for x := range 16 {
			frames.SetNRGBA(x, y, []color.NRGBA{red, green, blue}[y/16])
		}
	}
	s.set("resource_pack/textures/blocks/glow_cube.png", asPNG(t, frames))
}

func TestTheItemListIsReadByIdAndNothingElse(t *testing.T) {
	ids, err := parseItemIDs([]byte(syntheticItems))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"beef", "cobble_spawn_egg", "cooked_gloop", "ghost_item", "glimmer_orb", "glow_cube", "golden_zap", "lush_fern", "moss_boat", "music_disc_hum", "rubble", "silly_head", "striped_log", "wooden_zap", "zinc_blade"}
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	for name, raw := range map[string]string{
		"not JSON":      `{"data_items": [`,
		"no items":      `{"data_items": []}`,
		"another shape": `{"items": ["zinc_blade"]}`,
		"too many":      `{"data_items": [` + strings.Repeat(`{"name": "minecraft:a"},`, maxItems) + `{"name": "minecraft:b"}]}`,
	} {
		if _, err := parseItemIDs([]byte(raw)); err == nil {
			t.Errorf("a list that is %s was read", name)
		}
	}
}

func TestTheBlocksFileIsReadWithoutItsFormatVersion(t *testing.T) {
	blocks, err := parseBlocks([]byte(syntheticBlocks))
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := blocks["format_version"]; kept || len(blocks) != 6 {
		t.Errorf("%d entries read, among them the version: %v", len(blocks), kept)
	}
	if _, err := parseBlocks([]byte(`{"a": `)); err == nil {
		t.Error("a file that is not JSON was read")
	}
}

func TestAnItemsTextureIsFoundByTheNamesTheGameGivesIt(t *testing.T) {
	src := itemSources{
		items: atlas{Textures: map[string]struct {
			Textures json.RawMessage `json:"textures"`
		}{
			"glimmer_orb": {json.RawMessage(`"textures/items/orb_of_glimmer"`)},
			"many":        {json.RawMessage(`["textures/items/many_a", "textures/items/many_b"]`)},
		}},
		textures: map[string]bool{},
	}
	for _, path := range []string{"items/zinc_blade", "items/orb_of_glimmer", "items/wood_zap", "items/gold_zap", "items/gloop_cooked", "items/boat_moss",
		"items/record_hum", "items/beef_raw", "items/dye_powder_red", "items/many_a", "items/bucket_empty", "items/sign_darkoak"} {
		src.textures["textures/"+path] = true
	}
	for id, want := range map[string]string{
		// Its own name, the atlas's entry of that name, and the older words
		// for a material.
		"zinc_blade":     "textures/items/zinc_blade",
		"glimmer_orb":    "textures/items/orb_of_glimmer",
		"wooden_zap":     "textures/items/wood_zap",
		"golden_zap":     "textures/items/gold_zap",
		"cooked_gloop":   "textures/items/gloop_cooked",
		"moss_boat":      "textures/items/boat_moss",
		"music_disc_hum": "textures/items/record_hum",
		// Names that follow no rule, which the game itself holds.
		"beef":          "textures/items/beef_raw",
		"red_dye":       "textures/items/dye_powder_red",
		"bucket":        "textures/items/bucket_empty",
		"dark_oak_sign": "textures/items/sign_darkoak",
		// An atlas entry of several textures is a list of other items'.
		"many": "",
		// No texture the samples hold.
		"ghost_item": "",
	} {
		if got := src.texture(id); got != want {
			t.Errorf("%s is drawn with %q, want %q", id, got, want)
		}
	}
}

func TestABlocksItemIsItsSideAndATextureTheGameColoursInPlaceIsTinted(t *testing.T) {
	src := itemSources{textures: map[string]bool{}}
	if err := json.Unmarshal([]byte(itemTerrain), &src.terrain); err != nil {
		t.Fatal(err)
	}
	var err error
	if src.blocks, err = parseBlocks([]byte(syntheticBlocks)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"rubble", "striped_top", "striped_side", "lush_fern_leaf", "glow_cube"} {
		src.textures["textures/blocks/"+path] = true
	}
	for id, want := range map[string]string{
		"rubble":      "textures/blocks/rubble",
		"striped_log": "textures/blocks/striped_side",
		"glow_cube":   "textures/blocks/glow_cube",
		// Drawn grey in the samples; the picture multiplies it by a colour.
		"lush_fern": "textures/blocks/lush_fern_leaf",
		// Listed by a texture the atlas does not hold.
		"ghost_item":  "",
		"not_a_block": "",
	} {
		if got := src.blockTexture(id); got != want {
			t.Errorf("%s is drawn with %q, want %q", id, got, want)
		}
	}
	// A texture the listing does not have is not asked for.
	delete(src.textures, "textures/blocks/rubble")
	if got := src.blockTexture("rubble"); got != "" {
		t.Errorf("a block whose texture the samples lack is drawn with %q", got)
	}
}

func TestFetchMakesAPictureForEveryItemWhoseTextureIsKnown(t *testing.T) {
	s := newSamples(t)
	withItems(t, s)
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]color.NRGBA{
		"item/zinc_blade": red, "item/glimmer_orb": green, "item/wooden_zap": blue, "item/golden_zap": yellow,
		"item/cooked_gloop": red, "item/moss_boat": green, "item/music_disc_hum": blue, "item/beef": yellow,
		"item/rubble": red, "item/striped_log": blue, "item/glow_cube": red,
	}
	for key, colour := range want {
		raw, ok := set.Pictures[key]
		if !ok {
			t.Errorf("no %s: missing %v, rejected %v", key, set.Missing, set.Rejected)
			continue
		}
		if got := colourOf(t, raw); got != colour {
			t.Errorf("%s is %v, want %v", key, got, colour)
		}
	}
	for _, key := range []string{"item/lush_fern", "item/ghost_item", "item/silly_head", "item/cobble_spawn_egg", "item/zinc"} {
		if _, made := set.Pictures[key]; made {
			t.Errorf("%s was made though no picture of it is to be had", key)
		}
	}
	// One frame of the animation, and not the strip.
	if cfg, err := png.DecodeConfig(bytes.NewReader(set.Pictures["item/glow_cube"])); err != nil || cfg.Width != 16 || cfg.Height != 16 {
		t.Errorf("an animated texture is %dx%d, want its first frame, 16x16 (%v)", cfg.Width, cfg.Height, err)
	}
	if recipe, ok := set.Recipes["item/zinc_blade"]; !ok || recipe.Flat != "textures/items/zinc_blade" {
		t.Errorf("the recipe kept for an item is %+v", recipe)
	}
	if len(set.Missing) != 0 {
		t.Errorf("missing = %v: an item with no texture is not one to ask for again", set.Missing)
	}
	// A page asks for an item by a key that may be as long as an id is.
	long := "item/" + strings.Repeat("a", 64)
	if !pictureKey.MatchString(long) || pictureKey.MatchString(long+"a") || pictureKey.MatchString("item/Zinc") {
		t.Error("the shape of a picture's key does not allow the longest id and no more")
	}
}

func TestAnItemWhoseTextureCouldNotBeFetchedIsMadeLaterFromItsRecipeAlone(t *testing.T) {
	s := newSamples(t)
	withItems(t, s)
	var up atomic.Bool
	var asked []string
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/textures/items/zinc_blade.png") && !up.Load() {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if up.Load() {
			s.mu.Lock()
			asked = append(asked, r.URL.Path)
			s.mu.Unlock()
		}
		inner.ServeHTTP(w, r)
	})
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(set.Missing, "item/zinc_blade") || !slices.Contains(set.Unreached, "item/zinc_blade") {
		t.Fatalf("missing %v, unreached %v: the item is not marked to be asked for again", set.Missing, set.Unreached)
	}
	if _, ok := set.Pictures["item/glimmer_orb"]; !ok {
		t.Error("another item's picture was lost with it")
	}
	up.Store(true)
	got, err := s.source().Fill(t.Context(), set.Missing, set.Recipes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Pictures["item/zinc_blade"]; !ok || len(got.Missing) != 0 || got.Replanned {
		t.Errorf("after the source came back: pictures %v, missing %v, replanned %v", keys(got.Pictures), got.Missing, got.Replanned)
	}
	if slices.ContainsFunc(asked, func(path string) bool { return !strings.Contains(path, "/resource_pack/textures/") }) {
		t.Errorf("asked for %v, want textures and no list, listing or file of blocks", asked)
	}
}

func TestWithoutTheItemListNoItemHasAPictureAndNothingElseIsLost(t *testing.T) {
	s := newSamples(t)
	withItems(t, s)
	s.mu.Lock()
	delete(s.files, itemListPath)
	s.mu.Unlock()
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key := range set.Pictures {
		if strings.HasPrefix(key, "item/") {
			t.Errorf("%s was made from no list of items", key)
		}
	}
	if _, ok := set.Pictures["structure/fortress"]; !ok || len(set.Missing) != 0 {
		t.Errorf("the rest of the pictures did not survive: missing %v", set.Missing)
	}
}

func TestWithTheItemListOutOfReachNoPictureIsMadeYetAndAllAreAskedForAgain(t *testing.T) {
	s := newSamples(t)
	withItems(t, s)
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "mojang-items.json") {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		inner.ServeHTTP(w, r)
	})
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Faces and blocks are made from the same plan, so they wait for it too.
	if !slices.Contains(set.Missing, artPlan) || !slices.Contains(set.Unreached, artPlan) {
		t.Errorf("missing %v, unreached %v: the plan is not marked to be made again", set.Missing, set.Unreached)
	}
	if len(set.Mobs) == 0 {
		t.Error("the mob icons were lost with it")
	}
}

func TestMoreItemsThanAnIndexIsReadBackWithAreNotMade(t *testing.T) {
	recipes := map[string]Recipe{}
	for n := range maxFaceRecipes {
		recipes[fmt.Sprintf("face/m%d", n)] = Recipe{Flat: fmt.Sprintf("textures/items/f%d", n)}
	}
	items := map[string]Recipe{}
	for n := range maxRecipes + 500 {
		items[fmt.Sprintf("item/i%d", n)] = Recipe{Flat: "textures/items/x"}
	}
	rejected := map[string]string{}
	addItems(recipes, rejected, items)
	// Each recipe makes a picture, beside the plain ones, and the two together
	// are what an index is read back with.
	if got := len(recipes) + len(pictureKeys()); got != maxPictures || len(recipes) > maxRecipes {
		t.Errorf("%d recipes and %d plain pictures, want %d pictures in all", len(recipes), len(pictureKeys()), maxPictures)
	}
	if want := len(items) - (maxPictures - len(pictureKeys()) - maxFaceRecipes); len(rejected) != want {
		t.Errorf("%d items left out, want %d", len(rejected), want)
	}
	// Those left out are the last by key, the same ones every time.
	if _, made := recipes["item/i0"]; !made {
		t.Error("the items that fit were not the first by key")
	}
	// And the set they make is one that can be kept and read back.
	set := Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Pictures: map[string][]byte{}, Recipes: recipes, Rejected: rejected}
	tiny := picture(t, 1, 1, red)
	for key := range recipes {
		set.Pictures[key] = tiny
	}
	for _, key := range pictureKeys() {
		set.Pictures[key] = tiny
	}
	if err := (&Mobs{Dir: t.TempDir(), Ref: testRef, Logger: quiet()}).store(set); err != nil || len(set.Pictures) > maxPictures {
		t.Errorf("a full set of %d pictures was refused: %v", len(set.Pictures), err)
	}
}

func TestItemsAreLeftOutBeforeTheFacesAndBlocksWhenTheTexturesRunOut(t *testing.T) {
	recipes := map[string]Recipe{"face/cow": {Flat: "textures/items/cow"}}
	items := map[string]Recipe{}
	for n := range maxModelTextures + 10 {
		items[fmt.Sprintf("item/i%05d", n)] = Recipe{Flat: fmt.Sprintf("textures/items/t%d", n)}
	}
	// One that shares a texture already named costs the fetch nothing more.
	items["item/zz_twin"] = Recipe{Flat: "textures/items/cow"}
	rejected := map[string]string{}
	addItems(recipes, rejected, items)
	textures := map[string]bool{}
	for _, r := range recipes {
		textures[r.Flat] = true
	}
	if len(textures) != maxModelTextures {
		t.Errorf("%d textures named, want the %d a making may name", len(textures), maxModelTextures)
	}
	if _, made := recipes["item/zz_twin"]; !made {
		t.Error("an item whose texture was already named was left out")
	}
	// The face names one of the textures, so one item fewer than the ten over fits.
	if _, made := recipes["face/cow"]; !made || len(rejected) != 11 {
		t.Errorf("the face was lost or %d items were left out, want 11", len(rejected))
	}
}

func TestSomeItemsAreKnownToBeWronglyPicturedAndAreLeftWithout(t *testing.T) {
	src := itemSources{textures: map[string]bool{"textures/items/leather_chestplate": true, "textures/items/leather": true, "textures/blocks/trip_wire": true, "textures/blocks/trip_wire_source": true}}
	if err := json.Unmarshal([]byte(`{"texture_data": {"trip_wire": {"textures": "textures/blocks/trip_wire"}}}`), &src.terrain); err != nil {
		t.Fatal(err)
	}
	src.ids = []string{"leather", "leather_chestplate", "tripwire_hook"}
	src.blocks = map[string]blockEntry{"tripwire_hook": {json.RawMessage(`"trip_wire"`)}}
	got := src.recipes()
	// Leather armour is a grey base the dye is laid over, and leather is not armour.
	if _, made := got["item/leather_chestplate"]; made {
		t.Error("dyed armour was drawn as its undyed base")
	}
	if got["item/leather"].Flat != "textures/items/leather" {
		t.Errorf("leather is drawn with %q", got["item/leather"].Flat)
	}
	// The hook is the source of the wire and not the string.
	if got["item/tripwire_hook"].Flat != "textures/blocks/trip_wire_source" {
		t.Errorf("a tripwire hook is drawn with %q", got["item/tripwire_hook"].Flat)
	}
	for id, texture := range itemTextures {
		if notPictured(id) {
			t.Errorf("%s is in the table of names and is never drawn", id)
		}
		if strings.HasPrefix(texture, "textures/") && !modelTexture.MatchString(texture) {
			t.Errorf("%s names %q, which is no texture this would ask for", id, texture)
		}
	}
}

func TestAnItemListThatCannotBeReadIsSaidAndNotLeftOutInSilence(t *testing.T) {
	s := newSamples(t)
	withItems(t, s)
	s.set(itemListPath, []byte(`{"data_items": [`))
	var said bytes.Buffer
	source := s.source()
	source.Logger = slog.New(slog.NewTextHandler(&said, nil))
	set, err := source.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said.String(), "the list of items could not be read") {
		t.Errorf("an unreadable list was not said; the log is %q", said.String())
	}
	if _, ok := set.Pictures["item/zinc_blade"]; ok || len(set.Missing) != 0 {
		t.Errorf("items were made from no list, or something was left missing: %v", set.Missing)
	}
}

func TestAnAnimatedFlatTextureIsItsFirstFrameAndAnOddStackIsRefused(t *testing.T) {
	strip := func(w, h int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				img.SetNRGBA(x, y, color.NRGBA{uint8(y / w), 0, 0, 255})
			}
		}
		return img
	}
	r := Recipe{Flat: "textures/blocks/x"}
	got, err := r.render(map[string]*image.NRGBA{r.Flat: strip(16, 64)})
	if err != nil || got.Rect.Dx() != 16 || got.Rect.Dy() != 16 || got.NRGBAAt(got.Rect.Min.X, got.Rect.Min.Y).R != 0 {
		t.Errorf("a stack of four frames came out %v, %v", got.Rect, err)
	}
	// Not a whole number of frames: it is not an animation.
	if got, err := r.render(map[string]*image.NRGBA{r.Flat: strip(16, 40)}); err != nil || got.Rect.Dy() != 40 {
		t.Errorf("a texture of 16x40 was cut to %v (%v)", got.Rect, err)
	}
	// Frames that are themselves too large are still too large.
	if _, err := r.render(map[string]*image.NRGBA{r.Flat: strip(128, 256)}); err == nil {
		t.Error("a picture over the limit on a side was made")
	}
}

func TestAnIndexFromBeforeTheMarksForHealthAndHungerAsksForThemAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	first := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Entities: []string{"cow"},
			Pictures: map[string][]byte{"container/chest": picture(t, 16, 16, green)}}, nil
	}}
	first.Run(t.Context())
	path := filepath.Join(first.home(), indexFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]any
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	if idx["vitals"] != true {
		t.Fatal("this version writes no note of the marks; the test would prove nothing")
	}
	// An index that says the marks were asked for is not asked again.
	set, _, err := (&Mobs{Dir: dir, Ref: testRef}).load()
	if err != nil || slices.ContainsFunc(set.Missing, func(key string) bool { return strings.HasPrefix(key, "ui/") }) {
		t.Fatalf("an index written with the marks asked for them again: %v, %v", set.Missing, err)
	}
	delete(idx, "vitals")
	raw, _ = json.Marshal(idx)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	set, _, err = (&Mobs{Dir: dir, Ref: testRef}).load()
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	for _, p := range vitalPictures {
		asked = append(asked, p.name)
	}
	slices.Sort(asked)
	var missing []string
	for _, key := range set.Missing {
		if strings.HasPrefix(key, "ui/") {
			missing = append(missing, key)
		}
	}
	if !slices.Equal(missing, asked) {
		t.Errorf("an older index misses %v, want %v", missing, asked)
	}
	if _, held := set.Pictures["container/chest"]; !held || len(set.Mobs) != 1 {
		t.Error("what the older index held was lost")
	}
}

func TestTheMarksForHealthAndHungerAreFetchedAsTheyCame(t *testing.T) {
	set, err := newSamples(t).source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ui/heart_container", "ui/heart", "ui/heart_half", "ui/hunger_container", "ui/hunger", "ui/hunger_half", "ui/absorption", "ui/absorption_half"} {
		raw, ok := set.Pictures[key]
		if !ok {
			t.Errorf("no %s: missing %v", key, set.Missing)
			continue
		}
		if cfg, err := png.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != 9 || cfg.Height != 9 {
			t.Errorf("%s is %dx%d (%v), want 9x9", key, cfg.Width, cfg.Height, err)
		}
	}
}

func TestABiomeColouredTextureIsMultipliedByAPlainsColourAndNoOtherIsTinted(t *testing.T) {
	for _, c := range []struct{ id, path, want string }{
		{"short_grass", "textures/blocks/tallgrass", plainsGrass},
		{"tall_grass", "textures/blocks/double_plant_grass_bottom", plainsGrass},
		{"fern", "textures/blocks/fern.tga", plainsGrass},
		{"oak_leaves", "textures/blocks/leaves_oak.tga", plainsFoliage},
		{"birch_leaves", "textures/blocks/leaves_birch.tga", birchFoliage},
		{"spruce_leaves", "textures/blocks/leaves_spruce.tga", spruceFoliage},
		{"waterlily", "textures/blocks/waterlily", lilyPad},
		// A flower's stalk keeps its own green.
		{"sunflower", "textures/blocks/double_plant_sunflower_bottom", ""},
		{"lilac", "textures/blocks/double_plant_syringa_bottom", ""},
		// The game does not colour these in place.
		{"cherry_leaves", "textures/blocks/leaves_cherry", ""},
		{"azalea_leaves", "textures/blocks/leaves_azalea", ""},
		{"pale_oak_leaves", "textures/blocks/pale_oak_leaves", ""},
		{"cobblestone", "textures/blocks/cobblestone", ""},
	} {
		if got := tintOf(c.id, c.path); got != c.want {
			t.Errorf("%s drawn from %s is tinted %q, want %q", c.id, c.path, got, c.want)
		}
	}
	if r := flatOrTinted("oak_leaves", "textures/blocks/leaves_oak.tga"); r.Flat != "" || len(r.Layers) != 1 || r.Layers[0].Tint != plainsFoliage || r.check() != nil {
		t.Errorf("a leaf is not a valid tinted layer: %+v", r)
	}
	if r := flatOrTinted("cobblestone", "textures/blocks/cobblestone"); r.Flat != "textures/blocks/cobblestone" || r.check() != nil {
		t.Errorf("an uncoloured block is not drawn as it is: %+v", r)
	}
}

func TestAShieldAndAGrassBlockAreDrawnFromTheirModelsAndNotAFlatTexture(t *testing.T) {
	src := itemSources{
		ids:      []string{"shield", "grass_block"},
		textures: map[string]bool{"textures/blocks/grass_side_carried": true, "textures/blocks/grass_top": true},
	}
	if err := json.Unmarshal([]byte(`{"texture_data":{"shield":{"textures":"textures/entity/shield"},"grass_top":{"textures":["textures/blocks/grass_top"]}}}`), &src.items); err != nil {
		t.Fatal(err)
	}
	src.terrain = src.items
	recipes := src.recipes()
	shield, ok := recipes[itemKey("shield")]
	if !ok || shield.check() != nil || shield.Layers[0].Texture != "textures/entity/shield" || shield.Layers[0].Src != [4]int{1, 1, 12, 22} {
		t.Errorf("the shield is %+v", shield)
	}
	grass, ok := recipes[itemKey("grass_block")]
	if !ok || grass.check() != nil || grass.Block == nil || grass.Block.Top[0].Tint != plainsGrass || grass.Block.Left[0].Tint != "" {
		t.Errorf("the grass block is %+v", grass)
	}
	// With no model texture named, there is no shield to draw.
	src.items = atlas{}
	if _, ok := src.recipes()[itemKey("shield")]; ok {
		t.Error("a shield is drawn from a texture the atlas does not name")
	}
}

func TestATextureKeptAsATGAIsHeldByItsPath(t *testing.T) {
	src := itemSources{textures: map[string]bool{"textures/blocks/fern.tga": true, "textures/blocks/dirt": true}}
	for path, want := range map[string]string{
		"textures/blocks/fern": "textures/blocks/fern.tga",
		"textures/blocks/dirt": "textures/blocks/dirt",
		"textures/blocks/none": "",
		"":                     "",
	} {
		if got := src.held(path); got != want {
			t.Errorf("held(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLayeredAndCubeItemsCountEveryTextureTheyNameAgainstTheLimit(t *testing.T) {
	recipes := map[string]Recipe{}
	items := map[string]Recipe{}
	// Each names two textures of its own, none of them as a flat picture.
	for n := range maxModelTextures {
		layer := func(name string) []Layer {
			return []Layer{{Texture: name, Units: [2]int{16, 16}, Src: [4]int{0, 0, 16, 16}, Dst: [4]int{0, 0, 16, 16}}}
		}
		items[fmt.Sprintf("item/c%05d", n)] = Recipe{Block: &Block{
			W: 16, H: 16, D: 16,
			Top: layer(fmt.Sprintf("textures/blocks/t%d", n)), Left: layer(fmt.Sprintf("textures/blocks/l%d", n)), Right: layer(fmt.Sprintf("textures/blocks/t%d", n)),
		}}
	}
	rejected := map[string]string{}
	addItems(recipes, rejected, items)
	textures := map[string]bool{}
	for _, r := range recipes {
		for _, path := range r.textures() {
			textures[path] = true
		}
	}
	if len(textures) > maxModelTextures {
		t.Errorf("%d textures named, over the %d a making may name", len(textures), maxModelTextures)
	}
	if len(rejected) == 0 {
		t.Error("no cube was left out though together they name twice the limit")
	}
}
