// Package shape gives the collision boxes of a block from its name and
// states, the way the server's movement sees it. A unit that treats every
// non-passable block as a full cube cannot walk onto a slab or stair and
// believes a fence is only a block high.
//
// The boxes follow the Bedrock collision shapes; a test checks them against
// every state dragonfly's block table knows.
package shape

import (
	"math"
	"strings"
)

// Box is an axis-aligned box inside one block, in block-local coordinates.
type Box struct{ Min, Max [3]float32 }

var full = Box{Max: [3]float32{1, 1, 1}}

// Full is the box of an ordinary solid block.
func Full() Box { return full }

const (
	// Fence, wall and gate collision reaches half a block above the model, so
	// that nothing can jump them.
	tallHeight = 1.5

	carpetHeight = 1.0 / 16
	// Panel thicknesses as the server measures them: a ladder is exactly three
	// sixteenths, but a door or trapdoor is a hair thinner.
	ladderPanel = 3.0 / 16
	hatchPanel  = 0.1825

	fencePost = 0.375
	wallPost  = 0.25
)

// Empty reports whether a block has no collision at all. The list is
// deliberately short: a block missing from it is a solid cube, which is the
// safe error. Liquids are absent on purpose, as swimming is not modelled.
func Empty(name string) bool {
	switch name {
	case "air", "cave_air", "void_air", "short_grass", "tall_grass", "fern", "large_fern",
		"dandelion", "poppy", "torch", "deadbush", "vine", "glow_lichen",
		"rail", "golden_rail", "detector_rail", "activator_rail":
		return true
	}
	return strings.HasSuffix(name, "_sapling") || strings.HasSuffix(name, "_pressure_plate")
}

// Climbable reports whether a unit inside the block climbs it.
func Climbable(name string) bool { return name == "ladder" || name == "vine" }

// Fallback, when set, answers for the blocks Boxes does not model itself. It
// returns false for a block it does not know, which is then a cube. Set it
// once at start-up, before the first Boxes call.
var Fallback func(name string, props map[string]any) ([]Box, bool)

// Boxes returns the collision boxes of a block. Missing states take their
// default, so a caller that only knows the name still gets a sensible shape.
func Boxes(name string, p map[string]any) []Box {
	switch {
	case Empty(name):
		return nil
	case strings.HasSuffix(name, "_slab") && (strings.HasPrefix(name, "double_") || strings.Contains(name, "_double_")):
		return []Box{full}
	case strings.HasSuffix(name, "_slab"):
		if str(p, "minecraft:vertical_half") == "top" {
			return []Box{{Min: [3]float32{0, 0.5, 0}, Max: [3]float32{1, 1, 1}}}
		}
		return []Box{{Max: [3]float32{1, 0.5, 1}}}
	case strings.HasSuffix(name, "_stairs"):
		return stairs(p)
	case name == "snow_layer":
		return snowLayer(p)
	case name == "carpet" || strings.HasSuffix(name, "_carpet"):
		return []Box{{Max: [3]float32{1, carpetHeight, 1}}}
	case name == "ladder":
		return []Box{edge(ladderSide(num(p, "facing_direction")), ladderPanel)}
	case name == "trapdoor" || strings.HasSuffix(name, "_trapdoor"):
		return trapdoor(p)
	case strings.HasSuffix(name, "_door"):
		return door(p)
	case name == "fence_gate" || strings.HasSuffix(name, "_fence_gate"):
		return fenceGate(p)
	case name == "fence" || strings.HasSuffix(name, "_fence"):
		return fence(p)
	case strings.HasSuffix(name, "_wall"):
		return wall(p)
	}
	if boxes, ok := partial(name); ok {
		return boxes
	}
	if Fallback != nil {
		if boxes, ok := Fallback(name, p); ok {
			return boxes
		}
	}
	return []Box{full}
}

type dir uint8

// The numbering is dragonfly's, which the state tables below were derived
// from.
const (
	north dir = iota
	south
	west
	east
)

func (d dir) opposite() dir { return [...]dir{south, north, east, west}[d] }
func (d dir) right() dir    { return [...]dir{east, west, north, south}[d] }
func (d dir) left() dir     { return d.right().opposite() }

// axis is 0 for x and 2 for z.
func (d dir) axis() int {
	if d == north || d == south {
		return 2
	}
	return 0
}

func (d dir) positive() bool { return d == south || d == east }

func parseDir(s string) dir {
	switch s {
	case "north":
		return north
	case "west":
		return west
	case "east":
		return east
	}
	return south
}

// edge is a full-block panel of the given thickness against the block's side
// d.
func edge(d dir, thickness float32) Box {
	b := full
	if d.positive() {
		b.Min[d.axis()] = 1 - thickness
	} else {
		b.Max[d.axis()] = thickness
	}
	return b
}

// ladderSide is the side of the block a ladder with the given facing_direction
// is mounted on, which is opposite the way it faces.
func ladderSide(facing int) dir {
	switch facing {
	case 2:
		return south
	case 3:
		return north
	case 4:
		return east
	case 5:
		return west
	}
	return south
}

func trapdoor(p map[string]any) []Box {
	if boolean(p, "open_bit") {
		return []Box{edge([...]dir{west, east, north, south}[num(p, "direction")&3], hatchPanel)}
	}
	if boolean(p, "upside_down_bit") {
		return []Box{{Min: [3]float32{0, 1 - hatchPanel, 0}, Max: [3]float32{1, 1, 1}}}
	}
	return []Box{{Max: [3]float32{1, hatchPanel, 1}}}
}

func door(p map[string]any) []Box {
	facing := parseDir(str(p, "minecraft:cardinal_direction")).left()
	side := facing
	if boolean(p, "open_bit") {
		side = facing.right()
		if boolean(p, "door_hinge_bit") {
			side = facing.left()
		}
	}
	return []Box{edge(side.opposite(), hatchPanel)}
}

func fenceGate(p map[string]any) []Box {
	if boolean(p, "open_bit") {
		return nil
	}
	d := parseDir(str(p, "minecraft:cardinal_direction"))
	b := Box{Max: [3]float32{1, tallHeight, 1}}
	b.Min[d.axis()], b.Max[d.axis()] = fencePost, 1-fencePost
	return []Box{b}
}

func fence(p map[string]any) []Box {
	var out []Box
	for _, a := range [...]struct {
		key string
		d   dir
	}{{"minecraft:connection_north", north}, {"minecraft:connection_south", south},
		{"minecraft:connection_west", west}, {"minecraft:connection_east", east}} {
		if boolean(p, a.key) {
			out = append(out, arm(a.d, fencePost, tallHeight))
		}
	}
	if len(out) == 0 {
		return []Box{{Min: [3]float32{fencePost, 0, fencePost}, Max: [3]float32{1 - fencePost, tallHeight, 1 - fencePost}}}
	}
	return out
}

// arm reaches from the block's side d to the far edge of the post, spanning
// the post's width across.
func arm(d dir, post, height float32) Box {
	b := Box{Min: [3]float32{post, 0, post}, Max: [3]float32{1 - post, height, 1 - post}}
	if d.positive() {
		b.Max[d.axis()] = 1
	} else {
		b.Min[d.axis()] = 0
	}
	return b
}

func wall(p map[string]any) []Box {
	out := []Box{{Min: [3]float32{wallPost, 0, wallPost}, Max: [3]float32{1 - wallPost, tallHeight, 1 - wallPost}}}
	for _, a := range [...]struct {
		key string
		d   dir
	}{{"wall_connection_type_north", north}, {"wall_connection_type_south", south},
		{"wall_connection_type_west", west}, {"wall_connection_type_east", east}} {
		if s := str(p, a.key); s == "short" || s == "tall" {
			out = append(out, arm(a.d, wallPost, tallHeight))
		}
	}
	return out
}

// snowLayer: state height counts layers above the first, but the collision
// is one layer lower than what is drawn, so a single layer cannot be felt.
func snowLayer(p map[string]any) []Box {
	h := num(p, "height")
	if h <= 0 {
		return nil
	}
	return []Box{{Max: [3]float32{1, float32(h) / 8, 1}}}
}

func stairs(p map[string]any) []Box {
	facing := [...]dir{east, west, south, north}[num(p, "weirdo_direction")&3]
	upside := boolean(p, "upside_down_bit")
	corner := str(p, "minecraft:corner")

	base := Box{Max: [3]float32{1, 0.5, 1}}
	lo, hi := float32(0.5), float32(1)
	if upside {
		base = Box{Min: [3]float32{0, 0.5, 0}, Max: [3]float32{1, 1, 1}}
		lo, hi = 0, 0.5
	}
	out := []Box{base}
	// A quarter of the block above the base slab, on side d.
	half := func(b Box, d dir) Box {
		if d.positive() {
			b.Min[d.axis()] = 0.5
		} else {
			b.Max[d.axis()] = 0.5
		}
		return b
	}
	level := full
	level.Min[1], level.Max[1] = lo, hi

	inner := strings.HasPrefix(corner, "inner")
	outer := strings.HasPrefix(corner, "outer")
	if !outer {
		out = append(out, half(level, facing))
	}
	if inner || outer {
		side := facing.right()
		if strings.HasSuffix(corner, "_left") {
			side = facing.left()
		}
		front := facing
		if inner {
			front = facing.opposite()
		}
		out = append(out, half(half(level, front), side))
	}
	return out
}

func num(p map[string]any, key string) int {
	switch v := p[key].(type) {
	case int32:
		return int(v)
	case int:
		return v
	case uint8:
		return int(v)
	case int8:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(math.Round(v))
	}
	return 0
}

func boolean(p map[string]any, key string) bool {
	switch v := p[key].(type) {
	case bool:
		return v
	default:
		return num(p, key) != 0
	}
}

func str(p map[string]any, key string) string {
	s, _ := p[key].(string)
	return s
}
