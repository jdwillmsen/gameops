// Package blocktable gives the collision boxes of the blocks shape does not
// model itself, from dragonfly's block table: chests, paths, tables, pots,
// campfires and the rest of the blocks that are not full cubes. A block whose
// model needs its neighbours is left to the caller, which treats it as a cube.
//
// Importing it registers the dragonfly blocks, so it belongs in the binary and
// not in the tests of the packages that only use shape.
package blocktable

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	_ "github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

// solid lists blocks the table would call empty or partial but the unit must
// treat as a cube anyway: liquids, which are not swum through here, and blocks
// that hurt or move the unit.
func solid(name string) bool {
	switch name {
	case "water", "flowing_water", "lava", "flowing_lava", "fire", "soul_fire",
		"web", "portal", "end_portal", "end_gateway", "sweet_berry_bush", "magma",
		"bubble_column", "powder_snow", "wither_rose":
		return true
	}
	return strings.HasPrefix(name, "light_block")
}

type entry struct {
	boxes []shape.Box
	ok    bool
}

var cache sync.Map // string -> entry

// Boxes returns the boxes of the named block with the given states. ok is
// false when the table does not know the block, or when its shape depends on
// the blocks around it.
func Boxes(name string, props map[string]any) (boxes []shape.Box, ok bool) {
	if solid(name) {
		return nil, false
	}
	k := key(name, props)
	if e, hit := cache.Load(k); hit {
		return e.(entry).boxes, e.(entry).ok
	}
	boxes, ok = resolve(name, props)
	cache.Store(k, entry{boxes, ok})
	return boxes, ok
}

func key(name string, props map[string]any) string {
	if len(props) == 0 {
		return name
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		fmt.Fprintf(&b, "|%s=%v", k, props[k])
	}
	return b.String()
}

func resolve(name string, props map[string]any) (boxes []shape.Box, ok bool) {
	defer func() {
		// A model that reads its neighbours dereferences a world it was not
		// given.
		if recover() != nil {
			boxes, ok = nil, false
		}
	}()
	reg := world.DefaultBlockRegistry
	reg.Finalize()
	// Missing states take the table's defaults.
	rid, found := reg.StateToRuntimeID("minecraft:"+name, props)
	if !found {
		return nil, false
	}
	b, found := reg.BlockByRuntimeID(rid)
	if !found {
		return nil, false
	}
	return fromCube(b.Model().BBox(cube.Pos{}, nil)), true
}

func fromCube(bs []cube.BBox) []shape.Box {
	var out []shape.Box
	for _, b := range bs {
		mn, mx := b.Min(), b.Max()
		out = append(out, shape.Box{
			Min: [3]float32{float32(mn[0]), float32(mn[1]), float32(mn[2])},
			Max: [3]float32{float32(mx[0]), float32(mx[1]), float32(mx[2])},
		})
	}
	return out
}
