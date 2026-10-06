package biomes

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The helpers below build records the way the game lays them out, so the
// tests say what a record holds rather than which bytes it is.

func uniform(id uint32) []byte {
	return binary.LittleEndian.AppendUint32([]byte{1}, id)
}

func sameBelow() []byte { return []byte{0xff} }

// packed is a storage of bits per entry whose entry at x, y, z is
// palette[pick(x, y, z)].
func packed(bits uint, palette []uint32, pick func(x, y, z int) int) []byte {
	perWord := 32 / bits
	words := make([]uint32, (storageEntries+int(perWord)-1)/int(perWord))
	for x := range 16 {
		for z := range 16 {
			for y := range 16 {
				entry := uint(x<<8 | z<<4 | y)
				words[entry/perWord] |= uint32(pick(x, y, z)) << (entry % perWord * bits)
			}
		}
	}
	out := []byte{byte(bits<<1 | 1)}
	for _, w := range words {
		out = binary.LittleEndian.AppendUint32(out, w)
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(palette)))
	for _, id := range palette {
		out = binary.LittleEndian.AppendUint32(out, id)
	}
	return out
}

// record is a Data3D value: height gives each column's first air, counted
// from the bottom of the dimension.
func record(height func(x, z int) int, storages ...[]byte) []byte {
	out := make([]byte, heightmapBytes)
	for z := range 16 {
		for x := range 16 {
			binary.LittleEndian.PutUint16(out[(z*16+x)*2:], uint16(int16(height(x, z))))
		}
	}
	for _, s := range storages {
		out = append(out, s...)
	}
	return out
}

func flat(h int) func(int, int) int { return func(int, int) int { return h } }

func surfaceOf(t *testing.T, value []byte) [Columns]uint32 {
	t.Helper()
	var out [Columns]uint32
	if err := surface(value, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSurfaceIsTheBiomeOfEachColumnsHighestBlock(t *testing.T) {
	const cave, plains, river = 187, 1, 7
	// The river runs through one block of the upper storage: x 3, z 5, y 19.
	upper := packed(1, []uint32{plains, river}, func(x, y, z int) int {
		if x == 3 && z == 5 && y == 3 {
			return 1
		}
		return 0
	})
	// Every column stands 20 high but one, which stops in the cave below.
	height := func(x, z int) int {
		if x == 9 && z == 2 {
			return 10
		}
		return 20
	}
	got := surfaceOf(t, record(height, uniform(cave), upper))
	for z := range 16 {
		for x := range 16 {
			want := uint32(plains)
			switch {
			case x == 3 && z == 5:
				want = river
			case x == 9 && z == 2:
				want = cave
			}
			if got[z*16+x] != want {
				t.Errorf("column x %d z %d = %d, want %d", x, z, got[z*16+x], want)
			}
		}
	}
}

func TestSurfaceReadsEveryWidthOfIndex(t *testing.T) {
	for _, bits := range []uint{1, 2, 3, 4, 5, 6, 8, 16} {
		palette := make([]uint32, min(1<<bits, 200))
		for i := range palette {
			palette[i] = uint32(1000 + i)
		}
		// Every block of a column the same, and every column its own.
		s := packed(bits, palette, func(x, _, z int) int { return (z*16 + x) % len(palette) })
		got := surfaceOf(t, record(flat(8), s))
		for i, id := range got {
			if want := palette[i%len(palette)]; id != want {
				t.Fatalf("%d bits: column %d = %d, want %d", bits, i, id, want)
			}
		}
	}
}

func TestSurfaceFollowsAStorageThatRepeatsTheOneBelow(t *testing.T) {
	got := surfaceOf(t, record(flat(40), uniform(24), sameBelow(), sameBelow()))
	if got[0] != 24 || got[255] != 24 {
		t.Errorf("got %d and %d, want 24", got[0], got[255])
	}
}

// A column of nothing but air, and a height past the top of what the
// record holds, still have a biome: the nearest storage's.
func TestSurfaceKeepsToTheStoragesThereAre(t *testing.T) {
	got := surfaceOf(t, record(func(x, _ int) int { return []int{0, -5, 9999}[x%3] }, uniform(9), uniform(10)))
	if got[0] != 9 || got[1] != 9 || got[2] != 10 {
		t.Errorf("got %v, want 9 9 10", got[:3])
	}
}

func TestSurfaceRefusesARecordItCannotRead(t *testing.T) {
	short := packed(2, []uint32{1, 2}, func(x, _, _ int) int { return x % 4 })
	var many [][]byte
	for range maxStorages + 1 {
		many = append(many, uniform(1))
	}
	for name, value := range map[string][]byte{
		"nothing":                        nil,
		"a heightmap and no biomes":      record(flat(64)),
		"a first storage that repeats":   record(flat(64), sameBelow()),
		"a width no storage has":         record(flat(64), packed(7, []uint32{1}, func(int, int, int) int { return 0 })),
		"a palette of more than a chunk": record(flat(64), packed(16, make([]uint32, storageEntries+1), func(int, int, int) int { return 0 })),
		"an empty palette":               record(flat(64), packed(1, nil, func(int, int, int) int { return 0 })),
		"a palette longer than a chunk":  record(flat(64), append(packed(1, nil, func(int, int, int) int { return 0 })[:1+512], 0x01, 0x10, 0, 0)),
		"an index past its palette":      record(flat(8), short),
		"a storage cut short":            record(flat(64), uniform(1)[:3]),
		"words cut short":                record(flat(64), packed(4, []uint32{1}, func(int, int, int) int { return 0 })[:100]),
		"too many storages":              record(flat(64), many...),
	} {
		var out [Columns]uint32
		if err := surface(value, &out); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	v, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Records cut from the FWB world as it was on 2026-10-05, each from a
// chunk whose biome is not in doubt.
func TestSurfaceOfRealRecords(t *testing.T) {
	// An ocean monument, recorded at x 27 to 84, z 5243 to 5300, stands
	// in deep ocean: chunk 3, 329.
	for i, id := range surfaceOf(t, fixture(t, "overworld_3_329.data3d")) {
		if id != 24 {
			t.Fatalf("monument chunk, column %d = %s, want deep_ocean", i, Lookup(id).Name)
		}
	}
	// A witch hut, recorded at x -1216 to -1210, z 1712 to 1720, stands
	// in a swamp: chunk -76, 107, whose corner is the hut's. The swamp's
	// edge runs under the hut, block by block, so it is the middle of the
	// hut and most of the chunk that are swamp, not every column.
	hut := surfaceOf(t, fixture(t, "overworld_-76_107.data3d"))
	if id := hut[4*16+3]; id != 6 {
		t.Errorf("witch hut, x -1213 z 1716 = %s, want swampland", Lookup(id).Name)
	}
	swamp := 0
	for _, id := range hut {
		if id == 6 {
			swamp++
		}
	}
	if swamp <= Columns/2 {
		t.Errorf("%d of the witch hut chunk's columns are swampland, want most", swamp)
	}
	// A fortress, at x 74 to 231, z -450 to -286: chunk 5, -28.
	for i, id := range surfaceOf(t, fixture(t, "nether_5_-28.data3d")) {
		if id != 8 && (id < 178 || id > 181) {
			t.Fatalf("nether chunk, column %d = %s, which is no nether biome", i, Lookup(id).Name)
		}
	}
	for i, id := range surfaceOf(t, fixture(t, "end_0_0.data3d")) {
		if id != 9 {
			t.Fatalf("end chunk, column %d = %s, want the_end", i, Lookup(id).Name)
		}
	}
}

// Whatever a record is cut down to, reading it ends in an answer.
func TestSurfaceOfATruncatedRecordNeverPanics(t *testing.T) {
	for _, name := range []string{"overworld_-76_107.data3d", "nether_5_-28.data3d"} {
		whole := fixture(t, name)
		for n := range whole {
			var out [Columns]uint32
			_ = surface(whole[:n], &out)
		}
	}
}
