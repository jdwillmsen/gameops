package atlas

import (
	"bytes"
	"image/color"
	"image/png"
	"slices"
	"testing"
)

func buildFixture(t *testing.T) *Atlas {
	t.Helper()
	a, err := BuildWithOptions("testdata/pack", Options{TileSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// sample reads the atlas pixel at the centre of a UV rect.
func sample(t *testing.T, a *Atlas, r Rect) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(a.PNG()))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	x := int((r[0] + r[2]) / 2 * float32(b.Dx()))
	y := int((r[1] + r[3]) / 2 * float32(b.Dy()))
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func block(t *testing.T, a *Atlas, name string) Block {
	t.Helper()
	b, ok := a.Table().Blocks[name]
	if !ok {
		t.Fatalf("block %s missing from table", name)
	}
	return b
}

func TestBuildAtlasImage(t *testing.T) {
	a := buildFixture(t)
	img, err := png.Decode(bytes.NewReader(a.PNG()))
	if err != nil {
		t.Fatal(err)
	}
	tbl := a.Table()
	if tbl.TileSize != 4 || tbl.Width != img.Bounds().Dx() || tbl.Height != img.Bounds().Dy() {
		t.Fatalf("table dims %dx%d tile %d vs image %v", tbl.Width, tbl.Height, tbl.TileSize, img.Bounds())
	}
	if tbl.Width%4 != 0 || tbl.Height%4 != 0 {
		t.Fatalf("atlas %dx%d is not a whole number of tiles", tbl.Width, tbl.Height)
	}
}

func TestBuildResolvesFacesFromTextureKeys(t *testing.T) {
	a := buildFixture(t)

	stone := block(t, a, "minecraft:stone")
	for _, f := range []Face{stone.Up, stone.Down, stone.Side, stone.North, stone.South, stone.East, stone.West} {
		if got := sample(t, a, f.UV); got != (color.NRGBA{100, 100, 100, 255}) {
			t.Fatalf("stone face colour = %v", got)
		}
	}

	log := block(t, a, "minecraft:oak_log")
	if got := sample(t, a, log.Up.UV); got != (color.NRGBA{150, 120, 70, 255}) {
		t.Errorf("log top = %v", got)
	}
	if got := sample(t, a, log.East.UV); got != (color.NRGBA{90, 70, 40, 255}) {
		t.Errorf("log side = %v", got)
	}
	if log.Up.UV == log.North.UV {
		t.Error("log top and side share a tile")
	}
}

func TestBuildDirectionalFacesOverrideSideAndSideFillsGaps(t *testing.T) {
	a := buildFixture(t)
	p := block(t, a, "minecraft:pillar")
	want := map[string]struct {
		face Face
		col  color.NRGBA
	}{
		"north": {p.North, color.NRGBA{120, 80, 40, 255}},
		"south": {p.South, color.NRGBA{100, 100, 100, 255}},
		"east":  {p.East, color.NRGBA{100, 100, 100, 255}},
		"west":  {p.West, color.NRGBA{100, 100, 100, 255}},
		"up":    {p.Up, color.NRGBA{150, 120, 70, 255}},
		"down":  {p.Down, color.NRGBA{100, 100, 100, 255}},
	}
	for name, w := range want {
		if got := sample(t, a, w.face.UV); got != w.col {
			t.Errorf("%s = %v, want %v", name, got, w.col)
		}
	}
}

func TestBuildAcceptsBlocksThatNameEveryFaceWithoutSide(t *testing.T) {
	a := buildFixture(t)
	b := block(t, a, "minecraft:six_sided")
	if got := sample(t, a, b.South.UV); got != (color.NRGBA{150, 120, 70, 255}) {
		t.Errorf("south = %v", got)
	}
	if b.Side.UV != b.North.UV {
		t.Error("side should default to the north face")
	}
}

func TestBuildGrassUsesFirstVariationAndTintsTopOnly(t *testing.T) {
	a := buildFixture(t)
	g := block(t, a, "minecraft:grass")
	if got := sample(t, a, g.Up.UV); got != (color.NRGBA{200, 200, 200, 255}) {
		t.Errorf("grass top should use first variation, got %v", got)
	}
	if g.Tint != TintGrass {
		t.Errorf("tint = %q", g.Tint)
	}
	if !g.Up.Tinted || g.Down.Tinted || g.North.Tinted {
		t.Errorf("tinted faces: up=%v down=%v north=%v", g.Up.Tinted, g.Down.Tinted, g.North.Tinted)
	}
	if g.North.Overlay != "#79c05a" || g.Side.Overlay != "#79c05a" {
		t.Errorf("side overlay = %q", g.North.Overlay)
	}
	alias := block(t, a, "minecraft:grass_block")
	if alias != g {
		t.Error("grass_block should alias the legacy grass entry")
	}
}

func TestBuildDecodesTGATextures(t *testing.T) {
	a := buildFixture(t)
	leaves := block(t, a, "minecraft:oak_leaves")
	if got := sample(t, a, leaves.Up.UV); got != (color.NRGBA{40, 180, 40, 255}) {
		t.Errorf("tga leaf colour = %v", got)
	}
	if leaves.Tint != TintFoliage {
		t.Errorf("leaves tint = %q", leaves.Tint)
	}
	if got := block(t, a, "minecraft:azalea_leaves").Tint; got != TintNone {
		t.Errorf("azalea leaves tint = %q, want none", got)
	}
	f := a.Table().Formats
	if f["tga"] != 1 || f["png"] < 5 {
		t.Errorf("formats = %v", f)
	}
}

func TestBuildTintAndModelKinds(t *testing.T) {
	a := buildFixture(t)
	tests := []struct {
		name  string
		tint  string
		model string
	}{
		{"minecraft:stone", TintNone, ModelCube},
		{"minecraft:water", TintWater, ModelLiquid},
		{"minecraft:flowing_water", TintWater, ModelLiquid},
		{"minecraft:torch", TintNone, ModelTorch},
		{"minecraft:stone_stairs", TintNone, ModelStairs},
		{"minecraft:oak_slab", TintNone, ModelSlab},
		{"minecraft:oak_double_slab", TintNone, ModelCube},
		{"minecraft:short_grass", TintGrass, ModelCross},
	}
	for _, tc := range tests {
		b := block(t, a, tc.name)
		if b.Tint != tc.tint || b.Model != tc.model {
			t.Errorf("%s: tint %q model %q, want %q %q", tc.name, b.Tint, b.Model, tc.tint, tc.model)
		}
	}
}

func TestBuildFallsBackToCarriedTexturesAndRecordsUnmappedKeys(t *testing.T) {
	a := buildFixture(t)
	sg := block(t, a, "minecraft:short_grass")
	if got := sample(t, a, sg.Up.UV); got != (color.NRGBA{60, 160, 60, 128}) {
		t.Errorf("carried fallback colour = %v", got)
	}
	tbl := a.Table()
	for _, name := range []string{"minecraft:haunted", "minecraft:nowhere"} {
		if _, ok := tbl.Blocks[name]; ok {
			t.Errorf("%s should be unmapped", name)
		}
	}
	want := []string{"ghost", "missing_key", "no_such_key"}
	if !slices.Equal(tbl.Unmapped, want) {
		t.Errorf("unmapped keys = %v, want %v", tbl.Unmapped, want)
	}
}

func TestBuildNormalisesTextureSizes(t *testing.T) {
	a := buildFixture(t)
	if got := sample(t, a, block(t, a, "minecraft:panel").Up.UV); got != (color.NRGBA{10, 20, 30, 255}) {
		t.Errorf("downscaled panel = %v", got)
	}
	// Animation strips keep only their first frame.
	if got := sample(t, a, block(t, a, "minecraft:tide_block").Up.UV); got != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("strip frame = %v", got)
	}
}

func TestBuildDeduplicatesSharedTextures(t *testing.T) {
	a := buildFixture(t)
	if block(t, a, "minecraft:oak_slab").Up.UV != block(t, a, "minecraft:oak_double_slab").Up.UV {
		t.Error("blocks sharing a texture should share a tile")
	}
}

func TestMaterialsAndFallbackColours(t *testing.T) {
	a := buildFixture(t)
	tbl := a.Table()
	if got := tbl.Materials["gravel"]; got != "#785028" {
		t.Errorf("gravel material = %q", got)
	}
	for _, class := range []string{"default", "water", "lava"} {
		if tbl.Materials[class] == "" {
			t.Errorf("material %q has no colour", class)
		}
	}
	if got := tbl.Blocks["minecraft:stone"].Color; got != "#646464" {
		t.Errorf("block colour = %q", got)
	}
	if got := tbl.FallbackColor("minecraft:stone"); got != "#646464" {
		t.Errorf("known block fallback = %q", got)
	}
	if got := tbl.FallbackColor("minecraft:flowing_lava"); got != tbl.Materials["lava"] {
		t.Errorf("lava fallback = %q", got)
	}
	if got := tbl.FallbackColor("minecraft:mystery"); got != tbl.Materials["default"] {
		t.Errorf("unknown fallback = %q", got)
	}
}

func TestTintColours(t *testing.T) {
	a := buildFixture(t)
	if got := a.TintColor(TintGrass, 1, 1); got != (color.NRGBA{10, 200, 10, 255}) {
		t.Errorf("hot wet grass = %v", got)
	}
	if got := a.TintColor(TintFoliage, 0, 0); got != (color.NRGBA{200, 20, 20, 255}) {
		t.Errorf("cold dry foliage = %v", got)
	}
	// Out-of-range climate values are clamped, never indexed out of bounds.
	if got := a.TintColor(TintGrass, 5, -3); got != a.TintColor(TintGrass, 1, 0) {
		t.Errorf("clamping = %v", got)
	}
	if got := a.TintColor(TintNone, 1, 1); got != (color.NRGBA{255, 255, 255, 255}) {
		t.Errorf("no tint = %v", got)
	}
	if got := a.Table().WaterColor("minecraft:swamp"); got != (color.NRGBA{0x61, 0x7b, 0x64, 255}) {
		t.Errorf("swamp water = %v", got)
	}
	if got := a.Table().WaterColor("minecraft:plains"); got != (color.NRGBA{0x44, 0xaf, 0xf5, 255}) {
		t.Errorf("default water = %v", got)
	}
}

func TestBuildMissingPack(t *testing.T) {
	if _, err := Build(t.TempDir()); err == nil {
		t.Fatal("expected an error for an empty pack directory")
	}
}
