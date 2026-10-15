package layers

import (
	"encoding/binary"
	"math/bits"
	"path/filepath"
	"testing"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/sandertv/gophertunnel/minecraft/nbt"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Blocks of a made-up world are named by a function of where they are in
// the chunk: x and z from 0 to 15 and y in the slice from 0 to 15, or in
// the world where the function is given a world y.
type blockFunc func(x, y, z int) string

func entry(t testing.TB, name string) []byte {
	t.Helper()
	b, err := nbt.MarshalEncoding(map[string]any{
		"name":    "minecraft:" + name,
		"states":  map[string]any{"deep": byte(1), "list": []any{int32(1), int32(2)}, "kind": "some"},
		"version": int32(18153475),
	}, nbt.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// storage encodes one layer. width is the bits to a block, or 0 for the
// narrowest that holds the palette, and -1 for a layer of one block that
// is written at no width at all.
func storage(t testing.TB, names [blocksPerSub]string, width int) []byte {
	t.Helper()
	var palette []string
	index := map[string]int{}
	var ix [blocksPerSub]int
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
			t.Fatal("a layer at no width holds one block")
		}
		return append([]byte{0}, entry(t, palette[0])...)
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
	words := (blocksPerSub + perWord - 1) / perWord
	for w := range words {
		var word uint32
		for k := range perWord {
			if at := w*perWord + k; at < blocksPerSub {
				word |= uint32(ix[at]) << (k * width)
			}
		}
		out = binary.LittleEndian.AppendUint32(out, word)
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(palette)))
	for _, n := range palette {
		out = append(out, entry(t, n)...)
	}
	return out
}

// sliceRecord encodes a sub-chunk of the version, holding the layers,
// which are each a function of x, y, z in the slice.
func sliceRecord(t testing.TB, version byte, h int8, width int, layers ...blockFunc) []byte {
	t.Helper()
	var out []byte
	switch version {
	case 1:
		out = []byte{1}
	case 8:
		out = []byte{8, byte(len(layers))}
	case 9:
		out = []byte{9, byte(len(layers)), byte(h)}
	default:
		t.Fatalf("version %d", version)
	}
	for _, f := range layers {
		var names [blocksPerSub]string
		for x := range 16 {
			for z := range 16 {
				for y := range 16 {
					names[x<<8|z<<4|y] = f(x, y, z)
				}
			}
		}
		out = append(out, storage(t, names, width)...)
	}
	return out
}

// fake is a made-up world: chunks of slices written as a game writes them.
type fake struct {
	t  testing.TB
	db *leveldb.DB
}

func newFake(t testing.TB) *fake {
	t.Helper()
	db, err := leveldb.OpenFile(filepath.Join(t.TempDir(), "db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &fake{t: t, db: db}
}

func (f *fake) reader() Reader { return Reader{DB: f.db, Registry: NewRegistry()} }

// put writes one slice record of a chunk.
func (f *fake) put(pos chunks.Pos, h int8, record []byte) {
	f.t.Helper()
	key := append(chunks.RecordKey(pos, tagSubChunk), byte(h))
	if err := f.db.Put(key, record, nil); err != nil {
		f.t.Fatal(err)
	}
}

// chunk writes a whole chunk, a function of world x, y, z within it, from
// the lowest slice of the dimension to the highest that has anything.
func (f *fake) chunk(pos chunks.Pos, top int, world blockFunc) {
	f.t.Helper()
	lowest, _, _ := span(pos.Dim)
	for h := lowest; h <= top>>4; h++ {
		f.put(pos, int8(h), sliceRecord(f.t, 9, int8(h), 0, func(x, y, z int) string {
			return world(x, h*16+y, z)
		}))
	}
}

func all(n string) blockFunc { return func(int, int, int) string { return n } }
