package craft

import (
	"slices"
	"strings"
)

// Tag is an item tag the server used for an ingredient, such as
// "minecraft:planks". The client has no item-tag data of its own, so the
// tags the builds need are spelled out here.
type Tag string

// itemTags are the tags the recipes use and the items each covers. The
// server names them with the "minecraft:" prefix, which is trimmed like
// the item names; a tag that is not listed here covers nothing.
var itemTags = map[Tag]func(name string) bool{
	"planks":                   endsWith("_planks"),
	"logs":                     endsWith("_log", "_wood", "_stem"),
	"wooden_slabs":             woodSlab,
	"stone_crafting_materials": isOneOf("cobblestone", "blackstone", "cobbled_deepslate"),
	"coals":                    isOneOf("coal", "charcoal"),
}

// woods are the wood types whose slabs the wooden_slabs tag covers.
var woods = []string{
	"acacia", "bamboo", "birch", "cherry", "crimson", "dark_oak", "jungle",
	"mangrove", "oak", "pale_oak", "spruce", "warped",
}

// Matches says whether the item is one the tag covers.
func (t Tag) Matches(name string) bool {
	match, ok := itemTags[t]
	return ok && match(name)
}

// endsWith covers the items named after what they are made of, such as the
// planks of every wood.
func endsWith(suffixes ...string) func(name string) bool {
	return func(name string) bool {
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				return true
			}
		}
		return false
	}
}

// isOneOf covers the items the tag names one by one.
func isOneOf(names ...string) func(name string) bool {
	return func(name string) bool { return slices.Contains(names, name) }
}

// woodSlab covers the slabs of the woods and nothing else: the tag reaches
// neither the stone slabs nor the glass ones.
func woodSlab(name string) bool {
	base, ok := strings.CutSuffix(name, "_slab")
	if !ok {
		return false
	}
	return slices.Contains(woods, base)
}
