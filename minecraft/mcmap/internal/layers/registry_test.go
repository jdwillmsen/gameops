package layers

import "testing"

func TestRegistry_NumbersABlockOnceWhateverItsPrefix(t *testing.T) {
	r := NewRegistry()
	if r.Blocks()[AirID].Name != "air" || r.Blocks()[AirID].Flags&Air == 0 {
		t.Fatalf("block 0 is %+v", r.Blocks()[AirID])
	}
	a := r.intern([]byte("minecraft:stone"))
	if b := r.intern([]byte("minecraft:stone")); a != b {
		t.Errorf("one block has two numbers, %d and %d", a, b)
	}
	if r.intern([]byte("minecraft:air")) != AirID {
		t.Error("air is not block 0")
	}
	if r.Blocks()[a].Name != "stone" {
		t.Errorf("a block's name is %q", r.Blocks()[a].Name)
	}
	// A snapshot does not change under the reader.
	before := r.Blocks()
	r.intern([]byte("minecraft:granite"))
	if len(before) != int(a)+1 || len(r.Blocks()) != len(before)+1 {
		t.Errorf("snapshot has %d blocks, the registry %d", len(before), len(r.Blocks()))
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		name string
		want Flags
	}{
		{"air", Air}, {"cave_air", Air}, {"void_air", Air}, {"light_block_7", Air}, {"structure_void", Air},
		{"water", Water}, {"flowing_water", Water}, {"lava", Lava},
		{"short_grass", Plant}, {"oak_sapling", Plant}, {"pink_carpet", Plant}, {"snow_layer", Plant},
		{"rail", Feature}, {"torch", Feature}, {"oak_wall_sign", Feature}, {"stone_button", Feature}, {"waxed_lightning_rod", Feature},
		{"stone", 0}, {"oak_leaves", 0}, {"grass_block", 0}, {"red_mushroom_block", 0}, {"copper_chest", 0},
	} {
		if got := classify(c.name); got != c.want {
			t.Errorf("%s is %b, want %b", c.name, got, c.want)
		}
	}
	solid := Block{Flags: 0}
	if !solid.Solid() || (&Block{Flags: Water}).Solid() || (&Block{Flags: Plant}).Solid() || (&Block{Flags: Feature}).Solid() {
		t.Error("what can be stood on is wrong")
	}
}

func TestColours_AreListedDerivedOrGuessedAndAGuessIsStable(t *testing.T) {
	for _, c := range []struct {
		name   string
		source ColourSource
		rgb    [3]uint8
	}{
		{"stone", Listed, [3]uint8{0x7d, 0x7d, 0x7d}},
		{"lava", Listed, [3]uint8{0xcf, 0x5c, 0x0f}},
		{"oak_stairs", Derived, rgbOf(woods["oak"].planks)},
		{"spruce_log", Derived, rgbOf(woods["spruce"].log)},
		{"stripped_birch_log", Derived, rgbOf(woods["birch"].planks)},
		{"mangrove_leaves", Derived, rgbOf(woods["mangrove"].leaves)},
		{"cobblestone_wall", Derived, rgbOf(table["cobblestone"])},
		{"stone_brick_stairs", Derived, rgbOf(table["stone_bricks"])},
		{"polished_andesite_stairs", Derived, rgbOf(table["polished_andesite"])},
		{"red_wool", Derived, scale(rgbOf(dyes["red"]), 256, 256)},
		{"waxed_weathered_cut_copper_slab", Derived, rgbOf(table["weathered_copper"])},
		{"exposed_lightning_rod", Derived, rgbOf(table["exposed_copper"])},
	} {
		got, source := colourOf(c.name)
		if source != c.source || got != c.rgb {
			t.Errorf("%s: %v from %d, want %v from %d", c.name, got, source, c.rgb, c.source)
		}
	}
	a, source := colourOf("a_block_nobody_has_heard_of")
	b, _ := colourOf("a_block_nobody_has_heard_of")
	other, _ := colourOf("another_block_nobody_has_heard_of")
	if source != Guessed || a != b || a == other {
		t.Errorf("a guess is %v then %v, and %v for another, from %d", a, b, other, source)
	}
}

func TestColours_ADyedBlockIsItsDyeAndTerracottaAndGlassAreMutedByTheirMaterial(t *testing.T) {
	for dye := range dyes {
		for family := range dyedFamilies {
			name := dye + "_" + family
			if _, source := colourOf(name); source == Guessed {
				t.Errorf("%s has no colour", name)
			}
		}
	}
	wool, _ := colourOf("red_wool")
	terracotta, _ := colourOf("red_terracotta")
	glass, _ := colourOf("red_stained_glass")
	if terracotta == wool || glass == wool {
		t.Error("terracotta and glass are the colour of wool")
	}
}

func TestEveryListedBlockHasAColourThatIsNotAGuess(t *testing.T) {
	for name := range table {
		if _, source := colourOf(name); source != Listed {
			t.Errorf("%s is listed and not read as such", name)
		}
	}
}
