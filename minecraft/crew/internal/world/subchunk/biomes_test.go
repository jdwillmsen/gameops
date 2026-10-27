package subchunk

import (
	"errors"
	"testing"
)

// uniform builds one network paletted storage whose every cell is value, as a
// single-entry palette (zero bits per cell).
func uniform(value int32) []byte {
	return []byte{0<<1 | 1, byte(value << 1)}
}

func TestTopBiomesTakesLastSection(t *testing.T) {
	payload := append(uniform(1), uniform(4)...)
	payload = append(payload, 0x7f<<1|1) // repeats the section below
	payload = append(payload, 0)         // border byte
	got, err := TopBiomes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 4 || got[255] != 4 {
		t.Fatalf("got %d, %d; want the last stored section's biome 4", got[0], got[255])
	}
}

func TestTopBiomesPaletted(t *testing.T) {
	// 1 bit per cell: even y=15 plane cells use palette 1.
	payload := []byte{1<<1 | 1}
	words := make([]byte, 128*4)
	for i := 0; i < 4096; i++ {
		if i&15 == 15 && (i>>4)&1 == 1 { // y=15 and z odd
			words[(i/32)*4+(i%32)/8] |= 1 << (i % 8)
		}
	}
	payload = append(payload, words...)
	payload = append(payload, 2<<1, 10<<1, 40<<1) // 2 entries: 10, 40 (zigzag)
	got, err := TopBiomes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got[0<<4|0] != 10 || got[0<<4|1] != 40 {
		t.Fatalf("z=0 %d z=1 %d, want 10 and 40", got[0], got[1])
	}
}

func TestTopBiomesRejectsGarbage(t *testing.T) {
	for _, p := range [][]byte{nil, {0}, {0x7f<<1 | 1}, {0<<1 | 1}} {
		if _, err := TopBiomes(p); !errors.Is(err, ErrMalformed) {
			t.Errorf("%v: err %v, want ErrMalformed", p, err)
		}
	}
}

func TestTopBiomesRejectsAnIndexOutsideTheKeptPalette(t *testing.T) {
	// 2 bits per cell with a palette of one entry; every word is all ones, so
	// every index is 3.
	payload := []byte{2<<1 | 1}
	for i := 0; i < 256*4; i++ {
		payload = append(payload, 0xff)
	}
	payload = append(payload, 1<<1, 5<<1)
	if _, err := TopBiomes(payload); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err %v, want ErrMalformed", err)
	}
}

func BenchmarkTopBiomesFullColumn(b *testing.B) {
	// 24 storages at 4 bits per cell, the worst a vanilla column sends.
	var payload []byte
	for s := 0; s < 24; s++ {
		payload = append(payload, 4<<1|1)
		payload = append(payload, make([]byte, 512*4)...)
		payload = append(payload, 2<<1, 1<<1, 4<<1)
	}
	payload = append(payload, 0)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := TopBiomes(payload); err != nil {
			b.Fatal(err)
		}
	}
}
