package camera

import (
	"fmt"
	"math/rand"
	"testing"
)

// benchChunk is a sub-chunk using k distinct materials with a plausible skew:
// most cells are the first few.
func benchChunk(k int) *[subChunkCells]uint16 {
	rng := rand.New(rand.NewSource(int64(k)))
	var c [subChunkCells]uint16
	for i := range c {
		c[i] = uint16(1 + int(rng.ExpFloat64()*float64(k)/4)%k)
	}
	for m := 0; m < k; m++ {
		c[m] = uint16(1 + m)
	}
	return &c
}

func BenchmarkEncodeSubChunk(b *testing.B) {
	for _, k := range []int{2, 16, 64, 200, 600} {
		c := benchChunk(k)
		b.Run(fmt.Sprintf("palette%d", k), func(b *testing.B) {
			var size int
			for i := 0; i < b.N; i++ {
				size = len(encodeSubChunk(0, 0, 0, c))
			}
			b.ReportMetric(float64(size), "frame-bytes")
		})
	}
}

func BenchmarkDecodeSubChunk(b *testing.B) {
	for _, k := range []int{2, 16, 64, 200, 600} {
		f := encodeSubChunk(0, 0, 0, benchChunk(k))
		b.Run(fmt.Sprintf("palette%d", k), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, _, _, _, err := decodeSubChunk(f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMaterialsGrowth is the bytes a viewer receives while the table
// grows to n entries one material at a time, which is how the hub pushes it:
// the whole table every time (wire 3) against only the new entry (wire 4).
func BenchmarkMaterialsGrowth(b *testing.B) {
	for _, n := range []int{255, 1500} {
		b.Run(fmt.Sprintf("n%d", n), func(b *testing.B) {
			tab := newMaterialTable()
			var whole, delta int
			for i := 0; i < n; i++ {
				tab.id(fmt.Sprintf("minecraft:white_concrete_%04d", i))
				whole += len(encodeMaterials(tab.all()))
				delta += len(encodeMaterials(tab.from(i)))
			}
			for i := 0; i < b.N; i++ {
				_ = encodeMaterials(tab.from(n - 1))
			}
			b.ReportMetric(float64(whole), "whole-table-bytes")
			b.ReportMetric(float64(delta), "delta-bytes")
		})
	}
}

// BenchmarkDowngradeSubChunk is what a connection from a page that predates
// wire 4 pays for each sub-chunk.
func BenchmarkDowngradeSubChunk(b *testing.B) {
	d := &v3Downgrader{}
	f := encodeSubChunk(0, 0, 0, benchChunk(64))
	for i := 0; i < b.N; i++ {
		if _, err := d.convert(f); err != nil {
			b.Fatal(err)
		}
	}
}
