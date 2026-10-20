package path

import "github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"

// Passable reports whether the unit can occupy a block without breaking it,
// given its name without the namespace. Water is absent on purpose: a planned
// path must never enter it, though the body swims through it when a mishap
// drops the unit in. So are
// sweet berry bushes, which hurt and slow the unit. A snow layer counts: the
// unit walks over it.
func Passable(name string) bool { return shape.Empty(name) || name == "snow_layer" }

// Water reports whether a block, given its name without the namespace, is
// water the unit can swim in. Lava is not: it stays solid to the body.
func Water(name string) bool { return name == "water" || name == "flowing_water" }
