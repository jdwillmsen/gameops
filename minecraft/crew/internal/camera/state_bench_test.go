package camera

import (
	"testing"

	"github.com/df-mc/dragonfly/server/world"
)

// BenchmarkBlockState is what the planner pays for each cell it reads.
func BenchmarkBlockState(b *testing.B) {
	d := NewNetworkDecoder(nil)
	reg := world.DefaultBlockRegistry
	rid, ok := reg.StateToRuntimeID("minecraft:stone_stairs", map[string]any{
		"minecraft:corner": "none", "upside_down_bit": false, "weirdo_direction": int32(0),
	})
	if !ok {
		b.Fatal("stairs unknown")
	}
	h, _ := reg.RuntimeIDToHash(rid)
	if _, _, ok := d.BlockState(h); !ok {
		b.Fatal("stairs hash unknown")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d.BlockState(h)
	}
}
