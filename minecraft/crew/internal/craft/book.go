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

// Cell is one cell of a recipe's grid: the ingredient that fills it and how
// many of it the cell holds. A shaped recipe's grid keeps the cells it
// leaves empty, which hold nothing.
type Cell struct {
	Ingredient
	// Empty says the cell holds nothing.
	Empty bool
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
	// Width and Height are the shaped grid's size in cells. A shapeless
	// recipe has no grid of its own and leaves them zero.
	Width, Height int
	// Cells are the recipe's grid: row-major for a shaped recipe, the empty
	// cells included, and one cell per item for a shapeless one.
	Cells []Cell
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
		cells, ok := shapelessCells(r.Input)
		if !ok {
			continue
		}
		index(indexed, names, Recipe{
			NetworkID: r.RecipeNetworkID,
			Block:     r.Block,
			wide:      len(cells) > 4,
			Cells:     cells,
		}, r.Output)
	}
	for _, r := range pk.ShapedRecipes {
		cells, ok := shapedCells(r.Input, int(r.Width), int(r.Height))
		if !ok {
			continue
		}
		index(indexed, names, Recipe{
			NetworkID: r.RecipeNetworkID,
			Block:     r.Block,
			wide:      r.Width > 2 || r.Height > 2,
			Width:     int(r.Width),
			Height:    int(r.Height),
			Cells:     cells,
		}, r.Output)
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
func index(out map[string][]Recipe, names map[int32]string, r Recipe, outputs []protocol.ItemStack) {
	if r.NetworkID == 0 || len(outputs) == 0 {
		return
	}
	name, ok := names[outputs[0].NetworkID]
	if !ok || name == "" {
		return
	}
	r.OutputCount = int(outputs[0].Count)
	r.Output = outputs[0]
	for _, c := range r.Cells {
		if c.Empty {
			continue
		}
		ing := c.Ingredient
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

// shapedCells turns a shaped recipe's descriptors into its grid: row-major,
// with the cells the recipe leaves empty kept, so the grid's shape stays.
func shapedCells(in []protocol.ItemDescriptorCount, width, height int) ([]Cell, bool) {
	if width < 1 || height < 1 {
		return nil, false
	}
	cells := make([]Cell, 0, width*height)
	for i := 0; i < width*height; i++ {
		if i >= len(in) {
			// The server's list falls short of the grid it declared, so the
			// rest of the grid is empty.
			cells = append(cells, Cell{Empty: true})
			continue
		}
		c, ok := cellOf(in[i])
		if !ok {
			return nil, false
		}
		cells = append(cells, c)
	}
	return cells, true
}

// shapelessCells turns a shapeless recipe's ingredients into grid cells: a
// descriptor's count is how many items the recipe takes, and they fill a
// cell each.
func shapelessCells(in []protocol.ItemDescriptorCount) ([]Cell, bool) {
	var cells []Cell
	for _, d := range in {
		c, ok := cellOf(d)
		if !ok {
			return nil, false
		}
		if c.Empty {
			// A zero-count descriptor pads the list and fills no cell.
			continue
		}
		for i := 0; i < c.Count; i++ {
			cells = append(cells, Cell{Ingredient: Ingredient{Name: c.Name, Tag: c.Tag, Count: 1}})
		}
	}
	return cells, true
}

// cellOf reads one grid cell from a recipe's descriptor: the ingredient that
// fills it, or an empty cell. The second result is false for a descriptor
// the inventory cannot match at all, which rules the recipe out.
func cellOf(d protocol.ItemDescriptorCount) (Cell, bool) {
	switch desc := d.Descriptor.(type) {
	case *protocol.InvalidItemDescriptor:
		// An empty cell of a shaped recipe's grid.
		return Cell{Empty: true}, true
	case *protocol.DefaultItemDescriptor:
		return filledCell(Ingredient{Name: trim(desc.Name), Count: int(d.Count)}), true
	case *protocol.ItemTagItemDescriptor:
		// A tag stands for many items and the inventory holds concrete
		// names, so the tag is kept and the item is matched against it.
		return filledCell(Ingredient{Tag: Tag(trim(desc.Tag)), Count: int(d.Count)}), true
	default:
		return Cell{}, false
	}
}

// filledCell is a cell with an ingredient in it; a descriptor that names
// nothing usable fills no cell.
func filledCell(ing Ingredient) Cell {
	if ing.Count < 1 || (ing.Name == "" && ing.Tag == "") {
		return Cell{Empty: true}
	}
	return Cell{Ingredient: ing}
}

func trim(n string) string {
	const prefix = "minecraft:"
	if len(n) > len(prefix) && strings.HasPrefix(n, prefix) {
		return n[len(prefix):]
	}
	return n
}
