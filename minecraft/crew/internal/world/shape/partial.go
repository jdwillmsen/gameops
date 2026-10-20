package shape

// Blocks the block table gets wrong, or does not model, which the lab server
// was measured on: a unit dropped on the middle of each came to rest at the
// height noted. The footprints around those heights are the usual ones for the
// block, since a drop in the middle measures only the middle.

func slab(h float32) Box { return Box{Max: [3]float32{1, h, 1}} }

func inset(x0, z0, x1, z1, h float32) Box {
	return Box{Min: [3]float32{x0, 0, z0}, Max: [3]float32{x1, h, z1}}
}

// rim is the four walls of a vessel, thick on each side, reaching up to top.
func rim(thick, top float32) []Box {
	return []Box{
		{Max: [3]float32{1, top, thick}},
		{Min: [3]float32{0, 0, 1 - thick}, Max: [3]float32{1, top, 1}},
		{Max: [3]float32{thick, top, 1}},
		{Min: [3]float32{1 - thick, 0, 0}, Max: [3]float32{1, top, 1}},
	}
}

// partial returns the boxes of a block with a measured shape, and false for
// any other.
func partial(name string) ([]Box, bool) {
	switch name {
	case "trapped_chest":
		// The block table has no model for it; it rests a unit at 0.95 like a
		// chest.
		return []Box{inset(0.025, 0.025, 0.975, 0.975, 0.95)}, true
	case "turtle_egg":
		return []Box{inset(0.1875, 0.1875, 0.8125, 0.8125, 0.45)}, true
	case "waterlily":
		return []Box{inset(0.0625, 0.0625, 0.9375, 0.9375, 0.09375)}, true
	case "grindstone":
		// A cube is the safe side of a shape that turns with its mounting.
		return []Box{full}, true
	case "soul_sand", "mud":
		return []Box{slab(0.875)}, true
	case "daylight_detector", "daylight_detector_inverted":
		return []Box{slab(0.375)}, true
	case "sculk_sensor":
		return []Box{slab(0.5)}, true
	case "flower_pot":
		return []Box{inset(0.3125, 0.3125, 0.6875, 0.6875, 0.375)}, true
	case "bell":
		return []Box{inset(0.25, 0.25, 0.75, 0.75, 0.8125)}, true
	case "brewing_stand":
		return []Box{inset(0.0625, 0.0625, 0.9375, 0.9375, 0.125), inset(0.4375, 0.4375, 0.5625, 0.5625, 0.875)}, true
	case "composter":
		return append([]Box{slab(0.125)}, rim(0.125, 1)...), true
	case "cauldron":
		return append([]Box{slab(0.3125)}, rim(0.125, 1)...), true
	case "hopper":
		return append([]Box{slab(0.6875)}, rim(0.125, 1)...), true
	}
	return nil, false
}
