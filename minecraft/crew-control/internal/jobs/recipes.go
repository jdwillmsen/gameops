package jobs

// Recipe is one entry of the crew's recipe book. Units hold the server's
// recipe list to craft from; crew-control keeps only what its builds need
// in order to plan them.
type Recipe struct {
	// Makes is how many items one craft yields.
	Makes int
	// Inputs is how much of each item one craft consumes.
	Inputs map[string]int
	// Table marks a recipe the 3x3 crafting table must be there for;
	// the rest run in the 2x2 inventory grid.
	Table bool
}

// recipes is keyed by output item. Oak is listed wherever any wood would
// do; birch, spruce and the rest craft the same.
var recipes = map[string]Recipe{
	"oak_planks":     {Makes: 4, Inputs: map[string]int{"oak_log": 1}},
	"stick":          {Makes: 4, Inputs: map[string]int{"oak_planks": 2}},
	"crafting_table": {Makes: 1, Inputs: map[string]int{"oak_planks": 4}},
	"wooden_hoe":     {Makes: 1, Inputs: map[string]int{"oak_planks": 2, "stick": 2}, Table: true},
	"wooden_pickaxe": {Makes: 1, Inputs: map[string]int{"oak_planks": 3, "stick": 2}, Table: true},
	"wooden_axe":     {Makes: 1, Inputs: map[string]int{"oak_planks": 3, "stick": 2}, Table: true},
	"chest":          {Makes: 1, Inputs: map[string]int{"oak_planks": 8}, Table: true},
	"ladder":         {Makes: 3, Inputs: map[string]int{"stick": 7}, Table: true},
	"scaffolding":    {Makes: 6, Inputs: map[string]int{"bamboo": 6, "string": 1}, Table: true},
	"oak_slab":       {Makes: 6, Inputs: map[string]int{"oak_planks": 3}, Table: true},
	"oak_door":       {Makes: 3, Inputs: map[string]int{"oak_planks": 6}, Table: true},
}
