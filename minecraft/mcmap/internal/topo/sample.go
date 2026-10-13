package topo

import (
	"math"
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// maxBlock is past the edge of any Bedrock world.
const maxBlock = 32_000_000

// fine is how many parts of a block a height is kept in once it has been
// read. Averaged to whole blocks it would jump across a contour from one
// pixel to the next wherever the land lay within half a block of one, and
// a line would flicker along it.
const fine = 16

// zoomPlan is how heights are taken for the pixels of a tile at a zoom.
type zoomPlan struct {
	// blocks is the width of a pixel in blocks, or 1 where a block is
	// several pixels, which is scale of them.
	blocks, scale int
	// mean takes every column a pixel covers; otherwise it takes the one
	// in the middle of each chunk it covers.
	mean bool
	// smooth is the radius, in pixels, of the blur that takes the trees
	// out of the land before it is lit or lined.
	smooth int
	// relief is how many times steeper than it is a slope is lit. Wider
	// pixels average the land flat, and the slopes that are left of it
	// are drawn up to be seen.
	relief float64
}

// maxMeanBlocks is the widest pixel that is read column by column. Beyond
// it a tile is a thousand chunks wide and four million columns.
const maxMeanBlocks = 4

func planOf(zoom int) zoomPlan {
	zoom = min(max(zoom, MinZoom), MaxZoom)
	if zoom > 0 {
		return zoomPlan{blocks: 1, scale: 1 << zoom, smooth: 2, relief: 1}
	}
	p := zoomPlan{blocks: 1 << -zoom, scale: 1, smooth: 1, relief: 1 + 0.3*float64(-zoom)}
	p.mean = p.blocks > 1 && p.blocks <= maxMeanBlocks
	// A canopy is five blocks across and the pixels of a tile at one block
	// to a pixel resolve it; a pixel any wider holds it in its average.
	if p.blocks == 1 {
		p.smooth = 2
	}
	return p
}

// pixel is the width of a pixel in blocks.
func (p zoomPlan) pixel() float64 { return float64(p.blocks) / float64(p.scale) }

// ring is how many pixels round a tile are needed to light and line it:
// the blur, then a step either way. Where a block is several pixels the
// blur is done on blocks, before they are drawn smooth, and a pixel is
// all that is needed beyond.
func (p zoomPlan) ring() int {
	if p.scale > 1 {
		return 1
	}
	return p.smooth + 1
}

// work is the memory a tile is drawn in, which is the largest part of
// the cost of one that is not read from the heights: a tile at one block
// to a pixel is a quarter of a megabyte of rows, and the widest read
// millions of columns. They are kept for the next tile.
type work struct {
	raw, soft, cols, blocks []int16
	sums                    []int32
	counts                  []int16
}

var works = sync.Pool{New: func() any { return new(work) }}

func grow[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	return s[:n]
}

// sample reads the heights of the tile's pixels and a ring of ring pixels
// around it, side by side where side is TileSize+2*ring, into out. The
// pixels are those of the whole map's grid, so a tile and its neighbour
// read the same heights along the edge they share. It says whether any
// pixel has a surface.
func (w *work) sample(m *heights.Map, d chunks.Dimension, p zoomPlan, tx, ty, ring int, out []int16) bool {
	side := TileSize + 2*ring
	for i := range out[:side*side] {
		out[i] = heights.Unknown
	}
	x0 := int64(tx*TileSize-ring) * int64(p.blocks)
	z0 := int64(ty*TileSize-ring) * int64(p.blocks)
	span := int64(side) * int64(p.blocks)
	if x0 < -maxBlock || z0 < -maxBlock || x0+span > maxBlock || z0+span > maxBlock {
		return false
	}
	switch {
	case p.blocks == 1:
		if !m.Rect(d, int32(x0), int32(z0), side, side, out) {
			return false
		}
		for i, y := range out[:side*side] {
			if y != heights.Unknown {
				out[i] = y * fine
			}
		}
		return true
	case p.mean:
		return w.sampleMean(m, d, p, int32(x0), int32(z0), side, out)
	}
	return samplePoints(m, d, p, int32(x0), int32(z0), side, out)
}

// upsample draws the land of a tile that is finer than a block, and the
// ring of pixels round it, smooth: the blocks it covers and a margin of
// them are read and blurred as at a block to a pixel, and each pixel is
// then between the middles of the four blocks round it, by how near it is
// to each. A pixel is clear where its own block is, and takes only the
// blocks that have a surface of the four. Without it a slope is a
// staircase of squares, a line is as wide as a block, and a tile cut in
// another place would be a different picture. It says whether any pixel
// of the tile has a surface.
func (w *work) upsample(m *heights.Map, d chunks.Dimension, p zoomPlan, tx, ty, ring int, out []int16) bool {
	side := TileSize + 2*ring
	for i := range out[:side*side] {
		out[i] = heights.Unknown
	}
	s := p.scale
	across := TileSize / s
	// The blur's radius, and a block more either way for the four blocks a
	// pixel is between.
	margin := p.smooth + 2
	n := across + 2*margin
	x0, z0 := int64(tx*across-margin), int64(ty*across-margin)
	if x0 < -maxBlock || z0 < -maxBlock || x0+int64(n) > maxBlock || z0+int64(n) > maxBlock {
		return false
	}
	w.raw = grow(w.raw, n*n)
	if !m.Rect(d, int32(x0), int32(z0), n, n, w.raw) {
		return false
	}
	for i, y := range w.raw {
		if y != heights.Unknown {
			w.raw[i] = y * fine
		}
	}
	w.blocks = grow(w.blocks, n*n)
	w.smooth(w.raw, w.blocks, n, p.smooth)
	// Where each pixel's row and column is among the blocks: the block it
	// is in, the one before it and the weight of that.
	type place struct {
		in, before int
		after      float64
	}
	at := make([]place, side)
	for i := range at {
		// In blocks, from the middle of the first of the margin.
		pos := float64(margin) + (float64(i-ring)+0.5)/float64(s)
		in := int(pos)
		before := int(pos - 0.5)
		at[i] = place{in: in, before: before, after: pos - 0.5 - float64(before)}
	}
	found := false
	for j := range side {
		pj := at[j]
		for i := range side {
			pi := at[i]
			if w.blocks[pj.in*n+pi.in] == heights.Unknown {
				continue
			}
			var sum, weight float64
			for dz := range 2 {
				wz := 1 - pj.after
				if dz == 1 {
					wz = pj.after
				}
				for dx := range 2 {
					wx := 1 - pi.after
					if dx == 1 {
						wx = pi.after
					}
					if y := w.blocks[(pj.before+dz)*n+pi.before+dx]; y != heights.Unknown {
						sum += float64(y) * wx * wz
						weight += wx * wz
					}
				}
			}
			out[j*side+i] = int16(math.Round(sum / weight))
			if i >= ring && i < ring+TileSize && j >= ring && j < ring+TileSize {
				found = true
			}
		}
	}
	return found
}

// sampleMean reads every column the pixels cover and gives each pixel the
// mean of those that have a surface. A pixel is no wider than a sixteenth
// of a chunk, so it lies in one: a column with no surface is a lone gap,
// and with half of them gone a pixel is left clear.
func (w *work) sampleMean(m *heights.Map, d chunks.Dimension, p zoomPlan, x0, z0 int32, side int, out []int16) bool {
	b := p.blocks
	n := side * b
	w.cols = grow(w.cols, n*n)
	if !m.Rect(d, x0, z0, n, n, w.cols) {
		return false
	}
	for j := range side {
		for i := range side {
			sum, known := 0, 0
			for dz := range b {
				for _, y := range w.cols[(j*b+dz)*n+i*b:][:b] {
					if y != heights.Unknown {
						sum += int(y)
						known++
					}
				}
			}
			if known*2 > b*b {
				out[j*side+i] = int16((sum*fine + known/2) / known)
			}
		}
	}
	return true
}

// samplePoints takes a column in every chunk a pixel covers, which is one
// for pixels up to a chunk across and a few for the widest, and gives the
// pixel their mean. Reading every column would be millions to a tile.
func samplePoints(m *heights.Map, d chunks.Dimension, p zoomPlan, x0, z0 int32, side int, out []int16) bool {
	b := int32(p.blocks)
	k := max(b/16, 1)
	found := false
	for j := range int32(side) {
		for i := range int32(side) {
			sum, known := 0, 0
			for dz := range k {
				for dx := range k {
					x := x0 + i*b + (2*dx+1)*b/(2*k)
					z := z0 + j*b + (2*dz+1)*b/(2*k)
					if y, ok := m.At(d, x, z); ok {
						sum += int(y)
						known++
					}
				}
			}
			if known*2 > int(k*k) {
				out[j*int32(side)+i], found = int16((sum*fine+known/2)/known), true
			}
		}
	}
	return found
}

// smooth averages each pixel with the pixels within r of it, in a box,
// that have a surface. Clear pixels stay clear and add nothing to their
// neighbours, so the edge of the explored world is not a slope. It is a
// sum along each row and then down each column.
func (w *work) smooth(in, out []int16, side, r int) {
	if r <= 0 {
		copy(out, in)
		return
	}
	w.sums = grow(w.sums, side*side)
	w.counts = grow(w.counts, side*side)
	for j := range side {
		for i := range side {
			var s int32
			var n int16
			for _, y := range in[j*side+max(i-r, 0) : j*side+min(i+r, side-1)+1] {
				if y != heights.Unknown {
					s += int32(y)
					n++
				}
			}
			w.sums[j*side+i], w.counts[j*side+i] = s, n
		}
	}
	for j := range side {
		for i := range side {
			if in[j*side+i] == heights.Unknown {
				out[j*side+i] = heights.Unknown
				continue
			}
			var s, n int32
			for k := max(j-r, 0); k <= min(j+r, side-1); k++ {
				s += w.sums[k*side+i]
				n += int32(w.counts[k*side+i])
			}
			out[j*side+i] = int16((s + n/2) / n)
		}
	}
}
