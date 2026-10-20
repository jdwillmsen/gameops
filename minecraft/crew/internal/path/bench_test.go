package path

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// openStated is a stone floor with its top at y=1, size by size, under air.
func openStated(size int32) stated {
	w := stated{}
	for x := int32(0); x < size; x++ {
		for z := int32(0); z < size; z++ {
			w[protocol.BlockPos{x, 0, z}] = block{"stone", nil}
			for y := int32(1); y <= 4; y++ {
				w[protocol.BlockPos{x, y, z}] = block{"air", nil}
			}
		}
	}
	return w
}

// BenchmarkFindOpenGround is a straight 55-block walk, the common replan.
func BenchmarkFindOpenGround(b *testing.B) {
	w := openStated(60)
	from, to := protocol.BlockPos{2, 1, 2}, protocol.BlockPos{57, 1, 2}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Find(w, from, to, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindExhaustsTheBudget is a goal no walk reaches, so the search
// spends all its nodes.
func BenchmarkFindExhaustsTheBudget(b *testing.B) {
	w := openStated(80)
	// A standable island in a ring of fences.
	for x := int32(40); x <= 42; x++ {
		for z := int32(40); z <= 42; z++ {
			if x != 41 || z != 41 {
				w[protocol.BlockPos{x, 1, z}] = block{"stone", nil}
				w[protocol.BlockPos{x, 2, z}] = block{"stone", nil}
			}
		}
	}
	from, to := protocol.BlockPos{2, 1, 2}, protocol.BlockPos{41, 1, 41}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Find(w, from, to, 0); err == nil {
			b.Fatal("the island must be unreachable")
		}
	}
}
