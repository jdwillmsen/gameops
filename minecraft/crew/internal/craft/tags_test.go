package craft

import "testing"

// TestTagCoversTheItemsTheServerLists pins what each tag the builds need
// covers, because the client has no item-tag data of its own and a tag
// that matches the wrong item makes a recipe craft the wrong thing.
func TestTagCoversTheItemsTheServerLists(t *testing.T) {
	for _, tc := range []struct {
		tag    Tag
		covers []string
		misses []string
	}{
		{
			tag:    "planks",
			covers: []string{"oak_planks", "spruce_planks", "dark_oak_planks"},
			misses: []string{"oak_log", "stick", "planks"},
		},
		{
			tag:    "logs",
			covers: []string{"oak_log", "oak_wood", "crimson_stem", "stripped_oak_log", "stripped_spruce_wood"},
			misses: []string{"oak_planks", "oak_slab", "log"},
		},
		{
			tag:    "wooden_slabs",
			covers: []string{"oak_slab", "dark_oak_slab", "bamboo_slab"},
			misses: []string{"stone_slab", "smooth_stone_slab", "oak_planks"},
		},
		{
			tag:    "stone_crafting_materials",
			covers: []string{"cobblestone", "blackstone", "cobbled_deepslate"},
			misses: []string{"stone", "cobblestone_stairs", "deepslate"},
		},
		{
			tag:    "coals",
			covers: []string{"coal", "charcoal"},
			misses: []string{"coal_block", "charcoal_block", "coal_ore"},
		},
	} {
		for _, name := range tc.covers {
			if !tc.tag.Matches(name) {
				t.Errorf("tag %q does not cover %q", tc.tag, name)
			}
		}
		for _, name := range tc.misses {
			if tc.tag.Matches(name) {
				t.Errorf("tag %q covers %q", tc.tag, name)
			}
		}
	}
}

// TestAnUnknownTagCoversNothing keeps a tag the client does not know from
// matching an item that happens to share its name.
func TestAnUnknownTagCoversNothing(t *testing.T) {
	for _, name := range []string{"white_wool", "oak_planks", "planks"} {
		if Tag("wool").Matches(name) {
			t.Errorf("the unknown tag wool covers %q", name)
		}
	}
}
