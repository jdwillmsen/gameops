package layers

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// biomeAt answers for columns by their x, as a world with a desert and a
// plains side by side would.
type biomeAt func(x, z int32) (string, bool)

func (f biomeAt) At(_ chunks.Dimension, x, z int32) (biomes.Biome, bool) {
	name, ok := f(x, z)
	if !ok {
		return biomes.Biome{}, false
	}
	ids := map[string]uint32{"plains": 1, "desert": 2, "swampland": 6, "warm_ocean": 40}
	return biomes.Biome{ID: ids[name], Name: name}, true
}

func TestPaletteOf_NamesBiomesByTheirClimate(t *testing.T) {
	for name, want := range map[string]palette{
		"plains":             plains,
		"sunflower_plains":   plains,
		"desert":             biomePalettes[indexOf("desert")].palette,
		"mangrove_swamp":     biomePalettes[indexOf("mangrove")].palette,
		"swampland":          biomePalettes[indexOf("swamp")].palette,
		"deep_frozen_ocean":  biomePalettes[indexOf("frozen")].palette,
		"a_biome_not_known":  plains,
		"birch_forest_hills": biomePalettes[indexOf("birch")].palette,
	} {
		if got := paletteOf(name); got != want {
			t.Errorf("%s is %+v, want %+v", name, got, want)
		}
	}
}

func indexOf(word string) int {
	for i, p := range biomePalettes {
		if p.word == word {
			return i
		}
	}
	panic(word)
}

func TestTinted_LeavesAPlainsColumnAsTheTableHasItAndMovesOthersByTheRatio(t *testing.T) {
	grass := rgbOf(table["grass_block"])
	if got := tinted(grass, TintGrass, &plains); got != grass {
		t.Errorf("grass in plains is %v, want %v", got, grass)
	}
	dry := paletteOf("desert")
	got := tinted(grass, TintGrass, &dry)
	if got[0] <= grass[0] {
		t.Errorf("grass in a desert is %v, from %v in plains: it is not redder", got, grass)
	}
	stone := rgbOf(table["stone"])
	if tinted(stone, 0, &dry) != stone {
		t.Error("stone was tinted")
	}
	water := rgbOf(table["water"])
	if tinted(water, Water, &plains) != water {
		t.Errorf("water in plains is %v", tinted(water, Water, &plains))
	}
	if tinted(water, Water, &dry) == water {
		t.Error("water in a desert is a plains' water")
	}
	if tinted([3]uint8{255, 255, 255}, TintGrass, &dry)[0] != 255 {
		t.Error("a channel went past a byte")
	}
}

func TestTile_GrassAndWaterTakeTheirColoursFromTheBiomeOfTheColumn(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 0, Z: 0}, 10, func(x, y, z int) string {
		switch {
		case y <= 5:
			return "stone"
		case y <= 9 && z >= 8:
			return "water"
		case y <= 9:
			return "grass_block"
		}
		return "air"
	})
	r := renderer(f)
	spec := Spec{Dim: chunks.Overworld, Cut: 9, Thickness: 16}
	draw := func(b func() Biomes) (grass, water [4]uint8) {
		r.Biomes = b
		data, _, ok, err := r.Tile(spec, 0, 0, 0)
		if err != nil || !ok {
			t.Fatalf("ok %v, error %v", ok, err)
		}
		img := decodeTile(t, data)
		g, w := img.NRGBAAt(2, 2), img.NRGBAAt(2, 12)
		return [4]uint8{g.R, g.G, g.B, g.A}, [4]uint8{w.R, w.G, w.B, w.A}
	}
	plainGrass, plainWater := draw(nil)
	desert := func() Biomes {
		return biomeAt(func(x, z int32) (string, bool) { return "desert", true })
	}
	dryGrass, dryWater := draw(desert)
	if dryGrass == plainGrass || dryWater == plainWater {
		t.Errorf("a desert drew grass %v and water %v as plains does: %v %v", dryGrass, dryWater, plainGrass, plainWater)
	}
	// Nothing stored for the column is a plains one.
	none := func() Biomes { return biomeAt(func(x, z int32) (string, bool) { return "", false }) }
	if g, w := draw(none); g != plainGrass || w != plainWater {
		t.Errorf("a column with no biome is %v and %v, want plains' %v and %v", g, w, plainGrass, plainWater)
	}
}
