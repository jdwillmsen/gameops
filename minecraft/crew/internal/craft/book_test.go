package craft

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Item network IDs as a lab server would assign them.
const (
	idOakLog         int32  = 5
	idOakPlanks      int32  = 6
	idStick          int32  = 7
	idCraftingTable  int32  = 8
	idWoodenHoe      int32  = 9
	idSprucePlanks   int32  = 60
	idSpruceLog      int32  = 61
	idUnknownOutput  int32  = 999
	idRecipePlanks   uint32 = 10
	idRecipeStick    uint32 = 11
	idRecipeTable    uint32 = 12
	idRecipeHoe      uint32 = 13
	idRecipeSpruce   uint32 = 14
	idRecipeUnnamed  uint32 = 15
	idRecipeMoLang   uint32 = 16
	idRecipeZeroID   uint32 = 17
	idRecipeOverflow uint32 = 18
)

var itemEntries = []protocol.ItemEntry{
	{Name: "minecraft:oak_log", RuntimeID: int16(idOakLog)},
	{Name: "minecraft:oak_planks", RuntimeID: int16(idOakPlanks)},
	{Name: "minecraft:stick", RuntimeID: int16(idStick)},
	{Name: "minecraft:crafting_table", RuntimeID: int16(idCraftingTable)},
	{Name: "minecraft:wooden_hoe", RuntimeID: int16(idWoodenHoe)},
	{Name: "minecraft:spruce_log", RuntimeID: int16(idSpruceLog)},
	{Name: "minecraft:spruce_planks", RuntimeID: int16(idSprucePlanks)},
}

func named(name string, count int32) protocol.ItemDescriptorCount {
	return protocol.ItemDescriptorCount{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:" + name}, Count: count}
}

func stack(id int32, count uint16) protocol.ItemStack {
	return protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: count}
}

// sampleCraftingData holds the four recipes the early builds need, written
// the way the vanilla server sends them: every one tagged crafting_table,
// planks, sticks and the crafting table fitting the 2x2 grid, the hoe only a
// crafting table's, and the stick's ingredient as the planks item tag.
func sampleCraftingData() *packet.CraftingData {
	empty := protocol.ItemDescriptorCount{Descriptor: &protocol.InvalidItemDescriptor{}}
	return &packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID:        "oak_planks",
			Input:           []protocol.ItemDescriptorCount{named("oak_log", 1)},
			Output:          []protocol.ItemStack{stack(idOakPlanks, 4)},
			Block:           "crafting_table",
			RecipeNetworkID: idRecipePlanks,
		}},
		ShapedRecipes: []protocol.ShapedRecipe{
			{
				RecipeID: "stick", Width: 1, Height: 2,
				Input: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.ItemTagItemDescriptor{Tag: "minecraft:planks"}, Count: 1},
					{Descriptor: &protocol.ItemTagItemDescriptor{Tag: "minecraft:planks"}, Count: 1},
				},
				Output:          []protocol.ItemStack{stack(idStick, 4)},
				Block:           "crafting_table",
				RecipeNetworkID: idRecipeStick,
			},
			{
				RecipeID: "crafting_table", Width: 2, Height: 2,
				Input: []protocol.ItemDescriptorCount{
					named("oak_planks", 1), named("oak_planks", 1),
					named("oak_planks", 1), named("oak_planks", 1),
				},
				Output:          []protocol.ItemStack{stack(idCraftingTable, 1)},
				Block:           "crafting_table",
				RecipeNetworkID: idRecipeTable,
			},
			{
				// A hoe: two planks over two sticks, with empty cells in the
				// shaped grid between them.
				RecipeID: "wooden_hoe", Width: 2, Height: 3,
				Input: []protocol.ItemDescriptorCount{
					named("oak_planks", 1), named("oak_planks", 1),
					empty, named("stick", 1),
					empty, named("stick", 1),
				},
				Output:          []protocol.ItemStack{stack(idWoodenHoe, 1)},
				Block:           "crafting_table",
				RecipeNetworkID: idRecipeHoe,
			},
		},
	}
}

func TestStoreIndexesRecipesByOutputName(t *testing.T) {
	b := NewBook()
	b.Store(sampleCraftingData(), itemEntries)

	planks := b.Recipes("oak_planks")
	if len(planks) != 1 {
		t.Fatalf("oak_planks recipes = %d, want 1", len(planks))
	}
	if got := planks[0]; got.NetworkID != idRecipePlanks || got.Block != "crafting_table" || got.OutputCount != 4 ||
		len(got.Ingredients) != 1 || got.Ingredients[0].Name != "oak_log" || got.Ingredients[0].Count != 1 {
		t.Errorf("planks recipe = %+v", got)
	}

	sticks := b.Recipes("stick")
	if len(sticks) != 1 {
		t.Fatalf("stick recipes = %d, want 1", len(sticks))
	}
	if got := sticks[0]; got.NetworkID != idRecipeStick || got.OutputCount != 4 ||
		len(got.Ingredients) != 1 || got.Ingredients[0] != (Ingredient{Tag: "planks", Count: 2}) {
		t.Errorf("stick recipe = %+v, want the planks tag merged to one ingredient of 2", got)
	}

	table := b.Recipes("crafting_table")
	if len(table) != 1 {
		t.Fatalf("crafting_table recipes = %d, want 1", len(table))
	}
	if got := table[0]; got.NetworkID != idRecipeTable || got.OutputCount != 1 ||
		len(got.Ingredients) != 1 || got.Ingredients[0].Name != "oak_planks" || got.Ingredients[0].Count != 4 {
		t.Errorf("table recipe = %+v, want its four grid slots merged to one ingredient", got)
	}

	hoes := b.Recipes("wooden_hoe")
	if len(hoes) != 1 {
		t.Fatalf("wooden_hoe recipes = %d, want 1", len(hoes))
	}
	if got := hoes[0]; got.NetworkID != idRecipeHoe || got.OutputCount != 1 ||
		len(got.Ingredients) != 2 || got.Ingredients[0] != (Ingredient{Name: "oak_planks", Count: 2}) ||
		got.Ingredients[1] != (Ingredient{Name: "stick", Count: 2}) {
		t.Errorf("hoe recipe = %+v, want 2 planks and 2 sticks and no empty cells", got)
	}
}

func TestStoreKeepsTheOutputStackAndTheGrid(t *testing.T) {
	b := NewBook()
	b.Store(&packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID: "oak_planks",
			Input:    []protocol.ItemDescriptorCount{named("oak_log", 1)},
			Output: []protocol.ItemStack{{
				ItemType:       protocol.ItemType{NetworkID: idOakPlanks, MetadataValue: 3},
				BlockRuntimeID: 17,
				Count:          4,
			}},
			RecipeNetworkID: idRecipePlanks,
		}},
	}, itemEntries)

	planks := b.Recipes("oak_planks")
	if len(planks) != 1 {
		t.Fatalf("oak_planks recipes = %d, want 1", len(planks))
	}
	got := planks[0]
	if got.Output.NetworkID != idOakPlanks || got.Output.MetadataValue != 3 ||
		got.Output.BlockRuntimeID != 17 || got.Output.Count != 4 {
		t.Errorf("planks output = %+v, want the output stack of the recipe", got.Output)
	}
	if len(got.Cells) != 1 {
		t.Fatalf("planks cells = %+v, want the one cell of the recipe", got.Cells)
	}
	if got.Cells[0].Empty || got.Cells[0].Name != "oak_log" || got.Cells[0].Count != 1 {
		t.Errorf("planks cell = %+v, want one oak_log", got.Cells[0])
	}
}

func TestStoreKeepsTheGridOfAShapedRecipe(t *testing.T) {
	b := NewBook()
	b.Store(sampleCraftingData(), itemEntries)

	hoes := b.Recipes("wooden_hoe")
	if len(hoes) != 1 {
		t.Fatalf("wooden_hoe recipes = %d, want 1", len(hoes))
	}
	hoe := hoes[0]
	if hoe.Width != 2 || hoe.Height != 3 {
		t.Errorf("hoe grid = %dx%d, want 2x3", hoe.Width, hoe.Height)
	}
	// The grid keeps the cells the recipe leaves empty, or the shape of the
	// recipe is lost.
	if len(hoe.Cells) != 6 {
		t.Fatalf("hoe cells = %+v, want the six cells of the grid", hoe.Cells)
	}
	want := []struct {
		name  string
		empty bool
	}{
		{"oak_planks", false}, {"oak_planks", false},
		{"", true},
		{"stick", false},
		{"", true},
		{"stick", false},
	}
	for i, w := range want {
		got := hoe.Cells[i]
		if got.Empty != w.empty || (!w.empty && (got.Name != w.name || got.Count != 1)) {
			t.Errorf("hoe cell %d = %+v, want %q", i, got, w.name)
		}
	}
}

func TestStoreGivesAShapelessRecipeACellPerItem(t *testing.T) {
	b := NewBook()
	b.Store(&packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID: "chest",
			Input: []protocol.ItemDescriptorCount{
				named("oak_planks", 3),
				// A zero-count descriptor pads the list and fills no cell.
				named("stick", 0),
			},
			Output:          []protocol.ItemStack{stack(idCraftingTable, 1)},
			RecipeNetworkID: idRecipeTable,
		}},
	}, itemEntries)

	rs := b.Recipes("crafting_table")
	if len(rs) != 1 {
		t.Fatalf("crafting_table recipes = %d, want 1", len(rs))
	}
	cells := rs[0].Cells
	if len(cells) != 3 {
		t.Fatalf("cells = %+v, want the three filled cells", cells)
	}
	for i, c := range cells {
		if c.Empty || c.Name != "oak_planks" || c.Count != 1 {
			t.Errorf("cell %d = %+v, want one oak_planks", i, c)
		}
	}
}

func TestNeedsTableFollowsTheRecipeGrid(t *testing.T) {
	b := NewBook()
	b.Store(sampleCraftingData(), itemEntries)
	for item, want := range map[string]bool{"oak_planks": false, "stick": false, "crafting_table": false, "wooden_hoe": true} {
		rs := b.Recipes(item)
		if len(rs) != 1 {
			t.Fatalf("%s recipes = %d, want 1", item, len(rs))
		}
		if got := rs[0].NeedsTable(); got != want {
			t.Errorf("%s NeedsTable = %v, want %v", item, got, want)
		}
	}
}

func TestZeroCountPaddingDoesNotWidenAShapelessRecipe(t *testing.T) {
	in := []protocol.ItemDescriptorCount{named("oak_planks", 4), named("stick", 0)}
	cells, ok := shapelessCells(in)
	if !ok {
		t.Fatal("the recipe's cells could not be read")
	}
	if len(cells) != 4 {
		t.Errorf("cells = %d, want the 4 real cells", len(cells))
	}
}

func TestStoreSkipsRecipesTheUnitCannotWorkWith(t *testing.T) {
	b := NewBook()
	b.Store(&packet.CraftingData{
		ClearRecipes: true,
		ShapelessRecipes: []protocol.ShapelessRecipe{
			{
				// The output is not in the item table, so nothing can ask
				// for it by name.
				RecipeID:        "unnamed",
				Input:           []protocol.ItemDescriptorCount{named("oak_log", 1)},
				Output:          []protocol.ItemStack{stack(idUnknownOutput, 1)},
				RecipeNetworkID: idRecipeUnnamed,
			},
			{
				// MoLang names an ingredient the inventory cannot match.
				RecipeID: "molang",
				Input: []protocol.ItemDescriptorCount{{
					Descriptor: &protocol.MoLangItemDescriptor{Expression: "query.tag('planks')", Version: 6},
					Count:      1,
				}},
				Output:          []protocol.ItemStack{stack(idOakPlanks, 4)},
				RecipeNetworkID: idRecipeMoLang,
			},
			{
				// A zero network ID is not a recipe the server would accept
				// being quoted back.
				RecipeID:        "zero",
				Input:           []protocol.ItemDescriptorCount{named("spruce_log", 1)},
				Output:          []protocol.ItemStack{stack(idSprucePlanks, 4)},
				RecipeNetworkID: 0,
			},
			{
				// Zero-count descriptors are the server padding a list, not
				// an ingredient.
				RecipeID:        "overflow",
				Input:           []protocol.ItemDescriptorCount{named("spruce_log", 0)},
				Output:          []protocol.ItemStack{stack(idSprucePlanks, 4)},
				RecipeNetworkID: idRecipeOverflow,
			},
		},
	}, itemEntries)
	for _, item := range []string{"oak_planks", "spruce_planks"} {
		if rs := b.Recipes(item); len(rs) != 0 {
			t.Errorf("%s recipes = %+v, want none", item, rs)
		}
	}
}

func TestStoreReplacesOnClearAndMergesWithoutIt(t *testing.T) {
	b := NewBook()
	b.Store(sampleCraftingData(), itemEntries)

	spruce := &packet.CraftingData{
		ShapelessRecipes: []protocol.ShapelessRecipe{{
			RecipeID:        "spruce_planks",
			Input:           []protocol.ItemDescriptorCount{named("spruce_log", 1)},
			Output:          []protocol.ItemStack{stack(idSprucePlanks, 4)},
			RecipeNetworkID: idRecipeSpruce,
		}},
	}
	spruce.ClearRecipes = false
	b.Store(spruce, itemEntries)
	if rs := b.Recipes("oak_planks"); len(rs) != 1 || rs[0].NetworkID != idRecipePlanks {
		t.Errorf("a merge dropped the earlier recipes: %+v", rs)
	}
	if rs := b.Recipes("spruce_planks"); len(rs) != 1 || rs[0].NetworkID != idRecipeSpruce {
		t.Errorf("a merge did not add the new recipe: %+v", rs)
	}

	spruce.ClearRecipes = true
	b.Store(spruce, itemEntries)
	if rs := b.Recipes("oak_planks"); len(rs) != 0 {
		t.Errorf("a clear kept the earlier recipes: %+v", rs)
	}
	if rs := b.Recipes("spruce_planks"); len(rs) != 1 {
		t.Errorf("a clear did not leave the new recipes: %+v", rs)
	}
}
