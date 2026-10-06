package biomes

import (
	"bytes"
	"context"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	ocean, plains, desert, forest, river = 0, 1, 2, 4, 7
	hell, theEnd                         = 8, 9
)

// columnsOf is a chunk's surface as a record: every column 70 high in a
// single storage that says which biome each column is.
func columnsOf(biome func(x, z int) uint32) []byte {
	var palette []uint32
	index := map[uint32]int{}
	for z := range 16 {
		for x := range 16 {
			id := biome(x, z)
			if _, ok := index[id]; !ok {
				index[id] = len(palette)
				palette = append(palette, id)
			}
		}
	}
	if len(palette) == 1 {
		return record(flat(6), uniform(palette[0]))
	}
	return record(flat(6), packed(8, palette, func(x, _, z int) int { return index[biome(x, z)] }))
}

func all(id uint32) func(int, int) uint32 { return func(int, int) uint32 { return id } }

// westOf splits a chunk north to south: a to the west of column x, b from
// it eastwards.
func westOf(split int, a, b uint32) func(int, int) uint32 {
	return func(x, _ int) uint32 {
		if x < split {
			return a
		}
		return b
	}
}

type testWorld map[chunks.Pos][]byte

// write builds a LevelDB holding the chunks' biome records, closed and
// without a LOCK file, the way a mirrored world sits on disk.
func (w testWorld) write(t testing.TB, extra map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for p, v := range w {
		if err := db.Put(chunks.RecordKey(p, TagData3D), v, nil); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range extra {
		if err := db.Put([]byte(k), v, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "LOCK")); err != nil {
		t.Fatal(err)
	}
	return dir
}

var snapshotAt = time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)

func scanOf(t testing.TB, w testWorld, extra map[string][]byte) *World {
	t.Helper()
	db, done, err := chunks.OpenView(w.write(t, extra), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	got, err := Scan(context.Background(), db, snapshotAt, len(w))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func at(d chunks.Dimension, x, z int32) chunks.Pos { return chunks.Pos{Dim: d, X: x, Z: z} }

func nameAt(w *World, d chunks.Dimension, x, z int32) string {
	b, ok := w.At(d, x, z)
	if !ok {
		return ""
	}
	return b.Name
}

func TestScanReadsEachDimensionsBiomesByBlock(t *testing.T) {
	w := scanOf(t, testWorld{
		at(chunks.Overworld, 0, 0):   columnsOf(westOf(5, plains, river)),
		at(chunks.Overworld, -1, -3): columnsOf(all(desert)),
		at(chunks.Nether, 0, 0):      columnsOf(all(hell)),
		at(chunks.End, 2, 2):         columnsOf(all(theEnd)),
	}, nil)
	for _, c := range []struct {
		d    chunks.Dimension
		x, z int32
		want string
	}{
		{chunks.Overworld, 0, 0, "plains"},
		{chunks.Overworld, 4, 15, "plains"},
		{chunks.Overworld, 5, 15, "river"},
		{chunks.Overworld, 15, 0, "river"},
		// Chunk -1, -3 is blocks -16 to -1 and -48 to -33.
		{chunks.Overworld, -1, -33, "desert"},
		{chunks.Overworld, -16, -48, "desert"},
		{chunks.Overworld, -17, -48, ""},
		{chunks.Overworld, -1, -32, ""},
		{chunks.Overworld, 16, 0, ""},
		{chunks.Nether, 3, 3, "hell"},
		{chunks.End, 35, 40, "the_end"},
		{chunks.End, 3, 3, ""},
	} {
		if got := nameAt(w, c.d, c.x, c.z); got != c.want {
			t.Errorf("%s %d, %d = %q, want %q", c.d.Name(), c.x, c.z, got, c.want)
		}
	}
	if w.Stats.Chunks != 4 || w.Stats.Kinds != 5 || w.Stats.Unknown != 0 || w.Stats.Malformed != 0 {
		t.Errorf("stats = %+v", w.Stats)
	}
	if _, ok := w.At(chunks.Dimension(7), 0, 0); ok {
		t.Error("a dimension the game does not have was answered")
	}
}

// A biome added by a later game version is not in the list. It is still a
// biome of this world, and must be drawn, counted and found as itself.
func TestABiomeTheListDoesNotHaveIsKeptAsUnknown(t *testing.T) {
	w := scanOf(t, testWorld{at(chunks.Overworld, 0, 0): columnsOf(westOf(8, plains, 250))}, nil)
	b, ok := w.At(chunks.Overworld, 9, 0)
	if !ok || b.Known || b.ID != 250 || b.Name != "unknown_250" || b.Label != "Unknown biome 250" || len(b.Color) != 7 {
		t.Fatalf("got %+v, %v", b, ok)
	}
	if w.Stats.Unknown != 1 {
		t.Errorf("unknown = %d, want 1", w.Stats.Unknown)
	}
	var found *Presence
	for _, p := range w.Present(chunks.Overworld) {
		if p.ID == 250 {
			found = &p
		}
	}
	if found == nil || found.Area != 128 {
		t.Fatalf("the listing has %+v, want unknown_250 over 128 columns", found)
	}
	id, ok := Resolve("unknown_250")
	if !ok || id != 250 {
		t.Fatalf("Resolve = %d, %v", id, ok)
	}
	if hits, _ := w.Nearest(chunks.Overworld, id, 0, 0, 5); len(hits) != 1 || hits[0].X != 8 {
		t.Errorf("nearest = %+v", hits)
	}
	if _, ok := w.Tile(chunks.Overworld, 0, 0, 0, &id); !ok {
		t.Error("no tile picks it out")
	}
}

func TestScanCountsARecordItCannotReadAndKeepsTheRest(t *testing.T) {
	w := scanOf(t, testWorld{
		at(chunks.Overworld, 0, 0): columnsOf(all(plains)),
		at(chunks.Overworld, 1, 0): record(flat(64), []byte{7 << 1}),
		at(chunks.Overworld, 2, 0): columnsOf(all(desert))[:600],
	}, map[string][]byte{
		// Not chunk records at all, whatever their length.
		"BiomeData": {1, 2, 3},
		"\x05\x00\x00\x00\x00\x00\x00\x00\x2b\x03": []byte("a sub-chunk's key is a byte longer"),
	})
	if w.Stats.Chunks != 1 || w.Stats.Malformed != 2 {
		t.Errorf("stats = %+v, want 1 chunk and 2 malformed", w.Stats)
	}
	if nameAt(w, chunks.Overworld, 3, 3) != "plains" || nameAt(w, chunks.Overworld, 20, 3) != "" || nameAt(w, chunks.Overworld, 85, 3) != "" {
		t.Error("a record that could not be read left a biome behind, or cost one that could")
	}
}

func presence(w *World, d chunks.Dimension) map[string]Presence {
	out := map[string]Presence{}
	for _, p := range w.Present(d) {
		out[p.Name] = p
	}
	return out
}

// Two stretches of plains with desert between them, the western one
// crossing a chunk edge:
//
//	x: -16..-1   0..15    16..31   32..47   48..63
//	   plains    pl|des   desert   desert   plains
func plainsAndDesert() testWorld {
	return testWorld{
		at(chunks.Overworld, -1, 0): columnsOf(all(plains)),
		at(chunks.Overworld, 0, 0):  columnsOf(westOf(6, plains, desert)),
		at(chunks.Overworld, 1, 0):  columnsOf(all(desert)),
		at(chunks.Overworld, 2, 0):  columnsOf(all(desert)),
		at(chunks.Overworld, 3, 0):  columnsOf(all(plains)),
	}
}

func TestPresentListsAreasLargestFirst(t *testing.T) {
	w := scanOf(t, plainsAndDesert(), nil)
	list := w.Present(chunks.Overworld)
	if len(list) != 2 || list[0].Name != "desert" || list[1].Name != "plains" {
		t.Fatalf("listing = %+v", list)
	}
	got := presence(w, chunks.Overworld)
	if p := got["plains"]; p.Area != 256+6*16+256 || p.Chunks != 3 || p.Regions != 2 || !p.Known || p.Label != "Plains" {
		t.Errorf("plains = %+v", p)
	}
	if d := got["desert"]; d.Area != 10*16+512 || d.Chunks != 3 || d.Regions != 1 {
		t.Errorf("desert = %+v", d)
	}
	if len(w.Present(chunks.Nether)) != 0 {
		t.Error("the nether lists biomes it does not have")
	}
}

// Chunks holding a biome are one stretch of it when they touch or have
// one chunk between them, corner to corner included, and not from any
// further off.
func TestStretchesJoinAcrossOneChunkAndNoFurther(t *testing.T) {
	w := scanOf(t, testWorld{
		at(chunks.Overworld, 0, 0):  columnsOf(all(forest)),
		at(chunks.Overworld, 2, 0):  columnsOf(all(forest)),
		at(chunks.Overworld, 4, 2):  columnsOf(all(forest)),
		at(chunks.Overworld, 4, 5):  columnsOf(all(forest)),
		at(chunks.Overworld, 1, 5):  columnsOf(all(forest)),
		at(chunks.Overworld, -1, 7): columnsOf(all(forest)),
	}, nil)
	if p := presence(w, chunks.Overworld)["forest"]; p.Regions != 3 {
		t.Errorf("%d stretches, want 3", p.Regions)
	}
	_, north, _, _, _ := w.Region(chunks.Overworld, 0, 0, 10)
	if north != (Extent{Area: 3 * 256, Chunks: 3, MinX: 0, MinZ: 0, MaxX: 79, MaxZ: 47}) {
		t.Errorf("the northern stretch = %+v", north)
	}
	if _, alone, _, _, _ := w.Region(chunks.Overworld, 64, 80, 10); alone.Chunks != 1 {
		t.Errorf("a chunk three away was joined: %+v", alone)
	}
	if _, south, _, _, _ := w.Region(chunks.Overworld, 16, 80, 10); south.Chunks != 2 || south.MinX != -16 {
		t.Errorf("the southern stretch = %+v", south)
	}
}

func TestNearestGivesOneHitAStretchNearestFirst(t *testing.T) {
	w := scanOf(t, plainsAndDesert(), nil)
	// From the middle of the eastern desert chunk, the eastern plains
	// are nearer.
	hits, more := w.Nearest(chunks.Overworld, plains, 40, 8, 10)
	if len(hits) != 2 || more != 0 {
		t.Fatalf("hits = %+v, more %d", hits, more)
	}
	east, west := hits[0], hits[1]
	if east.X != 48 || east.Z != 8 || east.Distance != 8 || east.Region != (Extent{Area: 256, Chunks: 1, MinX: 48, MinZ: 0, MaxX: 63, MaxZ: 15}) {
		t.Errorf("east = %+v", east)
	}
	// The nearest plains to the west are the last column before the
	// desert, inside the chunk the two share, and not that chunk's edge.
	if west.X != 5 || west.Z != 8 || west.Distance != 35 || west.Region != (Extent{Area: 256 + 96, Chunks: 2, MinX: -16, MinZ: 0, MaxX: 15, MaxZ: 15}) {
		t.Errorf("west = %+v", west)
	}
	for _, h := range hits {
		if nameAt(w, chunks.Overworld, h.X, h.Z) != "plains" {
			t.Errorf("hit %d, %d is not plains", h.X, h.Z)
		}
	}
	if here, _ := w.Nearest(chunks.Overworld, plains, -3, 3, 1); len(here) != 1 || here[0].Distance != 0 || here[0].X != -3 || here[0].Z != 3 {
		t.Errorf("from inside the biome = %+v", here)
	}
}

func TestNearestKeepsToItsLimitAndSaysWhatIsLeft(t *testing.T) {
	w := testWorld{}
	for i := range int32(9) {
		w[at(chunks.Overworld, i*4, 0)] = columnsOf(all(forest))
	}
	world := scanOf(t, w, nil)
	hits, more := world.Nearest(chunks.Overworld, forest, 0, 0, 4)
	if len(hits) != 4 || more != 5 {
		t.Fatalf("%d hits and %d more, want 4 and 5", len(hits), more)
	}
	for i, h := range hits {
		if h.X != int32(i)*64 {
			t.Errorf("hit %d at x %d, want %d", i, h.X, i*64)
		}
	}
	for _, c := range []struct {
		d     chunks.Dimension
		id    uint32
		limit int
	}{{chunks.Overworld, desert, 5}, {chunks.Nether, forest, 5}, {chunks.Overworld, forest, 0}, {chunks.Overworld, forest, -1}} {
		if hits, more := world.Nearest(c.d, c.id, 0, 0, c.limit); len(hits) != 0 || more != 0 {
			t.Errorf("%s biome %d limit %d = %d hits, %d more", c.d.Name(), c.id, c.limit, len(hits), more)
		}
	}
}

func TestRegionIsTheStretchABlockBelongsTo(t *testing.T) {
	w := plainsAndDesert()
	// A second row under the western plains, joined to it from the south.
	w[at(chunks.Overworld, -1, 1)] = columnsOf(all(plains))
	world := scanOf(t, w, nil)

	b, extent, rects, more, ok := world.Region(chunks.Overworld, 2, 2, 100)
	if !ok || b.Name != "plains" || more != 0 {
		t.Fatalf("region = %+v %+v, more %d, ok %v", b, extent, more, ok)
	}
	if extent != (Extent{Area: 256 + 96 + 256, Chunks: 3, MinX: -16, MinZ: 0, MaxX: 15, MaxZ: 31}) {
		t.Errorf("extent = %+v", extent)
	}
	// One row of two chunks, then the single chunk under its west end.
	if len(rects) != 2 || rects[0] != (Rect{-16, 0, 15, 15}) || rects[1] != (Rect{-16, 16, -1, 31}) {
		t.Errorf("rects = %v", rects)
	}

	b, extent, _, _, ok = world.Region(chunks.Overworld, 10, 2, 100)
	if !ok || b.Name != "desert" || extent.Area != 160+512 || extent.MaxX != 47 {
		t.Errorf("the desert at 10, 2 = %+v %+v", b, extent)
	}
	if _, east, _, _, _ := world.Region(chunks.Overworld, 50, 2, 100); east.Chunks != 1 || east.MinX != 48 {
		t.Errorf("the eastern plains = %+v", east)
	}
	if _, _, _, _, ok := world.Region(chunks.Overworld, 400, 2, 100); ok {
		t.Error("a block in no chunk has a region")
	}
}

func TestRegionKeepsToItsRowLimit(t *testing.T) {
	w := testWorld{}
	for i := range int32(10) {
		w[at(chunks.Overworld, 0, i)] = columnsOf(all(forest))
	}
	_, extent, rects, more, ok := scanOf(t, w, nil).Region(chunks.Overworld, 0, 0, 3)
	if !ok || len(rects) != 3 || more != 7 || extent.Chunks != 10 {
		t.Errorf("%d rects, %d more, extent %+v", len(rects), more, extent)
	}
}

func TestAChunkPastTheEdgeOfAnyWorldIsNotHeld(t *testing.T) {
	w := scanOf(t, testWorld{
		at(chunks.Overworld, 0, 0):           columnsOf(all(plains)),
		at(chunks.Overworld, 200_000_000, 0): columnsOf(all(plains)),
		at(chunks.Overworld, 0, -2_000_001):  columnsOf(all(plains)),
	}, nil)
	if w.Stats.Chunks != 1 || w.Stats.OutOfRange != 2 {
		t.Errorf("stats = %+v", w.Stats)
	}
}

func build(room int, tune func(*builder), add func(*builder)) (*World, *Stats) {
	w := &World{}
	b := newBuilder(&w.Stats, &room)
	if tune != nil {
		tune(b)
	}
	add(b)
	empty := 0
	w.layers = [3]*Layer{b.layer, newBuilder(&w.Stats, &empty).layer, newBuilder(&w.Stats, &empty).layer}
	return w, &w.Stats
}

func columns(biome func(x, z int) uint32) *[Columns]uint32 {
	var out [Columns]uint32
	for i := range out {
		out[i] = biome(i&15, i>>4)
	}
	return &out
}

func TestChunksPastTheLimitAreCountedAndNotHeld(t *testing.T) {
	w, stats := build(3, nil, func(b *builder) {
		for i := range int32(10) {
			b.add(i, 0, columns(all(plains)))
		}
	})
	w.finish()
	if stats.Chunks != 3 || stats.OverLimit != 7 || len(w.layers[0].cells) != 3 {
		t.Errorf("stats = %+v with %d chunks held", *stats, len(w.layers[0].cells))
	}
}

func TestBiomesPastTheNumberOfKindsShareOne(t *testing.T) {
	w, stats := build(10, nil, func(b *builder) {
		// 300 different biomes, 150 to a chunk.
		b.add(0, 0, columns(func(x, z int) uint32 { return 1000 + uint32(z*16+x)%150 }))
		b.add(1, 0, columns(func(x, z int) uint32 { return 1150 + uint32(z*16+x)%150 }))
	})
	w.finish()
	if stats.KindsLeftOut != 300-maxKinds || len(w.layers[0].ids) != maxKinds+1 {
		t.Fatalf("left out %d of %d kinds", stats.KindsLeftOut, len(w.layers[0].ids))
	}
	if first, _ := w.At(chunks.Overworld, 0, 0); first.Name != "unknown_1000" {
		t.Errorf("the first biome seen = %+v", first)
	}
	// Chunk 1's column 149 is biome 1299, the last of the 300.
	last, ok := w.At(chunks.Overworld, 16+149%16, 149/16)
	if !ok || last.Name != "unknown" || last.Known {
		t.Errorf("a biome past the limit = %+v, %v", last, ok)
	}
	if _, ok := w.Tile(chunks.Overworld, 0, 0, 0, nil); !ok {
		t.Error("the world past the limit is not drawn")
	}
}

func TestChunksPastTheColumnBudgetAreKeptAsTheirCommonestBiome(t *testing.T) {
	w, stats := build(10, func(b *builder) { b.mixedLimit = 1 }, func(b *builder) {
		b.add(0, 0, columns(westOf(3, plains, desert)))
		b.add(1, 0, columns(westOf(3, plains, desert)))
		b.add(2, 0, columns(westOf(12, plains, desert)))
	})
	w.finish()
	if stats.Coarsened != 2 || len(w.layers[0].mixed) != Columns {
		t.Fatalf("coarsened %d, %d bytes of columns", stats.Coarsened, len(w.layers[0].mixed))
	}
	for x, want := range map[int32]string{0: "plains", 5: "desert", 16: "desert", 31: "desert", 32: "plains", 47: "plains"} {
		if got := nameAt(w, chunks.Overworld, x, 0); got != want {
			t.Errorf("x %d = %q, want %q", x, got, want)
		}
	}
}

func TestPairsPastTheIndexBudgetAreDrawnAndNotFound(t *testing.T) {
	w, stats := build(100, nil, func(b *builder) {
		for i := range int32(40) {
			b.add(i*2, 0, columns(all(plains)))
			b.add(i*2, 5, columns(all(desert)))
		}
	})
	w.finishWithin(20)
	if stats.Unindexed != 60 || len(w.layers[0].entries) != 20 {
		t.Fatalf("unindexed %d, %d entries", stats.Unindexed, len(w.layers[0].entries))
	}
	// Both biomes are still found, which cutting the end off would lose.
	got := presence(w, chunks.Overworld)
	if got["plains"].Chunks != 10 || got["desert"].Chunks != 10 {
		t.Errorf("listing = %+v", got)
	}
	for i := range int32(40) {
		if nameAt(w, chunks.Overworld, i*32, 0) != "plains" {
			t.Fatalf("chunk %d is no longer drawn", i*2)
		}
		// Whether or not it is indexed, asking must not fail.
		w.Region(chunks.Overworld, i*32, 0, 10)
	}
}

func pixel(t *testing.T, data []byte, x, y int) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != TileSize || b.Dy() != TileSize {
		t.Fatalf("tile is %dx%d", b.Dx(), b.Dy())
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func colorOf(id uint32) color.NRGBA {
	rgb := rgbOf(id)
	return color.NRGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
}

func TestTileIsAddressedAsTheTerrainIs(t *testing.T) {
	w := scanOf(t, testWorld{
		at(chunks.Overworld, 0, 0):   columnsOf(westOf(5, plains, river)),
		at(chunks.Overworld, -1, -1): columnsOf(all(desert)),
		at(chunks.Overworld, 20, 3):  columnsOf(all(forest)),
	}, nil)
	clear := color.NRGBA{}

	// Zoom 0: a pixel is a block, and tile 0, 0 starts at block 0, 0.
	tile, ok := w.Tile(chunks.Overworld, 0, 0, 0, nil)
	if !ok {
		t.Fatal("no tile at 0/0/0")
	}
	for _, c := range []struct {
		x, y int
		want color.NRGBA
	}{{0, 0, colorOf(plains)}, {4, 15, colorOf(plains)}, {5, 0, colorOf(river)}, {15, 15, colorOf(river)}, {16, 0, clear}, {0, 16, clear}, {255, 255, clear}} {
		if got := pixel(t, tile, c.x, c.y); got != c.want {
			t.Errorf("zoom 0 pixel %d, %d = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	// Tile -1, -1 ends at block -1, -1: the desert is its last 16 pixels.
	tile, ok = w.Tile(chunks.Overworld, 0, -1, -1, nil)
	if !ok || pixel(t, tile, 255, 255) != colorOf(desert) || pixel(t, tile, 240, 240) != colorOf(desert) || pixel(t, tile, 239, 240) != clear {
		t.Error("zoom 0 tile -1/-1 does not end in the desert chunk")
	}
	// Chunk 20, 3 is blocks 320 to 335 and 48 to 63: tile 1, 0.
	if tile, ok = w.Tile(chunks.Overworld, 0, 1, 0, nil); !ok || pixel(t, tile, 64, 48) != colorOf(forest) || pixel(t, tile, 63, 48) != clear {
		t.Error("zoom 0 tile 1/0 does not hold the forest chunk")
	}
	if _, ok := w.Tile(chunks.Overworld, 0, 5, 5, nil); ok {
		t.Error("a tile with no chunk in it has a picture")
	}

	// Zoom -2: a pixel is four blocks, so a chunk is four pixels and the
	// plains' five columns are the first pixel and a quarter.
	tile, _ = w.Tile(chunks.Overworld, -2, 0, 0, nil)
	if pixel(t, tile, 0, 0) != colorOf(plains) || pixel(t, tile, 1, 0) != colorOf(river) || pixel(t, tile, 3, 3) != colorOf(river) || pixel(t, tile, 4, 0) != clear {
		t.Error("zoom -2 does not put a chunk in four pixels")
	}
	if pixel(t, tile, 80, 12) != colorOf(forest) {
		t.Error("zoom -2 does not put chunk 20, 3 at pixel 80, 12")
	}
	// Zoom 2: a block is four pixels.
	tile, _ = w.Tile(chunks.Overworld, 2, 0, 0, nil)
	if pixel(t, tile, 19, 0) != colorOf(plains) || pixel(t, tile, 20, 0) != colorOf(river) || pixel(t, tile, 64, 0) != clear {
		t.Error("zoom 2 does not draw a block as four pixels")
	}
	if _, ok := w.Tile(chunks.Nether, 0, 0, 0, nil); ok {
		t.Error("a dimension with no chunks has a tile")
	}
}

func TestTilePicksOneBiomeOutAndDimsTheRest(t *testing.T) {
	w := scanOf(t, testWorld{at(chunks.Overworld, 0, 0): columnsOf(westOf(5, plains, river))}, nil)
	only := uint32(river)
	tile, ok := w.Tile(chunks.Overworld, 0, 0, 0, &only)
	if !ok {
		t.Fatal("no tile")
	}
	if got := pixel(t, tile, 9, 9); got != colorOf(river) {
		t.Errorf("the biome picked out = %v", got)
	}
	if got := pixel(t, tile, 2, 2); got != dimmed {
		t.Errorf("another biome = %v, want it dimmed", got)
	}
	if got := pixel(t, tile, 40, 40); got != (color.NRGBA{}) {
		t.Errorf("ungenerated ground = %v, want it clear", got)
	}
}

func TestNamesAreFoundHoweverTheyAreTyped(t *testing.T) {
	for name, want := range map[string]uint32{
		"mushroom_island": 14, "Mushroom Fields": 14, "mushroom fields": 14, "minecraft:mushroom_island": 14,
		"deep_ocean": 24, "Deep Ocean": 24, "swampland": 6, "swamp": 6, "hell": 8, "nether wastes": 8,
		"the_end": 9, "unknown_777": 777, "777": 777,
	} {
		if id, ok := Resolve(name); !ok || id != want {
			t.Errorf("Resolve(%q) = %d, %v; want %d", name, id, ok, want)
		}
	}
	for _, name := range []string{"", "narnia", "unknown_x", "-4"} {
		if id, ok := Resolve(name); ok {
			t.Errorf("Resolve(%q) = %d", name, id)
		}
	}
	if !Lookup(14).Matches(Fold("Mushroom")) || !Lookup(14).Matches(Fold("island")) || Lookup(14).Matches(Fold("ocean")) {
		t.Error("mushroom_island does not match by either of its names")
	}
	seen := map[string]uint32{}
	for id, b := range vanilla {
		if other, dup := seen[b.name]; dup {
			t.Errorf("biomes %d and %d are both named %s", id, other, b.name)
		}
		seen[b.name] = id
	}
}

func TestBuildMakesAWorldFromColumns(t *testing.T) {
	w := Build(snapshotAt, map[chunks.Pos][Columns]uint32{
		at(chunks.Overworld, 0, 0):    *columns(westOf(4, plains, desert)),
		at(chunks.Nether, -1, 0):      *columns(all(hell)),
		at(chunks.Dimension(9), 0, 0): *columns(all(plains)),
	})
	if nameAt(w, chunks.Overworld, 3, 9) != "plains" || nameAt(w, chunks.Overworld, 4, 9) != "desert" || nameAt(w, chunks.Nether, -5, 5) != "hell" {
		t.Error("the world built does not hold the columns given")
	}
	if w.Stats.Chunks != 2 || w.Stats.Kinds != 3 || !w.SnapshotAt.Equal(snapshotAt) {
		t.Errorf("stats = %+v at %s", w.Stats, w.SnapshotAt)
	}
	if hits, _ := w.Nearest(chunks.Overworld, desert, 0, 0, 1); len(hits) != 1 || hits[0].X != 4 {
		t.Errorf("nearest = %+v", hits)
	}
}
