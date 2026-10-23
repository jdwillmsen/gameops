package camera

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func cellsFrom(f func(i int) uint16) *[4096]uint16 {
	var c [4096]uint16
	for i := range c {
		c[i] = f(i)
	}
	return &c
}

func TestSubChunkRoundTripsAtEveryPaletteWidth(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 16, 17, 255, 256, 257, 1000, 4096} {
		rng := rand.New(rand.NewSource(int64(n)))
		ids := rng.Perm(maxMaterials + 1)[:n]
		cells := cellsFrom(func(i int) uint16 {
			// Every id appears once, so the palette really has n entries.
			if i < n {
				return uint16(ids[i])
			}
			return uint16(ids[rng.Intn(n)])
		})
		if n == 1 {
			cells = cellsFrom(func(int) uint16 { return uint16(ids[0]) })
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

func TestSubChunkKeepsIdsAboveTheOldByteLimit(t *testing.T) {
	cells := cellsFrom(func(i int) uint16 { return []uint16{0, 255, 256, 300, maxMaterials}[i%5] })
	_, _, _, got, err := decodeSubChunk(encodeSubChunk(0, 0, 0, cells))
	if err != nil || got != *cells {
		t.Errorf("ids above 255 changed: err %v, first cells %v", err, got[:5])
	}
}

func TestSubChunkFrameIsFarSmallerThanTheRawArray(t *testing.T) {
	// Terrain-like: mostly one block with a thin top layer.
	cells := cellsFrom(func(i int) uint16 {
		if i < 256 {
			return 2
		}
		return 1
	})
	frame := encodeSubChunk(0, 0, 0, cells)
	if len(frame) > 13+2+4+512 {
		t.Errorf("two-material sub-chunk is %d bytes, want a 1-bit index (about 530)", len(frame))
	}
	uniform := encodeSubChunk(0, 0, 0, cellsFrom(func(int) uint16 { return 4 }))
	if len(uniform) != 1+12+2+2 {
		t.Errorf("uniform sub-chunk is %d bytes, want header and one palette entry only", len(uniform))
	}
}

func TestFrameLayoutIsStable(t *testing.T) {
	frame := encodeSubChunk(1, 2, 3, cellsFrom(func(i int) uint16 { return uint16(i & 1 * 9) }))
	want := []byte{frameSubChunk, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 2, 0, 0, 0, 9, 0}
	if !bytes.HasPrefix(frame, want) {
		t.Errorf("header = % x, want prefix % x", frame[:len(want)], want)
	}
	if len(frame) != len(want)+512 || frame[len(want)] != 0b10101010 {
		t.Errorf("index bytes: len %d first %08b", len(frame), frame[len(want)])
	}
	if b := encodeBlock(-1, 64, 5, 0x0107); !bytes.Equal(b, []byte{frameBlock, 255, 255, 255, 255, 64, 0, 0, 0, 5, 0, 0, 0, 7, 1}) {
		t.Errorf("block frame = % x", b)
	}
}

func TestDecodeRejectsMalformedFrames(t *testing.T) {
	good := encodeSubChunk(0, 0, 0, cellsFrom(func(i int) uint16 { return uint16(i % 3) }))
	for name, f := range map[string][]byte{
		"empty":             nil,
		"wrong type":        append([]byte{frameBlock}, good[1:]...),
		"short header":      good[:10],
		"truncated body":    good[:len(good)-1],
		"trailing bytes":    append(append([]byte(nil), good...), 0),
		"empty palette":     append(append([]byte(nil), good[:13]...), 0, 0),
		"palette over 4096": append(append([]byte(nil), good[:13]...), 1, 16),
	} {
		if _, _, _, _, err := decodeSubChunk(f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestEntityRecordQuantisesAnglesAndKeepsPositionsExact(t *testing.T) {
	rec := entityRecord(300, 5, 1.5, -2.25, 1e6, 90, -90, 180)
	id, n := binary.Uvarint(rec)
	if id != 300 || n != 2 || len(rec) != n+2+12+3 {
		t.Fatalf("record = % x", rec)
	}
	typ := binary.LittleEndian.Uint16(rec[n:])
	x := math.Float32frombits(binary.LittleEndian.Uint32(rec[n+2:]))
	z := math.Float32frombits(binary.LittleEndian.Uint32(rec[n+10:]))
	if typ != 5 || x != 1.5 || z != 1e6 || rec[n+14] != 64 || rec[n+15] != 192 || rec[n+16] != 128 {
		t.Errorf("type %d x %v z %v yaw %d pitch %d head %d", typ, x, z, rec[n+14], rec[n+15], rec[n+16])
	}
}

func TestTickFrameCarriesSelfThenEntities(t *testing.T) {
	a, b := entityRecord(1, 0, 0, 0, 0, 0, 0, 0), entityRecord(2, 0, 1, 1, 1, 0, 0, 0)
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
	m := encodeMaterials([]material{{ID: 1, Name: "stone", Color: "#7d7d7d", Alpha: 1}, {ID: 258, Name: "torch", Color: "#3050c0", Alpha: .6, NonCube: true}})
	want := []byte{frameMaterials, 2, 0, 1, 0, 255, 0x7d, 0x7d, 0x7d, 0, 5, 's', 't', 'o', 'n', 'e', 2, 1, 153, 0x30, 0x50, 0xc0, matNonCube, 5, 't', 'o', 'r', 'c', 'h'}
	if !bytes.Equal(m, want) {
		t.Errorf("materials = % x\nwant        % x", m, want)
	}
	if e := encodeEntityTypes([]string{"player", "cow"}); !bytes.Equal(e, []byte{frameEntityTypes, 2, 0, 6, 'p', 'l', 'a', 'y', 'e', 'r', 3, 'c', 'o', 'w'}) {
		t.Errorf("types = % x", e)
	}
}

func TestAtlasFrameIsATypeByteAndTheAtlasVersion(t *testing.T) {
	if f := atlasFrame(); !bytes.Equal(f, []byte{frameAtlas, 1}) {
		t.Errorf("atlas frame = % x", f)
	}
}

func TestNamesFrameCarriesIdAndNameAndCapsTheLength(t *testing.T) {
	f := encodeNames(map[uint64]string{300: "Steve"})
	if want := []byte{frameNames, 1, 0xac, 0x02, 5, 'S', 't', 'e', 'v', 'e'}; !bytes.Equal(f, want) {
		t.Errorf("names = % x\nwant    % x", f, want)
	}
	long := encodeNames(map[uint64]string{1: strings.Repeat("a", 400)})
	if len(long) != 3+1+255 {
		t.Errorf("long name frame is %d bytes", len(long))
	}
	cut := encodeNames(map[uint64]string{1: strings.Repeat("\u00e9", 200)})
	if n := int(cut[3]); n != 254 || !utf8.Valid(cut[4:]) {
		t.Errorf("a name cut inside a character: len %d valid %v", n, utf8.Valid(cut[4:]))
	}
}
