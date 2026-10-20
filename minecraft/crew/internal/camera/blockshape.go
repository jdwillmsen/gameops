package camera

import (
	"math"
	"strconv"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

// renderName is a block's name for the camera. The page cannot see block
// states, so the blocks whose look depends on one carry their boxes in the
// name: a slab in the top half, a stair facing any way and every door and
// trapdoor would otherwise be drawn in one fixed pose, leaving a hole where
// the block really is. Boxes come from the same table the unit's pathing uses,
// in sixteenths of a block.
func renderName(name string, props map[string]any) string {
	var boxes []shape.Box
	switch {
	case strings.Contains(name, "double_slab"):
		return name
	case strings.HasSuffix(name, "_slab"):
		if v, _ := props["minecraft:vertical_half"].(string); v != "top" {
			return name
		}
		boxes = shape.Boxes(name, props)
	case strings.HasSuffix(name, "_stairs"), strings.HasSuffix(name, "_trapdoor"), name == "trapdoor",
		strings.HasSuffix(name, "_door"):
		boxes = shape.Boxes(name, props)
	case name == "snow_layer":
		// The collision box is a layer lower than the picture.
		h := 0
		switch v := props["height"].(type) {
		case int32:
			h = int(v)
		case int:
			h = v
		}
		boxes = []shape.Box{{Max: [3]float32{1, float32(h+1) / 8, 1}}}
	default:
		return name
	}
	if len(boxes) == 0 {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	for i, box := range boxes {
		if i == 0 {
			b.WriteByte('@')
		} else {
			b.WriteByte(';')
		}
		for k := 0; k < 6; k++ {
			v := float64(box.Min[k%3])
			if k >= 3 {
				// Fence and wall collision rises past the block; the picture
				// does not.
				v = math.Min(float64(box.Max[k%3]), 1)
			}
			if k > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(int(math.Round(v * 16))))
		}
	}
	return b.String()
}
