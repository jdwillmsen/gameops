package camera

import (
	"strings"
	"testing"
)

func TestRenderNameCarriesTheShapeStatesChange(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"stone_brick_slab", map[string]any{"minecraft:vertical_half": "bottom"}, "stone_brick_slab"},
		{"stone_brick_slab", map[string]any{"minecraft:vertical_half": "top"}, "stone_brick_slab@0,8,0,16,16,16"},
		{"stone_brick_double_slab", map[string]any{"minecraft:vertical_half": "top"}, "stone_brick_double_slab"},
		{"oak_stairs", map[string]any{"weirdo_direction": int32(0), "upside_down_bit": false}, "oak_stairs@0,0,0,16,8,16;8,8,0,16,16,16"},
		{"oak_stairs", map[string]any{"weirdo_direction": int32(1), "upside_down_bit": true}, "oak_stairs@0,8,0,16,16,16;0,0,0,8,8,16"},
		{"snow_layer", map[string]any{"height": int32(3)}, "snow_layer@0,0,0,16,8,16"},
		{"stone", nil, "stone"},
		{"minecraft:trapdoor", map[string]any{"open_bit": false, "upside_down_bit": true}, "minecraft:trapdoor@0,13,0,16,16,16"},
		{"minecraft:snow_layer", map[string]any{"height": int32(3)}, "minecraft:snow_layer@0,0,0,16,8,16"},
		{"minecraft:stone_brick_slab", map[string]any{"minecraft:vertical_half": "top"}, "minecraft:stone_brick_slab@0,8,0,16,16,16"},
	}
	for _, c := range cases {
		if got := renderName(c.name, c.props); got != c.want {
			t.Errorf("%s %v = %q, want %q", c.name, c.props, got, c.want)
		}
	}
}

func TestRenderNameCapsTallBoxesAtTheBlock(t *testing.T) {
	got := renderName("oak_trapdoor", map[string]any{"open_bit": false, "upside_down_bit": true})
	if !strings.HasPrefix(got, "oak_trapdoor@0,13,0,16,16,16") {
		t.Errorf("got %q", got)
	}
}

// The substring list this replaces hid every one of these cubes.
func TestWholeCubesAreNotFlaggedNonCube(t *testing.T) {
	for _, n := range []string{"fire_coral_block", "brown_mushroom_block", "red_mushroom_block", "dried_kelp_block",
		"budding_amethyst", "mushroom_stem", "dead_fire_coral_block", "oak_leaves", "flowering_azalea_leaves", "grass_block", "snow",
		"bamboo_planks", "smooth_stone", "design"} {
		if notCube(n) {
			t.Errorf("%s is flagged non-cube", n)
		}
	}
	for _, n := range []string{"oak_button", "wall_sign", "amethyst_cluster", "fire", "rail", "golden_rail", "fire_coral", "oak_stairs",
		"cobblestone_wall", "flower_pot", "pointed_dripstone", "short_grass", "oak_trapdoor"} {
		if !notCube(n) {
			t.Errorf("%s is not flagged non-cube", n)
		}
	}
}

func TestLightBlocksAreNotDrawn(t *testing.T) {
	tab := newMaterialTable()
	for _, n := range []string{"minecraft:light_block_15", "minecraft:light_block", "minecraft:barrier"} {
		if id, _ := tab.id(n); id != 0 {
			t.Errorf("%s got id %d", n, id)
		}
	}
}

func TestShapeVariantsFallBackToTheBlockOncePastTheBudget(t *testing.T) {
	tab := newMaterialTable()
	for i := 0; i < variantBudget; i++ {
		tab.id("minecraft:block_" + strings.Repeat("x", i/26) + string(rune('a'+i%26)))
	}
	id, _ := tab.id("minecraft:oak_stairs@0,0,0,16,8,16")
	for _, m := range tab.all() {
		if m.ID == id && m.Name != "oak_stairs" {
			t.Errorf("variant past the budget kept its own id: %s", m.Name)
		}
	}
	if base, boxes := splitVariant("oak_stairs@1,2"); base != "oak_stairs" || boxes != "1,2" {
		t.Errorf("split = %q %q", base, boxes)
	}
}
