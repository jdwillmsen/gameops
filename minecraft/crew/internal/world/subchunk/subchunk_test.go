package subchunk_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
)

func assertLayer(t *testing.T, got subchunk.Layer, want blocks) {
	t.Helper()
	for x := 0; x < 16; x++ {
		for y := 0; y < 16; y++ {
			for z := 0; z < 16; z++ {
				if g := got.Block(x, y, z); g != want[idx(x, y, z)] {
					t.Fatalf("block (%d,%d,%d) = %#x, want %#x", x, y, z, g, want[idx(x, y, z)])
				}
			}
		}
	}
}

func TestDecodeRoundTripAllStorageWidths(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	// Distinct-value counts that land on each bits-per-block width:
	// 0, 1, 2, 3, 4, 5, 6, 8 and 16.
	for _, n := range []int{2, 3, 4, 5, 8, 9, 16, 17, 32, 33, 64, 65, 256, 257, 1000} {
		want := scattered(r, n)
		payload := newEncoded(t, -3, want)

		sc, err := subchunk.Decode(payload)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if sc.Version != 9 || sc.Y != -3 {
			t.Fatalf("n=%d: version %d y %d", n, sc.Version, sc.Y)
		}
		if len(sc.Layers) != 1 {
			t.Fatalf("n=%d: %d layers", n, len(sc.Layers))
		}
		if len(sc.Trailer) != 0 {
			t.Fatalf("n=%d: unexpected trailer %x", n, sc.Trailer)
		}
		assertLayer(t, sc.Layers[0], want)
	}
}

func TestDecodeSingleValuePalette(t *testing.T) {
	// Zero bits per block: no index words and no palette count, one entry.
	p := append([]byte{9, 1, 2, 0x01}, varint32(-1)...)
	sc, err := subchunk.Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	assertLayer(t, sc.Layers[0], filled(0xFFFFFFFF))

	// The encoder keeps the air entry after overwriting every block, so the
	// palette has two entries and one is unused.
	want := filled(0xDEADBEEF)
	sc, err = subchunk.Decode(newEncoded(t, 5, want))
	if err != nil {
		t.Fatal(err)
	}
	assertLayer(t, sc.Layers[0], want)
}

func TestDecodeMultipleLayers(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	l0, l1 := scattered(r, 20), filled(airHash)
	l1[idx(1, 2, 3)] = 0xCAFE0001
	sc, err := subchunk.Decode(newEncoded(t, 0, l0, l1))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Layers) != 2 {
		t.Fatalf("%d layers", len(sc.Layers))
	}
	assertLayer(t, sc.Layers[0], l0)
	assertLayer(t, sc.Layers[1], l1)
}

func TestDecodeEmptyStorages(t *testing.T) {
	// The encoder writes an all-air sub-chunk as zero storages.
	sc, err := subchunk.Decode(newEncoded(t, 4, filled(airHash)))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Layers) != 0 || sc.Y != 4 {
		t.Fatalf("%+v", sc)
	}
}

func TestDecodeVersion8HasNoY(t *testing.T) {
	// One storage with a single-entry palette (0 bits per block).
	p := append([]byte{8, 1, 0x01}, varint32(int32(-5))...)
	sc, err := subchunk.Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Version != 8 || sc.Y != 0 {
		t.Fatalf("%+v", sc)
	}
	if got := sc.Layers[0].Block(15, 15, 15); got != uint32(0xFFFFFFFB) {
		t.Fatalf("block %#x", got)
	}
}

// Sub-chunk packets append data after the block storages. The decoder must
// stop at the end of the storages and hand the rest back untouched, rather
// than read it as more storages.
func TestDecodeKeepsTrailer(t *testing.T) {
	want := filled(0x1234)
	extra := []byte{0x0a, 0x00, 0x00, 0x05, 0x05, 'a', 'b', 0x00}
	payload := append(newEncoded(t, 1, want), extra...)

	sc, err := subchunk.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	assertLayer(t, sc.Layers[0], want)
	if !bytes.Equal(sc.Trailer, extra) {
		t.Fatalf("trailer %x, want %x", sc.Trailer, extra)
	}
}

func TestDecodeMalformed(t *testing.T) {
	good := newEncoded(t, 0, scattered(rand.New(rand.NewPCG(5, 6)), 40))

	cases := map[string][]byte{
		"empty":                  nil,
		"version only":           {9},
		"unknown version":        {7, 1, 0},
		"version 1 unsupported":  {1, 0},
		"truncated header":       {9, 1},
		"truncated indices":      good[:10],
		"truncated palette":      good[:len(good)-2],
		"persistent palette":     {9, 1, 0, 0x00},
		"same-as-previous first": {9, 1, 0, 0x7f<<1 | 1},
		"bad bits per block":     {9, 1, 0, 7<<1 | 1},
		"zero palette count":     append(append([]byte{9, 1, 0, 1<<1 | 1}, make([]byte, 128*4)...), varint32(0)...),
		"huge palette count":     append(append([]byte{9, 1, 0, 1<<1 | 1}, make([]byte, 128*4)...), varint32(1<<30)...),
		"unterminated varint":    append(append([]byte{9, 1, 0, 1<<1 | 1}, make([]byte, 128*4)...), 0x80, 0x80),
	}
	// An index pointing past the palette: 2 bits per block, palette of one.
	idxWords := make([]byte, 256*4)
	idxWords[0] = 0x03
	cases["index past palette"] = append(append([]byte{9, 1, 0, 2<<1 | 1}, idxWords...), append(varint32(1), varint32(7)...)...)

	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := subchunk.Decode(p)
			if !errors.Is(err, subchunk.ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	r := rand.New(rand.NewPCG(7, 8))
	for _, n := range []int{1, 2, 5, 40, 300} {
		f.Add(newEncoded(f, 2, scattered(r, n)))
	}
	f.Add(newEncoded(f, 0, scattered(r, 9), filled(airHash)))
	f.Add([]byte{9, 0, 0})
	f.Fuzz(func(t *testing.T, p []byte) {
		sc, err := subchunk.Decode(p)
		if err != nil {
			if !errors.Is(err, subchunk.ErrMalformed) {
				t.Fatalf("error not ErrMalformed: %v", err)
			}
			return
		}
		for _, l := range sc.Layers {
			_ = l.Block(15, 15, 15)
			_ = l.Block(0, 0, 0)
		}
	})
}
