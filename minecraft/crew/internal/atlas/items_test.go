package atlas

import (
	"bytes"
	"context"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func itemPixel(t *testing.T, a *Atlas, r Rect) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(a.ItemPNG()))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	x := int((r[0] + r[2]) / 2 * float32(b.Dx()))
	y := int((r[1] + r[3]) / 2 * float32(b.Dy()))
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestItemSheetUsesTheItemTexture(t *testing.T) {
	a := buildFixture(t)
	items := a.Items()
	if got := itemPixel(t, a, items.Items["minecraft:gem"]); got != (color.NRGBA{200, 30, 60, 255}) {
		t.Errorf("gem = %v", got)
	}
	// A tall strip contributes its first frame.
	if got := itemPixel(t, a, items.Items["minecraft:wand"]); got != (color.NRGBA{30, 200, 60, 255}) {
		t.Errorf("wand = %v", got)
	}
	// The item's own picture wins over the block of the same name.
	if got := itemPixel(t, a, items.Items["minecraft:torch"]); got != (color.NRGBA{240, 200, 20, 255}) {
		t.Errorf("torch = %v", got)
	}
}

// A shared key's later list entries are reachable under the item names their
// files are named for, including the renamed wooden_ and colour_bed forms.
func TestItemSheetNamesListedPicturesByFile(t *testing.T) {
	a := buildFixture(t)
	items := a.Items()
	for name, want := range map[string]color.NRGBA{
		"minecraft:wood_staff":   {90, 60, 30, 255},
		"minecraft:wooden_staff": {90, 60, 30, 255},
		"minecraft:bed_teal":     {20, 150, 150, 255},
		"minecraft:teal_bed":     {20, 150, 150, 255},
		"minecraft:gem":          {200, 30, 60, 255},
	} {
		r, ok := items.Items[name]
		if !ok {
			t.Errorf("%s has no picture", name)
			continue
		}
		if got := itemPixel(t, a, r); got != want {
			t.Errorf("%s pixel = %v, want %v", name, got, want)
		}
	}
}

func TestItemSheetFallsBackToTheBlockFace(t *testing.T) {
	a := buildFixture(t)
	want := sample(t, a, block(t, a, "minecraft:stone").Side.UV)
	r, ok := a.Items().Items["minecraft:stone"]
	if !ok {
		t.Fatal("stone has no item entry")
	}
	if got := itemPixel(t, a, r); got != want {
		t.Errorf("stone = %v, want the block face %v", got, want)
	}
	if _, ok := a.Items().Items["minecraft:six_sided"]; !ok {
		t.Error("a block with six faces has no item entry")
	}
}

func TestItemSheetLeavesUnknownItemsToThePlaceholder(t *testing.T) {
	a := buildFixture(t)
	items := a.Items()
	if _, ok := items.Items["minecraft:ember"]; ok {
		t.Error("an item whose texture is missing got an entry")
	}
	if _, ok := items.Items["minecraft:nothing"]; ok {
		t.Error("an invented item got an entry")
	}
	if got := itemPixel(t, a, items.Placeholder); got != (color.NRGBA{0x8b, 0x8b, 0x8b, 255}) {
		t.Errorf("placeholder = %v", got)
	}
	img, _ := png.Decode(bytes.NewReader(a.ItemPNG()))
	if items.Width != img.Bounds().Dx() || items.Height != img.Bounds().Dy() || items.TileSize != 4 {
		t.Errorf("table %dx%d tile %d vs image %v", items.Width, items.Height, items.TileSize, img.Bounds())
	}
}

func TestItemSheetWithoutAnItemIndexStillHasBlocks(t *testing.T) {
	pack := t.TempDir()
	if err := os.CopyFS(pack, os.DirFS("testdata/pack")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(pack, "textures", "item_texture.json")); err != nil {
		t.Fatal(err)
	}
	a, err := BuildWithOptions(pack, Options{TileSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Items().Items["minecraft:stone"]; !ok {
		t.Error("no block entries without an item index")
	}
}

func TestItemSheetSurvivesTheCache(t *testing.T) {
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	opts := Options{TileSize: 4}
	first, _, err := Ensure(context.Background(), gh.fetcher(), "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatal(err)
	}
	gh.Close()
	second, _, err := Ensure(context.Background(), gh.fetcher(), "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatalf("cached ensure: %v", err)
	}
	if !bytes.Equal(first.ItemPNG(), second.ItemPNG()) || !reflect.DeepEqual(first.Items(), second.Items()) {
		t.Error("cached item sheet differs")
	}
}

// An entry saved before the sheet existed has no way to produce one, so it
// must read as missing and be rebuilt.
func TestLoadAtlasRejectsAnEntryWithoutItems(t *testing.T) {
	dir := t.TempDir()
	if err := buildFixture(t).Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, itemsTableFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAtlas(dir); err == nil {
		t.Error("loaded an atlas with no item table")
	}
}
