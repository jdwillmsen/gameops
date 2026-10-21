// Package craft indexes the recipes the server sends at join, so a unit can
// pick one by the item it wants to make. Recipe network IDs are assigned per
// server boot and are only meaningful together with the CraftingData packet
// they came in, so a session keeps its own book.
package craft

import (
	"strings"
	"sync"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Ingredient is one item a recipe consumes, by the name belief gives the
// same item in the inventory. A recipe may name a tag instead of an item,
// which stands for many of the items the inventory holds.
type Ingredient struct {
	Name  string
	Tag   Tag
	Count int
}

// Matches says whether the item the inventory holds pays for this
// ingredient: the named item, or any item the tag covers.
func (i Ingredient) Matches(name string) bool {
	if i.Tag != "" {
		return i.Tag.Matches(name)
	}
	return i.Name == name
}

// Recipe is one recipe from the server's CraftingData. Block is the station
// the server names, which is crafting_table for every crafting recipe;
// NeedsTable says which of them a player's own 2x2 grid cannot make.
type Recipe struct {
	NetworkID uint32
	Block     string
	// wide says the recipe spans more than the 2x2 player grid.
	wide bool
	// Ingredients are merged by item or tag, in the server's order.
	Ingredients []Ingredient
	// OutputCount is how many of the output one craft makes.
	OutputCount int
	// Output is the recipe's first output stack, which a craft result quotes
	// by metadata and block runtime ID.
	Output protocol.ItemStack
	// Inputs are the recipe's ingredient descriptors as the server sent them,
	// without the empty cells of a shaped grid.
	Inputs []protocol.ItemDescriptorCount
}

// NeedsTable says whether the recipe only fits a crafting table's 3x3 grid.
// Bedrock tags every crafting recipe crafting_table, the 2x2 ones the player
// grid makes included, so the grid the recipe spans decides it, not Block.
func (r Recipe) NeedsTable() bool { return r.Block == "crafting_table" && r.wide }

// Book is the server's recipe book, safe to store from the read loop while a
// skill reads it.
type Book struct {
	mu       sync.Mutex
	byOutput map[string][]Recipe
}

func NewBook() *Book { return &Book{byOutput: map[string][]Recipe{}} }

// Store indexes the packet's shaped and shapeless recipes by the name of
// their first output, mapping output network IDs through the server's item
// table. ClearRecipes replaces the book, as the packet's meaning at join is;
// without it the recipes merge into what an earlier packet stored.
func (b *Book) Store(pk *packet.CraftingData, items []protocol.ItemEntry) {
	names := make(map[int32]string, len(items))
	for _, e := range items {
		names[int32(e.RuntimeID)] = trim(e.Name)
	}
	indexed := make(map[string][]Recipe)
	for _, r := range pk.ShapelessRecipes {
		index(indexed, names, r.RecipeNetworkID, r.Block, filled(r.Input) > 4, r.Input, r.Output)
	}
	for _, r := range pk.ShapedRecipes {
		index(indexed, names, r.RecipeNetworkID, r.Block, r.Width > 2 || r.Height > 2, r.Input, r.Output)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if pk.ClearRecipes {
		b.byOutput = indexed
		return
	}
	for item, rs := range indexed {
		b.byOutput[item] = append(b.byOutput[item], rs...)
	}
}

// Recipes returns the recipes whose first output is item, in server order.
func (b *Book) Recipes(item string) []Recipe {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Recipe(nil), b.byOutput[item]...)
}

// index flattens one recipe into the map, or skips it whole when the unit
// could not work with it: an output the item table does not name cannot be
// asked for, and an ingredient named only by a MoLang expression cannot be
// matched against the inventory.
func index(out map[string][]Recipe, names map[int32]string, id uint32, block string, wide bool, in []protocol.ItemDescriptorCount, outputs []protocol.ItemStack) {
	if id == 0 || len(outputs) == 0 {
		return
	}
	name, ok := names[outputs[0].NetworkID]
	if !ok || name == "" {
		return
	}
	r := Recipe{NetworkID: id, Block: block, wide: wide, OutputCount: int(outputs[0].Count), Output: outputs[0]}
	for _, d := range in {
		var ing Ingredient
		switch desc := d.Descriptor.(type) {
		case *protocol.InvalidItemDescriptor:
			// An empty cell of a shaped recipe's grid.
			continue
		case *protocol.DefaultItemDescriptor:
			ing = Ingredient{Name: trim(desc.Name), Count: int(d.Count)}
		case *protocol.ItemTagItemDescriptor:
			// A tag stands for many items and the inventory holds
			// concrete names, so the tag is kept and the item is matched
			// against it.
			ing = Ingredient{Tag: Tag(trim(desc.Tag)), Count: int(d.Count)}
		default:
			return
		}
		if ing.Count < 1 || (ing.Name == "" && ing.Tag == "") {
			continue
		}
		// The recipe's own descriptors are kept, because a recipe book
		// craft quotes them back to the server as they were sent.
		r.Inputs = append(r.Inputs, d)
		merged := false
		for i := range r.Ingredients {
			if r.Ingredients[i].Name == ing.Name && r.Ingredients[i].Tag == ing.Tag {
				r.Ingredients[i].Count += ing.Count
				merged = true
				break
			}
		}
		if !merged {
			r.Ingredients = append(r.Ingredients, ing)
		}
	}
	if len(r.Ingredients) == 0 {
		return
	}
	out[name] = append(out[name], r)
}

// filled counts a shapeless recipe's ingredient cells, which is what decides
// whether it fits the player's 2x2 grid.
func filled(in []protocol.ItemDescriptorCount) int {
	n := 0
	for _, d := range in {
		// Zero-count entries pad the list and fill no cell.
		if _, empty := d.Descriptor.(*protocol.InvalidItemDescriptor); !empty && d.Count > 0 {
			n += int(d.Count)
		}
	}
	return n
}

func trim(n string) string {
	const prefix = "minecraft:"
	if len(n) > len(prefix) && strings.HasPrefix(n, prefix) {
		return n[len(prefix):]
	}
	return n
}
