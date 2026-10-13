package heights

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

var at = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// record is a Data3D record: the heightmap, then bytes standing in for the
// biomes that follow it, which this package never reads.
func record(columns [Columns]int16) []byte {
	out := make([]byte, 0, heightmapBytes+8)
	for _, v := range columns {
		out = binary.LittleEndian.AppendUint16(out, uint16(v))
	}
	return append(out, 0, 0, 0, 0, 0, 0, 0, 0)
}

func filled(v int16) (c [Columns]int16) {
	for i := range c {
		c[i] = v
	}
	return c
}

// build makes a Map of chunks given by their heightmaps.
func build(t *testing.T, held map[chunks.Pos][Columns]int16) *Map {
	t.Helper()
	b := NewBuilder(at, len(held))
	for pos, c := range held {
		if err := b.Add(pos.Dim, pos.X, pos.Z, record(c)); err != nil {
			t.Fatal(err)
		}
	}
	return b.Map()
}

func TestFloorsAreThoseOfTheWorldsDimensions(t *testing.T) {
	for d, want := range map[chunks.Dimension]int32{chunks.Overworld: -64, chunks.Nether: 0, chunks.End: 0} {
		if got, ok := Floor(d); !ok || got != want {
			t.Errorf("floor of %v is %d, %v, want %d", d, got, ok, want)
		}
	}
	if _, ok := Floor(3); ok {
		t.Error("a fourth dimension has a floor")
	}
}

// 127 over the sea is the stored height of a surface at y 62 in a world
// whose bottom is y -64: the first air is at 63, and the surface is the
// block under it.
func TestStoredHeightIsCountedFromTheFloorToTheBlockUnderTheFirstAir(t *testing.T) {
	var c [Columns]int16
	c[0], c[1], c[16], c[17] = 127, 1, 0, 385
	for i := 18; i < Columns; i++ {
		c[i] = 100
	}
	m := build(t, map[chunks.Pos][Columns]int16{{Dim: chunks.Overworld, X: 0, Z: 0}: c})
	for _, tc := range []struct {
		x, z int32
		y    int32
		ok   bool
	}{
		{0, 0, 62, true},
		{1, 0, -64, true},
		// Nothing in the column at all: no surface, and not y -65.
		{0, 1, 0, false},
		{1, 1, 320, true},
		{2, 1, 35, true},
		{16, 0, 0, false},
	} {
		if y, ok := m.At(chunks.Overworld, tc.x, tc.z); y != tc.y || ok != tc.ok {
			t.Errorf("at %d, %d: %d, %v; want %d, %v", tc.x, tc.z, y, ok, tc.y, tc.ok)
		}
	}
}

func TestTheNetherAndTheEndCountFromZero(t *testing.T) {
	m := build(t, map[chunks.Pos][Columns]int16{
		{Dim: chunks.Nether, X: -1, Z: -1}: filled(128),
		{Dim: chunks.End, X: 0, Z: 0}:      filled(71),
	})
	if y, ok := m.At(chunks.Nether, -1, -1); !ok || y != 127 {
		t.Errorf("nether roof: %d, %v", y, ok)
	}
	if y, ok := m.At(chunks.End, 15, 15); !ok || y != 70 {
		t.Errorf("end island: %d, %v", y, ok)
	}
	if _, ok := m.At(chunks.Overworld, 15, 15); ok {
		t.Error("an overworld column from a chunk that is in the end")
	}
}

func TestAColumnWithoutAChunkIsNotKnown(t *testing.T) {
	m := build(t, map[chunks.Pos][Columns]int16{{Dim: chunks.Overworld, X: 2, Z: 3}: filled(90)})
	for _, tc := range [][2]int32{{0, 0}, {31, 48}, {48, 48}, {32, 47}, {-1, -1}} {
		if _, ok := m.At(chunks.Overworld, tc[0], tc[1]); ok {
			t.Errorf("%v is known", tc)
		}
	}
	if _, ok := m.At(chunks.Overworld, 32, 48); !ok {
		t.Error("the chunk's own corner is not known")
	}
	var none *Map
	if _, ok := none.At(chunks.Overworld, 0, 0); ok || none.Chunks(chunks.Overworld) != 0 || none.Bytes() != 0 {
		t.Error("no map knows something")
	}
	if _, ok := m.At(7, 0, 0); ok {
		t.Error("a dimension that is not one")
	}
}

// Every width of storage reads back exactly, including a chunk that spans
// more than a byte can say and one that is as far apart as heights go.
func TestEveryTierKeepsEveryColumnExactly(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, span := range []int{0, 1, 2, 3, 4, 15, 16, 255, 256, 300, 383, 1000, 65535} {
		for _, base := range []int{0, 1, 63, 400, -32768} {
			if base+span > math.MaxInt16 {
				continue
			}
			var c [Columns]int16
			for i := range c {
				c[i] = int16(base + rng.IntN(span+1))
			}
			c[rng.IntN(Columns)], c[rng.IntN(Columns)] = int16(base), int16(base+span)
			b := NewBuilder(at, 1)
			if err := b.AddColumns(chunks.Overworld, -3, 5, &c); err != nil {
				t.Fatal(err)
			}
			m := b.Map()
			var got [Columns]int16
			l := m.layers[chunks.Overworld]
			l.expand(l.cells[cellKey(-3, 5)], &got)
			for i := range c {
				if got[i] != c[i] || l.raw(l.cells[cellKey(-3, 5)], i) != c[i] {
					t.Fatalf("span %d base %d column %d: %d, %d, want %d", span, base, i, got[i], l.raw(l.cells[cellKey(-3, 5)], i), c[i])
				}
			}
		}
	}
}

func TestAChunkSpanningMoreThanAByteIsKeptWhole(t *testing.T) {
	var c [Columns]int16
	for i := range c {
		c[i] = int16(i + 1)
	}
	// 1 through 257 but for one gap: one more than a byte can say from the
	// lowest, though no height is large.
	c[Columns-1]++
	m := build(t, map[chunks.Pos][Columns]int16{{Dim: chunks.Overworld, X: 0, Z: 0}: c})
	for i := range c {
		y, ok := m.At(chunks.Overworld, int32(i&15), int32(i>>4))
		if want := int32(c[i]) - 64 - 1; !ok || y != want {
			t.Fatalf("column %d: %d, %v, want %d", i, y, ok, want)
		}
	}
	if tier := m.layers[0].cells[0] >> tierShift; int(tier) != escapeTier {
		t.Errorf("kept in tier %d", tier)
	}
}

func TestFlatAndNearFlatChunksCostNextToNothing(t *testing.T) {
	flat := filled(127)
	near := filled(127)
	near[200] = 128
	m := build(t, map[chunks.Pos][Columns]int16{
		{Dim: chunks.Overworld, X: 0, Z: 0}: flat,
		{Dim: chunks.Overworld, X: 1, Z: 0}: near,
	})
	if got, want := len(m.layers[0].data), recordSize(1); got != want {
		t.Errorf("data is %d bytes, want %d", got, want)
	}
	if want := 2 + 32; recordSize(1) != want {
		t.Errorf("a one-bit chunk is %d bytes", recordSize(1))
	}
}

func TestRecordsWithoutAHeightmapAreRefused(t *testing.T) {
	b := NewBuilder(at, 0)
	if err := b.Add(chunks.Overworld, 0, 0, make([]byte, heightmapBytes-1)); err == nil {
		t.Error("a short record is taken")
	}
	if err := b.Add(5, 0, 0, record(filled(1))); err == nil {
		t.Error("a fifth dimension is taken")
	}
	if err := b.Add(chunks.Overworld, 0, 0, record(filled(1))); err != nil {
		t.Error(err)
	}
	// The first of two records for one chunk stands.
	if err := b.Add(chunks.Overworld, 0, 0, record(filled(9))); err != nil {
		t.Error(err)
	}
	if y, _ := b.Map().At(chunks.Overworld, 0, 0); y != -64 {
		t.Errorf("replaced by the second: %d", y)
	}
}

func TestChunksPastTheEdgeOfAnyWorldAndPastTheLimitAreNotHeld(t *testing.T) {
	b := NewBuilder(at, 0)
	_ = b.AddColumns(chunks.Overworld, maxChunkCoordinate+1, 0, &[Columns]int16{})
	b.room = 1
	c := filled(100)
	_ = b.AddColumns(chunks.Overworld, 0, 0, &c)
	_ = b.AddColumns(chunks.Overworld, 1, 0, &c)
	m := b.Map()
	if _, ok := m.At(chunks.Overworld, 0, 0); !ok || m.Chunks(chunks.Overworld) != 1 {
		t.Errorf("held %d chunks, the first of them %v", m.Chunks(chunks.Overworld), ok)
	}
}

// ragged is a world of chunks of every kind around the origin.
func ragged(t *testing.T) *Map {
	t.Helper()
	rng := rand.New(rand.NewPCG(7, 8))
	held := map[chunks.Pos][Columns]int16{}
	for cz := int32(-6); cz < 6; cz++ {
		for cx := int32(-6); cx < 6; cx++ {
			if (cx+cz)%5 == 0 {
				continue
			}
			var c [Columns]int16
			span := []int{0, 1, 3, 12, 200, 300}[rng.IntN(6)]
			for i := range c {
				c[i] = int16(1 + rng.IntN(span+1))
			}
			// A column with nothing in it, here and there.
			c[rng.IntN(Columns)] = 0
			held[chunks.Pos{Dim: chunks.Overworld, X: cx, Z: cz}] = c
		}
	}
	return build(t, held)
}

func TestARectangleIsWhatAtSaysOfEachColumn(t *testing.T) {
	m := ragged(t)
	for _, tc := range []struct {
		x, z int32
		w, h int
	}{{-100, -100, 200, 200}, {-5, -7, 1, 1}, {13, 14, 5, 40}, {-17, -33, 34, 2}, {90, 90, 8, 8}, {-3, 0, 3, 1}} {
		out := make([]int16, tc.w*tc.h)
		found := m.Rect(chunks.Overworld, tc.x, tc.z, tc.w, tc.h, out)
		any := false
		for j := range tc.h {
			for i := range tc.w {
				y, ok := m.At(chunks.Overworld, tc.x+int32(i), tc.z+int32(j))
				want := Unknown
				if ok {
					want, any = int16(y), true
				}
				if got := out[j*tc.w+i]; got != want {
					t.Fatalf("%+v: column %d, %d is %d, want %d", tc, i, j, got, want)
				}
			}
		}
		if found != any {
			t.Errorf("%+v: found %v, want %v", tc, found, any)
		}
	}
}

func TestARectangleAskedForBadlyIsUnknown(t *testing.T) {
	m := ragged(t)
	out := []int16{1, 2, 3, 4}
	for name, ok := range map[string]bool{
		"empty":     m.Rect(chunks.Overworld, 0, 0, 0, 2, out),
		"negative":  m.Rect(chunks.Overworld, 0, 0, 2, -1, out),
		"too small": m.Rect(chunks.Overworld, 0, 0, 3, 3, out),
		"huge":      m.Rect(chunks.Overworld, 0, 0, 1<<20, 1<<20, out),
		"far":       m.Rect(chunks.Overworld, math.MaxInt32-1, 0, 2, 2, out),
		"nowhere":   m.Rect(chunks.Nether, 0, 0, 2, 2, out),
	} {
		if ok {
			t.Errorf("%s: something found", name)
		}
	}
	// A rectangle that fits is filled even where nothing is known.
	m.Rect(chunks.Nether, 0, 0, 2, 2, out)
	for _, v := range out {
		if v != Unknown {
			t.Errorf("%v is not all unknown", out)
			break
		}
	}
}

func TestATileIsTheColumnUnderEachPixelsMiddleAtEveryZoom(t *testing.T) {
	m := ragged(t)
	var out [TileSize * TileSize]int16
	for _, tc := range []struct{ zoom, tx, ty int }{{0, 0, 0}, {0, -1, -1}, {-1, -1, 0}, {-3, 0, -1}, {1, -1, 0}, {2, 0, 0}, {-4, 0, 0}, {0, 40, 40}} {
		found := m.Tile(chunks.Overworld, tc.zoom, tc.tx, tc.ty, &out)
		blocks := math.Ldexp(1, -tc.zoom)
		any := false
		for py := range TileSize {
			for px := range TileSize {
				x := int32(math.Floor((float64(tc.tx)*TileSize + float64(px) + 0.5) * blocks))
				z := int32(math.Floor((float64(tc.ty)*TileSize + float64(py) + 0.5) * blocks))
				want := Unknown
				if y, ok := m.At(chunks.Overworld, x, z); ok {
					want, any = int16(y), true
				}
				if got := out[py*TileSize+px]; got != want {
					t.Fatalf("%+v: pixel %d, %d is %d, want %d", tc, px, py, got, want)
				}
			}
		}
		if found != any {
			t.Errorf("%+v: found %v, want %v", tc, found, any)
		}
	}
	if m.Tile(chunks.Overworld, 0, 100, 100, &out) {
		t.Error("a tile with no chunk in it has heights")
	}
	var none *Map
	if none.Tile(chunks.Overworld, 0, 0, 0, &out) || none.Rect(chunks.Overworld, 0, 0, 1, 1, out[:1]) {
		t.Error("no map has heights")
	}
}

// The addresses are the terrain's: at zoom 0 a pixel is a block and a tile
// 256 of them from its corner, a zoom below is two blocks to a pixel and a
// zoom above two pixels to a block. Each block here is worked out from
// that by hand, in two chunks whose every column has a height of its own.
func TestATileIsAddressedAsTheTerrainsAre(t *testing.T) {
	var south, north [Columns]int16
	for i := range south {
		// The surface is at y z*16+x-64 in the chunk at the origin, and
		// at 235+z*16+x in the one across both axes from it.
		south[i], north[i] = int16(1+i), int16(300+i)
	}
	m := build(t, map[chunks.Pos][Columns]int16{
		{Dim: chunks.Overworld, X: 0, Z: 0}:   south,
		{Dim: chunks.Overworld, X: -1, Z: -1}: north,
	})
	var out [TileSize * TileSize]int16
	for _, tc := range []struct {
		zoom, tx, ty, px, py int
		want                 int16
	}{
		// Block 3, 2.
		{0, 0, 0, 3, 2, -29},
		// Block -1, -1: the last pixel of the tile before the origin.
		{0, -1, -1, 255, 255, 490},
		// Block 16, 0, in a chunk that is not held.
		{0, 0, 0, 16, 0, Unknown},
		// Two blocks to a pixel, the one under its middle: 1, 1 and 5, 3.
		{-1, 0, 0, 0, 0, -47},
		{-1, 0, 0, 2, 1, -11},
		// Blocks -1, -1 and -3, -1.
		{-1, -1, -1, 255, 255, 490},
		{-1, -1, -1, 254, 255, 488},
		// Two pixels to a block: both of block 0, 0, then 1, 0 and 1, 1.
		{1, 0, 0, 0, 0, -64},
		{1, 0, 0, 1, 1, -64},
		{1, 0, 0, 2, 0, -63},
		{1, 0, 0, 3, 2, -47},
		// Blocks -1, -1 and -2, -1.
		{1, -1, -1, 255, 255, 490},
		{1, -1, -1, 253, 255, 489},
	} {
		m.Tile(chunks.Overworld, tc.zoom, tc.tx, tc.ty, &out)
		if got := out[tc.py*TileSize+tc.px]; got != tc.want {
			t.Errorf("zoom %d tile %d, %d pixel %d, %d is %d, want %d", tc.zoom, tc.tx, tc.ty, tc.px, tc.py, got, tc.want)
		}
	}
}

func TestSavedHeightsComeBackTheSame(t *testing.T) {
	m := ragged(t)
	m.SnapshotAt, m.CensusChunks = at, 12345
	path := filepath.Join(t.TempDir(), "sub", "heights.bin")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("the temporary file is left")
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.SnapshotAt.Equal(at) || back.CensusChunks != 12345 {
		t.Errorf("came back as %v, %d", back.SnapshotAt, back.CensusChunks)
	}
	for _, d := range chunks.Dimensions {
		if back.Chunks(d) != m.Chunks(d) {
			t.Errorf("%v: %d chunks, want %d", d, back.Chunks(d), m.Chunks(d))
		}
	}
	a, b := make([]int16, 200*200), make([]int16, 200*200)
	m.Rect(chunks.Overworld, -100, -100, 200, 200, a)
	back.Rect(chunks.Overworld, -100, -100, 200, 200, b)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("column %d came back as %d, was %d", i, b[i], a[i])
		}
	}
	// The same heights are the same bytes, whatever order the table was
	// walked in.
	if string(m.Encode()) != string(back.Encode()) {
		t.Error("the saved form is not stable")
	}
}

// reseal gives a damaged body the checksum a whole file would have, so
// that it is the damage and not the sum that is found.
func reseal(body []byte) []byte {
	return binary.LittleEndian.AppendUint32(body[:len(body):len(body)], crc32.ChecksumIEEE(body))
}

func TestHeightsFromAnotherVersionOrDamagedAreNotRead(t *testing.T) {
	m := ragged(t)
	whole := m.Encode()
	body := whole[:len(whole)-4]
	flip := func(i int) []byte {
		out := append([]byte(nil), body...)
		out[i] ^= 0xff
		return out
	}
	older := append([]byte(nil), body...)
	older[len(fileMagic)-1] = '0'
	newer := append([]byte(nil), body...)
	newer[len(fileMagic)-1] = '2'

	// The first chunk of the overworld, whose reference is made to point past the data.
	first := len(fileMagic) + 16 + 4
	badRef := append([]byte(nil), body...)
	binary.LittleEndian.PutUint32(badRef[first+8:], uint32(escapeTier)<<tierShift|payloadMask)
	badTier := append([]byte(nil), body...)
	binary.LittleEndian.PutUint32(badTier[first+8:], 15<<tierShift)
	badKey := append([]byte(nil), body...)
	binary.LittleEndian.PutUint64(badKey[first:], cellKey(maxChunkCoordinate+1, 0))

	for name, raw := range map[string][]byte{
		"an older version":      reseal(older),
		"a newer version":       reseal(newer),
		"another file":          reseal(append([]byte("NOTHEIGH"), body[len(fileMagic):]...)),
		"a flipped byte":        append(flip(len(body)/2), whole[len(whole)-4:]...),
		"a wrong sum":           append(append([]byte(nil), body...), 0, 0, 0, 0),
		"truncated":             reseal(body[:len(body)-7]),
		"trailing bytes":        reseal(append(append([]byte(nil), body...), 1)),
		"empty":                 nil,
		"a reference past data": reseal(badRef),
		"a tier that is none":   reseal(badTier),
		"a chunk past the edge": reseal(badKey),
	} {
		if m, err := Decode(raw); err == nil || m != nil {
			t.Errorf("%s: read", name)
		}
	}
	if _, err := Decode(whole); err != nil {
		t.Fatal(err)
	}
}

func TestAFileThatIsAbsentOrTooLargeIsNotRead(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "none.bin")); !os.IsNotExist(err) {
		t.Errorf("an absent file: %v", err)
	}
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxFile + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := Load(big); err == nil {
		t.Error("a file past every bound is read")
	}
}
