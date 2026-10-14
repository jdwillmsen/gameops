// Package heights keeps the surface height of every column of the chunks a
// world has generated, exactly, and answers for one column or a rectangle
// of them.
//
// "Surface" is the heightmap the game itself stores, which is the top of
// the highest block of any kind: the crown of a tree, the top of an ice
// sheet and the surface of the sea, not the ground or the sea floor under
// them. About a third of an ocean world's columns top out in water and a
// fifth in leaves. Finding the ground would mean reading the sub-chunks,
// which this deliberately does not: what is served from here is no more
// than the terrain tiles already show.
package heights

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	// Columns in a chunk, at index z*16+x.
	Columns = 256
	// TileSize is a tile's width and height in pixels, as the terrain's is.
	TileSize = 256

	// Unknown is what a rectangle holds for a column with no surface to
	// report: in a chunk the world has not generated, or in one whose
	// column has no block at all, as in the void of the end. It is below
	// any height a world can have.
	Unknown int16 = math.MinInt16

	// maxChunks is the most chunks kept, over all dimensions: the bound
	// the biomes are held to, so that the two cover the same chunks.
	maxChunks = 1_000_000
	// maxData is the most bytes of columns kept for a dimension, which is
	// what a chunk's offset into them has room for. A chunk is at most 512.
	maxData = 1 << payloadBits
	// maxChunkCoordinate is past the edge of any Bedrock world, and keeps
	// a chunk's block coordinates inside 32 bits.
	maxChunkCoordinate = 2_000_000
	// maxRectColumns is the most columns one Rect reads, 4,096 on a side:
	// a request for more would walk every chunk in a box that may hold
	// none.
	maxRectColumns = 1 << 24
)

// A chunk is kept as the least it needs to be exact. Its lowest column is
// the base, and each column is its distance above the base in as few bits
// as the highest of them takes, from this table; the nearest sizes that
// divide a byte, because a column then never straddles two. A chunk with
// every column alike is no more than the one height. A chunk that spans
// more than 255 is the escape: sixteen bits a column, as stored.
//
// Measured on the FWB world, a quarter of the overworld's chunks are flat
// or within one block, and none but the escape needs more than eight bits,
// so the 144,000 chunks come to a fifth of the 74 MB that sixteen bits a
// column would be.
var tierBits = [...]uint{0, 1, 2, 4, 8, 16}

const (
	// A chunk's reference is its tier above and, below, what the tier
	// says: the height itself of a flat chunk, otherwise where its record
	// begins in the layer's data.
	payloadBits = 28
	tierShift   = payloadBits
	payloadMask = 1<<payloadBits - 1
	// escapeTier stores a chunk's columns as they were.
	escapeTier = len(tierBits) - 1
)

// recordSize is how many bytes a chunk of the tier takes in the layer's
// data: the base and the packed columns, or the columns alone.
func recordSize(tier int) int {
	bits := int(tierBits[tier])
	if tier == escapeTier {
		return Columns * 2
	}
	return 2 + Columns*bits/8
}

type layer struct {
	// cells is every chunk, by position, as its reference.
	cells map[uint64]uint32
	data  []byte
}

// Map is the surface heights of every generated chunk as of one snapshot.
// It is never changed once built, so any number of requests may read it.
type Map struct {
	// SnapshotAt is when the snapshot it was read from was taken.
	SnapshotAt time.Time
	// CensusChunks is how many chunks the world held then, by the chunk
	// count's reckoning, or -1 if nothing had counted them.
	CensusChunks int

	layers [3]*layer
}

func cellKey(cx, cz int32) uint64 { return uint64(uint32(cx))<<32 | uint64(uint32(cz)) }

func (m *Map) layer(d chunks.Dimension) *layer {
	if m == nil || d < 0 || int(d) >= len(m.layers) {
		return nil
	}
	return m.layers[d]
}

// Floor is the lowest block of a dimension, in world y: the number its
// stored heights are counted from. It is false for a dimension that is not
// one of the three.
func Floor(d chunks.Dimension) (int32, bool) {
	switch d {
	case chunks.Overworld:
		return -64, true
	case chunks.Nether, chunks.End:
		return 0, true
	}
	return 0, false
}

// Chunks is how many chunks of the dimension are held.
func (m *Map) Chunks(d chunks.Dimension) int {
	if l := m.layer(d); l != nil {
		return len(l.cells)
	}
	return 0
}

// Bytes is about how much memory the heights take.
func (m *Map) Bytes() int {
	n := 0
	if m == nil {
		return 0
	}
	for _, l := range m.layers {
		if l == nil {
			continue
		}
		// A map slot is its key, its reference and the table's own
		// overhead, near enough to twenty-four bytes.
		n += len(l.cells)*24 + len(l.data)
	}
	return n
}

// raw is the stored height of one column of the chunk with this reference:
// blocks above the floor of the first air over its highest block.
func (l *layer) raw(ref uint32, i int) int16 {
	tier := int(ref >> tierShift)
	payload := int(ref & payloadMask)
	switch tier {
	case 0:
		return int16(payload)
	case escapeTier:
		return int16(binary.LittleEndian.Uint16(l.data[payload+i*2:]))
	}
	bits := tierBits[tier]
	rec := l.data[payload:]
	at := i * int(bits)
	v := int(rec[2+at>>3]>>(at&7)) & (1<<bits - 1)
	return int16(int(int16(binary.LittleEndian.Uint16(rec))) + v)
}

// expand is all 256 raw heights of the chunk with this reference.
func (l *layer) expand(ref uint32, out *[Columns]int16) {
	tier := int(ref >> tierShift)
	payload := int(ref & payloadMask)
	switch tier {
	case 0:
		v := int16(payload)
		for i := range out {
			out[i] = v
		}
	case escapeTier:
		rec := l.data[payload:]
		for i := range out {
			out[i] = int16(binary.LittleEndian.Uint16(rec[i*2:]))
		}
	default:
		bits := tierBits[tier]
		rec := l.data[payload:]
		base := int(int16(binary.LittleEndian.Uint16(rec)))
		packed := rec[2:]
		mask := 1<<bits - 1
		for i := range out {
			at := i * int(bits)
			out[i] = int16(base + int(packed[at>>3]>>(at&7))&mask)
		}
	}
}

// world turns a stored height into the y of the column's highest block.
// The stored value counts the first air above it, so the block is one
// below; a column with nothing in it has no block.
func world(raw int16, floor int32) (int16, bool) {
	if raw <= 0 {
		return Unknown, false
	}
	return int16(floor + int32(raw) - 1), true
}

// At is the y of the highest block in the column at block x, z: the
// surface, which is the top of any tree, ice or water there and not the
// ground under it. It is false where the world has generated no chunk, or
// the column has no block at all.
func (m *Map) At(d chunks.Dimension, x, z int32) (int32, bool) {
	l := m.layer(d)
	floor, known := Floor(d)
	if l == nil || !known {
		return 0, false
	}
	ref, ok := l.cells[cellKey(x>>4, z>>4)]
	if !ok {
		return 0, false
	}
	y, ok := world(l.raw(ref, int(z&15)*16+int(x&15)), floor)
	if !ok {
		return 0, false
	}
	return int32(y), true
}

// Surface is At for telling how high a marker stands above or below the
// ground it is on. The nether has none to give: its heightmap is the
// bedrock roof over the whole of it, which every marker is under and so
// says nothing of where in the nether one is.
func (m *Map) Surface(d chunks.Dimension, x, z int32) (int32, bool) {
	if d == chunks.Nether {
		return 0, false
	}
	return m.At(d, x, z)
}

// Rect reads the surface y of the w by h columns whose lowest x and z are
// minX and minZ into out, a row of w for each z, so that the column at
// block x, z is out[(z-minZ)*w+x-minX]. A column with no surface is
// Unknown. It says whether any column had one, and false too if the
// rectangle is empty, past the edge of any world or too large for out.
//
// It is what a hillshade or a contour wants for a tile and the ring of
// blocks around it, since a slope is a difference between neighbours.
func (m *Map) Rect(d chunks.Dimension, minX, minZ int32, w, h int, out []int16) bool {
	if w <= 0 || h <= 0 || w > maxRectColumns || h > maxRectColumns || w*h > maxRectColumns || len(out) < w*h {
		return false
	}
	out = out[:w*h]
	for i := range out {
		out[i] = Unknown
	}
	l := m.layer(d)
	floor, known := Floor(d)
	if l == nil || !known || len(l.cells) == 0 || !inWorld(int64(minX)) || !inWorld(int64(minZ)) ||
		!inWorld(int64(minX)+int64(w)) || !inWorld(int64(minZ)+int64(h)) {
		return false
	}
	found := false
	var cols [Columns]int16
	maxX, maxZ := minX+int32(w)-1, minZ+int32(h)-1
	for cz := minZ >> 4; cz <= maxZ>>4; cz++ {
		for cx := minX >> 4; cx <= maxX>>4; cx++ {
			ref, ok := l.cells[cellKey(cx, cz)]
			if !ok {
				continue
			}
			l.expand(ref, &cols)
			// The part of this chunk inside the rectangle, in blocks.
			x0, x1 := max(cx*16, minX), min(cx*16+15, maxX)
			z0, z1 := max(cz*16, minZ), min(cz*16+15, maxZ)
			for z := z0; z <= z1; z++ {
				row := out[int(z-minZ)*w+int(x0-minX):]
				src := cols[int(z&15)*16+int(x0&15):]
				for i := range int(x1-x0) + 1 {
					if y, ok := world(src[i], floor); ok {
						row[i], found = y, true
					}
				}
			}
		}
	}
	return found
}

// inWorld says a block coordinate is inside what a chunk coordinate can be.
func inWorld(v int64) bool { return v >= -maxChunkCoordinate*16 && v <= maxChunkCoordinate*16 }

// Tile reads the surface y of the columns a map tile draws, addressed
// exactly as the terrain's and the biomes' tiles are: at zoom 0 a pixel is
// a block and tile x, y starts at block x*256, z y*256; each zoom below
// doubles the blocks to a pixel, and each above halves them. A pixel is
// the column under its middle, at out[py*TileSize+px], and is Unknown where
// there is no surface. It says whether any pixel had one.
func (m *Map) Tile(d chunks.Dimension, zoom, tx, ty int, out *[TileSize * TileSize]int16) bool {
	for i := range out {
		out[i] = Unknown
	}
	l := m.layer(d)
	floor, known := Floor(d)
	if l == nil || !known || len(l.cells) == 0 {
		return false
	}
	blocks := math.Ldexp(1, -zoom)
	column := func(tile, pixel int) (int32, bool) {
		v := math.Floor((float64(tile)*TileSize + float64(pixel) + 0.5) * blocks)
		return int32(v), math.Abs(v) <= maxChunkCoordinate*16
	}
	var xs [TileSize]int32
	var inX [TileSize]bool
	for px := range xs {
		xs[px], inX[px] = column(tx, px)
	}
	found := false
	// Neighbouring pixels are mostly in one chunk, which is then expanded
	// once.
	var cols [Columns]int16
	var lastKey uint64
	have, held := false, false
	for py := range TileSize {
		z, inZ := column(ty, py)
		if !inZ {
			continue
		}
		row := out[py*TileSize:][:TileSize]
		for px, x := range xs {
			if !inX[px] {
				continue
			}
			if key := cellKey(x>>4, z>>4); !have || key != lastKey {
				lastKey, have = key, true
				var ref uint32
				ref, held = l.cells[key]
				if held {
					l.expand(ref, &cols)
				}
			}
			if !held {
				continue
			}
			if y, ok := world(cols[int(z&15)*16+int(x&15)], floor); ok {
				row[px], found = y, true
			}
		}
	}
	return found
}
