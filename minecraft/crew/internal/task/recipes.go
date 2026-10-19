package task

// Source is what yields an item: the block to break and the tool class that
// harvests it.
type Source struct {
	Block string
	Tool  string
	// Fell makes the unit cut down whole trees, pillaring up for logs above
	// its reach, instead of only breaking what it can reach from the ground.
	Fell bool
}

// recipes is closed on purpose: an item missing here is rejected at the API
// instead of sending the unit to mine something it cannot collect.
var recipes = map[string]Source{
	"cobblestone": {Block: "stone", Tool: "pickaxe"},
	"oak_log":     {Block: "oak_log", Tool: "axe", Fell: true},
}
