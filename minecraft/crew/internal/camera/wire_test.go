package camera

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

func cellsFrom(f func(i int) uint8) *[4096]uint8 {
	var c [4096]uint8
	for i := range c {
		c[i] = f(i)
	}
	return &c
}

func TestSubChunkRoundTripsAtEveryPaletteWidth(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 16, 17, 255, 256} {
		rng := rand.New(rand.NewSource(int64(n)))
		ids := rng.Perm(256)[:n]
		cells := cellsFrom(func(int) uint8 { return uint8(ids[rng.Intn(n)]) })
		if n == 1 {
			cells = cellsFrom(func(int) uint8 { return uint8(ids[0]) })
		}
		frame := encodeSubChunk(-3, 7, 1<<20, cells)
		x, y, z, got, err := decodeSubChunk(frame)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if x != -3 || y != 7 || z != 1<<20 || got != *cells {
			t.Errorf("n=%d: round trip changed the sub-chunk", n)
		}
	}
}

func TestSubChunkFrameIsFarSmallerThanTheRawArray(t *testing.T) {
	// Terrain-like: mostly one block with a thin top layer.
	cells := cellsFrom(func(i int) uint8 {
		if i < 256 {
			return 2
		}
		return 1
	})
	frame := encodeSubChunk(0, 0, 0, cells)
	if len(frame) > 13+2+2+512 {
		t.Errorf("two-material sub-chunk is %d bytes, want a 1-bit index (about 530)", len(frame))
	}
	uniform := encodeSubChunk(0, 0, 0, cellsFrom(func(int) uint8 { return 4 }))
	if len(uniform) != 1+12+2+1 {
		t.Errorf("uniform sub-chunk is %d bytes, want header and one palette entry only", len(uniform))
	}
}

func TestFrameLayoutIsStable(t *testing.T) {
	frame := encodeSubChunk(1, 2, 3, cellsFrom(func(i int) uint8 { return uint8(i & 1 * 9) }))
	want := []byte{frameSubChunk, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 2, 0, 0, 9}
	if !bytes.HasPrefix(frame, want) {
		t.Errorf("header = % x, want prefix % x", frame[:len(want)], want)
	}
	if len(frame) != len(want)+512 || frame[len(want)] != 0b10101010 {
		t.Errorf("index bytes: len %d first %08b", len(frame), frame[len(want)])
	}
	if b := encodeBlock(-1, 64, 5, 7); !bytes.Equal(b, []byte{frameBlock, 255, 255, 255, 255, 64, 0, 0, 0, 5, 0, 0, 0, 7}) {
		t.Errorf("block frame = % x", b)
	}
}

func TestDecodeRejectsMalformedFrames(t *testing.T) {
	good := encodeSubChunk(0, 0, 0, cellsFrom(func(i int) uint8 { return uint8(i % 3) }))
	for name, f := range map[string][]byte{
		"empty":            nil,
		"wrong type":       append([]byte{frameBlock}, good[1:]...),
		"short header":     good[:10],
		"truncated body":   good[:len(good)-1],
		"trailing bytes":   append(append([]byte(nil), good...), 0),
		"empty palette":    append(append([]byte(nil), good[:13]...), 0, 0),
		"palette over 256": append(append([]byte(nil), good[:13]...), 1, 1),
	} {
		if _, _, _, _, err := decodeSubChunk(f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestEntityRecordQuantisesAnglesAndKeepsPositionsExact(t *testing.T) {
	rec := entityRecord(300, 5, 1.5, -2.25, 1e6, 90, -90)
	id, n := binary.Uvarint(rec)
	if id != 300 || n != 2 || len(rec) != n+2+12+2 {
		t.Fatalf("record = % x", rec)
	}
	typ := binary.LittleEndian.Uint16(rec[n:])
	x := math.Float32frombits(binary.LittleEndian.Uint32(rec[n+2:]))
	z := math.Float32frombits(binary.LittleEndian.Uint32(rec[n+10:]))
	if typ != 5 || x != 1.5 || z != 1e6 || rec[n+14] != 64 || rec[n+15] != 192 {
		t.Errorf("type %d x %v z %v yaw %d pitch %d", typ, x, z, rec[n+14], rec[n+15])
	}
}

func TestTickFrameCarriesSelfThenEntities(t *testing.T) {
	a, b := entityRecord(1, 0, 0, 0, 0, 0, 0), entityRecord(2, 0, 1, 1, 1, 0, 0)
	if f := encodeTick(nil, [][]byte{a, b}); len(f) != 3+len(a)+len(b) || f[0] != frameTick || f[1] != 0 || f[2] != 2 {
		t.Errorf("without self: % x", f)
	}
	self := &selfPose{X: 1, Y: 2, Z: 3, Yaw: 4, Pitch: 5}
	f := encodeTick(self, nil)
	if len(f) != 3+20 || f[1] != 1 || math.Float32frombits(binary.LittleEndian.Uint32(f[6:])) != 2 || f[22] != 0 {
		t.Errorf("with self: % x", f)
	}
}

func TestMaterialsAndEntityTypesFrames(t *testing.T) {
	m := encodeMaterials([]material{{ID: 1, Name: "stone", Color: "#7d7d7d", Alpha: 1}, {ID: 2, Name: "water", Color: "#3050c0", Alpha: .6}})
	want := []byte{frameMaterials, 2, 0, 1, 255, 0x7d, 0x7d, 0x7d, 5, 's', 't', 'o', 'n', 'e', 2, 153, 0x30, 0x50, 0xc0, 5, 'w', 'a', 't', 'e', 'r'}
	if !bytes.Equal(m, want) {
		t.Errorf("materials = % x\nwant        % x", m, want)
	}
	if e := encodeEntityTypes([]string{"player", "cow"}); !bytes.Equal(e, []byte{frameEntityTypes, 2, 0, 6, 'p', 'l', 'a', 'y', 'e', 'r', 3, 'c', 'o', 'w'}) {
		t.Errorf("types = % x", e)
	}
}
