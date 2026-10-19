package path

import "github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"

// Passable reports whether the unit can occupy a block without breaking it,
// given its name without the namespace. Liquids are absent on purpose:
// swimming is not modelled, so a planned path must never enter one. So are
// sweet berry bushes, which hurt and slow the unit. A snow layer counts: the
// unit walks over it.
func Passable(name string) bool { return shape.Empty(name) || name == "snow_layer" }
