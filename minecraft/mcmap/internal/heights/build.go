package heights

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// heightmapBytes is the heightmap at the start of a chunk's Data3D record:
// 256 little-endian 16-bit values, one per column at index z*16+x, each the
// height of the first air above the column's highest block, counted from
// the bottom of the dimension. So 127 is over an ocean whose surface is y
// 62 in a world that starts at y -64. The biomes follow it, and are read
// elsewhere.
const heightmapBytes = Columns * 2

var errRecord = errors.New("record is too short to hold a heightmap")

// FromRecord reads the heightmap off the front of a Data3D record.
func FromRecord(record []byte, out *[Columns]int16) error {
	if len(record) < heightmapBytes {
		return errRecord
	}
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(record[i*2:]))
	}
	return nil
}

// Builder puts a Map together a chunk at a time.
type Builder struct {
	m    *Map
	room int
}

// NewBuilder starts the heights of a snapshot taken at at, in which the
// chunk count found censusChunks chunks, or -1 if it found nothing.
func NewBuilder(at time.Time, censusChunks int) *Builder {
	m := &Map{SnapshotAt: at, CensusChunks: censusChunks}
	for i := range m.layers {
		m.layers[i] = &layer{cells: map[uint64]uint32{}}
	}
	return &Builder{m: m, room: maxChunks}
}

// Add takes the heights of one chunk from its Data3D record. A chunk
// already held is not replaced, and neither it nor a chunk past the edge
// of any world or past what is kept is an error: the biome pass counts
// those. A dimension that is not one of the three, or a record that holds
// no heightmap, is refused.
func (b *Builder) Add(d chunks.Dimension, cx, cz int32, record []byte) error {
	var columns [Columns]int16
	if err := FromRecord(record, &columns); err != nil {
		return err
	}
	return b.AddColumns(d, cx, cz, &columns)
}

// AddColumns is Add for heights already read, each at index z*16+x and
// counted as the record counts them.
func (b *Builder) AddColumns(d chunks.Dimension, cx, cz int32, columns *[Columns]int16) error {
	l := b.m.layer(d)
	if l == nil {
		return errors.New("not a dimension of the world")
	}
	if cx < -maxChunkCoordinate || cx > maxChunkCoordinate || cz < -maxChunkCoordinate || cz > maxChunkCoordinate {
		return nil
	}
	key := cellKey(cx, cz)
	if _, held := l.cells[key]; held {
		return nil
	}
	low, high := columns[0], columns[0]
	for _, v := range columns {
		low, high = min(low, v), max(high, v)
	}
	span := int(high) - int(low)
	tier := 0
	switch {
	case span == 0:
	case span <= 1:
		tier = 1
	case span <= 3:
		tier = 2
	case span <= 15:
		tier = 3
	case span <= 255:
		tier = 4
	default:
		tier = escapeTier
	}
	if tier == 0 {
		if b.room <= 0 {
			return nil
		}
		b.room--
		l.cells[key] = uint32(uint16(low))
		return nil
	}
	if b.room <= 0 || len(l.data)+recordSize(tier) > maxData {
		return nil
	}
	b.room--
	ref := uint32(tier)<<tierShift | uint32(len(l.data))
	if tier == escapeTier {
		for _, v := range columns {
			l.data = binary.LittleEndian.AppendUint16(l.data, uint16(v))
		}
	} else {
		bits := tierBits[tier]
		l.data = binary.LittleEndian.AppendUint16(l.data, uint16(low))
		start := len(l.data)
		l.data = append(l.data, make([]byte, Columns*int(bits)/8)...)
		packed := l.data[start:]
		for i, v := range columns {
			at := i * int(bits)
			packed[at>>3] |= byte(int(v)-int(low)) << (at & 7)
		}
	}
	l.cells[key] = ref
	return nil
}

// Map is what has been added. The Builder must not be used after.
func (b *Builder) Map() *Map {
	for _, l := range b.m.layers {
		// A table that grew by doubling carries room it will never use.
		l.data = append([]byte(nil), l.data...)
	}
	return b.m
}
