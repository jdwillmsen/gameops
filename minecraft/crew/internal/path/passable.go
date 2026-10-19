package path

import "strings"

// passable lists the blocks the unit walks through. Liquids are absent on
// purpose: swimming is not modelled, so a planned path must never enter one.
// So are sweet berry bushes, which hurt and slow the unit.
var passable = map[string]bool{
	"air":         true,
	"short_grass": true,
	"tall_grass":  true,
	"fern":        true,
	"large_fern":  true,
	"dandelion":   true,
	"poppy":       true,
	"torch":       true,
	"snow_layer":  true,
	"deadbush":    true,
	"vine":        true,
	"glow_lichen": true,
}

// Passable reports whether the unit can occupy a block, given its name without
// the namespace.
func Passable(name string) bool {
	return passable[name] || strings.HasSuffix(name, "_sapling")
}
