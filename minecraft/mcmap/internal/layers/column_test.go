package layers

import (
	"context"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// A palette of every width the game packs, and the several ways a slice
// can be written, read back block for block.
func TestDecode_ReadsEveryWidthAndVersionBlockForBlock(t *testing.T) {
	kinds := []string{"stone", "dirt", "granite", "diorite", "andesite", "gravel", "sand", "clay", "tuff", "calcite", "obsidian", "glowstone"}
	// A block's kind is a function of where it is that uses them unevenly.
	pick := func(n int) blockFunc {
		return func(x, y, z int) string { return kinds[(x*7+y*3+z*5+x*y)%n] }
	}
	for _, c := range []struct {
		name    string
		version byte
		kinds   int
		width   int
	}{
		{"1 bit", 8, 2, 1},
		{"2 bits", 8, 4, 2},
		{"3 bits", 8, 5, 3},
		{"4 bits", 9, 12, 4},
		{"5 bits", 9, 12, 5},
		{"6 bits", 9, 12, 6},
		{"8 bits", 9, 12, 8},
		{"16 bits", 9, 12, 16},
		{"the narrowest", 9, 9, 0},
		{"the older version", 1, 3, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := NewRegistry()
			var col Column
			col.begin(chunks.Pos{Dim: chunks.Overworld})
			rec := sliceRecord(t, c.version, 2, c.width, pick(c.kinds))
			if err := col.load(reg, 2, rec); err != nil {
				t.Fatal(err)
			}
			blocks := reg.Blocks()
			want := pick(c.kinds)
			for x := range 16 {
				for z := range 16 {
					for y := range 16 {
						if got := blocks[col.ID(x, 32+y, z)].Name; got != want(x, y, z) {
							t.Fatalf("block %d,%d,%d is %q, want %q", x, y, z, got, want(x, y, z))
						}
					}
				}
			}
		})
	}
}

func TestDecode_ALayerAtNoWidthIsOneBlockEverywhere(t *testing.T) {
	reg := NewRegistry()
	var col Column
	col.begin(chunks.Pos{Dim: chunks.Overworld})
	if err := col.load(reg, 0, sliceRecord(t, 9, 0, -1, all("deepslate"))); err != nil {
		t.Fatal(err)
	}
	if got := reg.Blocks()[col.ID(3, 5, 9)].Name; got != "deepslate" {
		t.Errorf("block is %q", got)
	}
	// Nothing was unpacked for it.
	if !col.slots[4].uniform {
		t.Error("a layer of one block was unpacked block by block")
	}
}

// The second layer is the water a block stands in. It is read as a mark on
// the block and never as the block.
func TestDecode_TheSecondLayerIsWhatTheBlockIsWaterloggedIn(t *testing.T) {
	reg := NewRegistry()
	var col Column
	col.begin(chunks.Pos{Dim: chunks.Overworld})
	solid := func(x, y, z int) string {
		if y == 4 {
			return "oak_slab"
		}
		return "air"
	}
	wet := func(x, y, z int) string {
		if y == 4 && x < 8 {
			return "water"
		}
		return "air"
	}
	if err := col.load(reg, 0, sliceRecord(t, 8, 0, 0, solid, wet)); err != nil {
		t.Fatal(err)
	}
	blocks := reg.Blocks()
	if got := blocks[col.ID(2, 4, 2)].Name; got != "oak_slab" {
		t.Errorf("a waterlogged slab is %q", got)
	}
	if !col.Wet(2, 4, 2) || col.Wet(12, 4, 2) || col.Wet(2, 5, 2) {
		t.Errorf("wet at the slab in the water %v, past it %v, over it %v", col.Wet(2, 4, 2), col.Wet(12, 4, 2), col.Wet(2, 5, 2))
	}
	// A second layer with no liquid in it marks nothing, and a slice read
	// after one that had marks does not inherit them.
	none := func(int, int, int) string { return "air" }
	if err := col.load(reg, 0, sliceRecord(t, 8, 0, 0, solid, none)); err != nil {
		t.Fatal(err)
	}
	if col.Wet(2, 4, 2) {
		t.Error("the marks of the last slice were kept")
	}
}

func TestDecode_RefusesWhatIsNotLaidOutAsItReadsAndNeverPanics(t *testing.T) {
	reg := NewRegistry()
	good := sliceRecord(t, 9, 0, 0, func(x, y, z int) string {
		if x == 3 {
			return "stone"
		}
		return "air"
	})
	var col Column
	col.begin(chunks.Pos{Dim: chunks.Overworld})
	for cut := range len(good) {
		// Any length may fail, and none may do worse.
		_ = col.load(reg, 0, good[:cut])
	}
	for _, bad := range [][]byte{
		nil, {9}, {7, 1, 0}, {9, 0, 0, 0}, {9, 1, 0, 1}, {8, 1, 0xff, 0xff},
		// A palette that claims more blocks than a slice has.
		append(append([]byte{8, 1, 4 << 1}, make([]byte, 4096/8*4)...), 0xff, 0xff, 0xff, 0x7f),
	} {
		if err := col.load(reg, 0, bad); err == nil {
			t.Errorf("%v was read", bad)
		}
	}
	// What a failed read leaves is a slice that is not there.
	col.begin(chunks.Pos{Dim: chunks.Overworld})
	_ = col.load(reg, 0, good)
	_ = col.load(reg, 0, good[:20])
	if col.Slices != 0 || col.ID(3, 1, 1) != AirID {
		t.Errorf("a slice that would not read is still there: %d slices", col.Slices)
	}
}

func TestBlockName_StepsOverStatesOfEveryKind(t *testing.T) {
	rec := entry(t, "oak_stairs")
	name, rest, err := blockName(append(rec[:len(rec):len(rec)], 0xaa))
	if err != nil || string(name) != "minecraft:oak_stairs" || len(rest) != 1 || rest[0] != 0xaa {
		t.Errorf("name %q rest %v err %v", name, rest, err)
	}
	for cut := range len(rec) {
		if _, _, err := blockName(rec[:cut]); err == nil {
			t.Errorf("an entry cut at %d was read", cut)
		}
	}
}

func TestRead_AndWalkFindTheSameChunksAndTheirSlices(t *testing.T) {
	f := newFake(t)
	ground := func(x, y, z int) string {
		switch {
		case y < 0:
			return "deepslate"
		case y < 40:
			return "stone"
		}
		return "air"
	}
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 2, Z: -3}, 63, ground)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: -1, Z: 0}, 63, ground)
	f.chunk(chunks.Pos{Dim: chunks.Nether, X: 2, Z: -3}, 127, ground)
	// A chunk that holds nothing but its version has no slices.
	if err := f.db.Put(chunks.RecordKey(chunks.Pos{Dim: chunks.Overworld, X: 9, Z: 9}, 0x2c), []byte{40}, nil); err != nil {
		t.Fatal(err)
	}
	r := f.reader()

	byRead := map[chunks.Pos]int{}
	for _, p := range []chunks.Pos{{Dim: chunks.Overworld, X: 2, Z: -3}, {Dim: chunks.Overworld, X: -1, Z: 0}, {Dim: chunks.Overworld, X: 9, Z: 9}, {Dim: chunks.Overworld, X: 5, Z: 5}} {
		col := new(Column)
		if found, err := r.Read(p, col); err != nil {
			t.Fatal(err)
		} else if found {
			byRead[p] = col.Slices
		}
	}
	byWalk := map[chunks.Pos]int{}
	err := r.Walk(context.Background(), chunks.Overworld, func(c *Column) error {
		byWalk[c.Pos] = c.Slices
		if blocks := r.Registry.Blocks(); blocks[c.ID(4, -1, 4)].Name != "deepslate" || blocks[c.ID(4, 39, 4)].Name != "stone" || c.ID(4, 40, 4) != AirID {
			t.Errorf("chunk %v reads wrongly", c.Pos)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Overworld slices run from -64: 4 below, and 0 to 3 above.
	if len(byRead) != 2 || len(byWalk) != 2 || byRead[chunks.Pos{Dim: chunks.Overworld, X: 2, Z: -3}] != 8 {
		t.Errorf("read %v, walked %v", byRead, byWalk)
	}
	for p, n := range byRead {
		if byWalk[p] != n {
			t.Errorf("%v: %d slices by lookup, %d by walk", p, n, byWalk[p])
		}
	}
	nether := 0
	_ = r.Walk(context.Background(), chunks.Nether, func(c *Column) error { nether++; return nil })
	if nether != 1 {
		t.Errorf("the walk of the nether found %d chunks", nether)
	}
}

func TestWalk_StopsAtTheErrorOfTheCallerAndOnACancelledContext(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 1, Z: 1}, 15, all("stone"))
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 2, Z: 1}, 15, all("stone"))
	r := f.reader()
	calls := 0
	want := context.DeadlineExceeded
	if err := r.Walk(context.Background(), chunks.Overworld, func(*Column) error { calls++; return want }); err != want || calls != 1 {
		t.Errorf("%d calls, error %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Walk(ctx, chunks.Overworld, func(*Column) error { return nil }); err == nil {
		t.Error("a cancelled walk ended without error")
	}
}
