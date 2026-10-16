// Package layerstest writes made-up sub-chunks, as the game writes them, for
// the tests and the browser checks' harness to draw a world from. Nothing in
// it belongs to a real world: the blocks are a function the caller gives.
package layerstest

import (
	"encoding/binary"
	"math/bits"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/sandertv/gophertunnel/minecraft/nbt"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// BlocksPerSlice is every block of a 16-block-tall slice.
const BlocksPerSlice = 4096

// Blocks names the block at x and z within the chunk and y within the slice.
type Blocks func(x, y, z int) string

// Entry is a palette entry for a block, with states of the several kinds
// that a real entry's are made of, for the reader to step over.
func Entry(name string) []byte {
	b, err := nbt.MarshalEncoding(map[string]any{
		"name":    "minecraft:" + name,
		"states":  map[string]any{"deep": byte(1), "list": []any{int32(1), int32(2)}, "kind": "some"},
		"version": int32(18153475),
	}, nbt.LittleEndian)
	if err != nil {
		panic(err)
	}
	return b
}

// Storage encodes one layer. width is the bits to a block, or 0 for the
// narrowest that holds the palette, and -1 for a layer of one block that is
// written at no width at all.
func Storage(names [BlocksPerSlice]string, width int) []byte {
	var palette []string
	index := map[string]int{}
	var ix [BlocksPerSlice]int
	for i, n := range names {
		p, ok := index[n]
		if !ok {
			p = len(palette)
			index[n] = p
			palette = append(palette, n)
		}
		ix[i] = p
	}
	if width == -1 {
		if len(palette) != 1 {
			panic("a layer at no width holds one block")
		}
		return append([]byte{0}, Entry(palette[0])...)
	}
	if width == 0 {
		need := bits.Len(uint(len(palette) - 1))
		for _, w := range []int{1, 2, 3, 4, 5, 6, 8, 16} {
			if w >= need {
				width = w
				break
			}
		}
	}
	out := []byte{byte(width << 1)}
	perWord := 32 / width
	words := (BlocksPerSlice + perWord - 1) / perWord
	for w := range words {
		var word uint32
		for k := range perWord {
			if at := w*perWord + k; at < BlocksPerSlice {
				word |= uint32(ix[at]) << (k * width)
			}
		}
		out = binary.LittleEndian.AppendUint32(out, word)
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(palette)))
	for _, n := range palette {
		out = append(out, Entry(n)...)
	}
	return out
}

// Slice encodes a sub-chunk of the version, 1, 8 or 9, holding the layers.
func Slice(version byte, h int8, width int, layers ...Blocks) []byte {
	var out []byte
	switch version {
	case 1:
		out = []byte{1}
	case 8:
		out = []byte{8, byte(len(layers))}
	case 9:
		out = []byte{9, byte(len(layers)), byte(h)}
	default:
		panic("not a version of a sub-chunk this writes")
	}
	for _, f := range layers {
		var names [BlocksPerSlice]string
		for x := range 16 {
			for z := range 16 {
				for y := range 16 {
					names[x<<8|z<<4|y] = f(x, y, z)
				}
			}
		}
		out = append(out, Storage(names, width)...)
	}
	return out
}

// lowest is the height index of a dimension's lowest slice.
func lowest(d chunks.Dimension) int {
	if d == chunks.Overworld {
		return -4
	}
	return 0
}

// Chunk writes the slices of a chunk from the lowest of its dimension to
// the one that holds world y top, as version 9. world is given the position
// in the chunk and the y in the world.
func Chunk(db *leveldb.DB, pos chunks.Pos, top int, world func(x, y, z int) string) error {
	for h := lowest(pos.Dim); h <= top>>4; h++ {
		rec := Slice(9, int8(h), 0, func(x, y, z int) string { return world(x, h*16+y, z) })
		key := append(chunks.RecordKey(pos, 0x2f), byte(int8(h)))
		if err := db.Put(key, rec, nil); err != nil {
			return err
		}
	}
	return nil
}
