// Package biomes reads the biomes a world has stored for the chunks it has
// generated, and keeps them in a form that can be drawn as map tiles,
// searched, and asked about one block at a time.
//
// Nothing here uses the seed. A biome read from the world is a fact about
// it; one worked out from a seed is a calculation, and this world's seed
// has already been seen not to describe it.
package biomes

import (
	"encoding/binary"
	"errors"
)

// A chunk's record 43 ("Data3D") is its heightmap followed by its biomes.
//
// The heightmap is 256 little-endian 16-bit values, one per column at
// index z*16+x: the height of the first air above the column's highest
// block, counted from the bottom of the dimension, so 127 over an ocean
// whose surface is y 62 in a world that starts at y -64.
//
// The biomes follow as one palettised storage per 16 blocks of height,
// bottom first: 24 in the overworld, 8 in the nether, 16 in the end. Each
// begins with a byte whose top seven bits are the bits per entry:
//
//	0     one biome for all 4,096 blocks: a 32-bit id follows
//	127   the same as the storage below; nothing follows
//	else  4,096 indices packed low bits first into 32-bit words, as many
//	      to a word as fit whole, then a 32-bit palette length and that
//	      many 32-bit ids
//
// An entry's index is x<<8 | z<<4 | y. Everything is little-endian.
const (
	// TagData3D is the record's tag in a chunk key.
	TagData3D = 0x2b

	heightmapBytes = 512
	// Columns in a chunk, and so the biomes one chunk reduces to.
	Columns = 256
	// More storages than any dimension has. It bounds what one record can
	// make a decode hold.
	maxStorages    = 64
	storageEntries = 4096
	sameAsBelow    = 0x7f
)

var errRecord = errors.New("biome record is not a heightmap followed by palettised storages")

type storage struct {
	bits    uint
	words   []byte
	palette []byte
}

func (s *storage) at(x, y, z int) (uint32, bool) {
	index := 0
	if s.bits != 0 {
		perWord := 32 / s.bits
		entry := uint(x<<8 | z<<4 | y)
		word := binary.LittleEndian.Uint32(s.words[entry/perWord*4:])
		index = int(word >> (entry % perWord * s.bits) & (1<<s.bits - 1))
	}
	// A palette shorter than its indices is damage, and there is no biome
	// to put there.
	if index*4 >= len(s.palette) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(s.palette[index*4:]), true
}

// surface reduces a record to one biome id per column, at index z*16+x: the
// biome of the column's highest block. A map looks down on the world, and
// under most of it the biome changes on the way down, to a cave biome or
// the deep dark, so the biome at any fixed height is not the one a player
// standing there sees.
//
// A record that does not parse to its last byte is refused whole.
func surface(value []byte, out *[Columns]uint32) error {
	if len(value) <= heightmapBytes {
		return errRecord
	}
	var stores [maxStorages]storage
	n := 0
	for b := value[heightmapBytes:]; len(b) > 0; n++ {
		if n == maxStorages {
			return errRecord
		}
		bits := uint(b[0] >> 1)
		b = b[1:]
		if bits == sameAsBelow {
			if n == 0 {
				return errRecord
			}
			stores[n] = stores[n-1]
			continue
		}
		s := storage{bits: bits}
		count := 1
		switch bits {
		case 0:
		case 1, 2, 3, 4, 5, 6, 8, 16:
			perWord := int(32 / bits)
			size := (storageEntries + perWord - 1) / perWord * 4
			if len(b) < size+4 {
				return errRecord
			}
			s.words, b = b[:size], b[size:]
			count = int(binary.LittleEndian.Uint32(b))
			b = b[4:]
			if count < 1 || count > storageEntries {
				return errRecord
			}
		default:
			return errRecord
		}
		if len(b) < count*4 {
			return errRecord
		}
		s.palette, b = b[:count*4], b[count*4:]
		stores[n] = s
	}
	for i := range out {
		// One below the first air; a column with nothing in it, as in the
		// void of the end, has only its lowest storage to go by.
		y := int(int16(binary.LittleEndian.Uint16(value[i*2:]))) - 1
		y = min(max(y, 0), n*16-1)
		id, ok := stores[y>>4].at(i&15, y&15, i>>4)
		if !ok {
			return errRecord
		}
		out[i] = id
	}
	return nil
}
