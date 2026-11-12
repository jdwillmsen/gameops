package task

// Source is what yields an item: the block to break and the tool class that
// harvests it.
type Source struct {
	Block string
	Tool  string
	// Fell makes the unit cut down whole trees, pillaring up for logs above
	// its reach, instead of only breaking what it can reach from the ground.
	Fell bool
	// Chancy marks an item the block drops only now and then, so most breaks
	// bring nothing and the task must not give up after a few.
	Chancy bool
}

// recipes is closed on purpose: an item missing here is rejected at the API
// instead of sending the unit to mine something it cannot collect. Ores are
// absent on purpose: the plain mining skill walks to a block with no check for
// lava, water or a fall, so ore comes only from the mine_shaft dive
// (mine_ore, iron_age), which makes those checks.
var recipes = map[string]Source{
	"cobblestone": {Block: "stone", Tool: "pickaxe"},
	"oak_log":     {Block: "oak_log", Tool: "axe", Fell: true},
	"oak_sapling": {Block: "oak_leaves", Chancy: true},
}
