package layers

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

func upTo(top int, block string) blockFunc {
	return func(x, y, z int) string {
		if y <= top {
			return block
		}
		return "air"
	}
}

func decodeTile(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, TileSize, TileSize) {
		t.Fatalf("a tile is %v", img.Bounds())
	}
	out := image.NewNRGBA(img.Bounds())
	for y := range TileSize {
		for x := range TileSize {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func renderer(f *fake) *Renderer { return &Renderer{Reader: f.reader()} }

func TestTile_DrawsEachColumnInItsBlocksColourAndLeavesUnknownGroundClear(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 0, Z: 0}, 10, upTo(10, "stone"))
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 1, Z: 0}, 10, func(x, y, z int) string {
		switch {
		case y <= 6:
			return "dirt"
		case y <= 10 && x < 8:
			return "air"
		case y <= 10:
			return "lava"
		}
		return "air"
	})
	r := renderer(f)
	spec := Spec{Dim: chunks.Overworld, Cut: 10, Thickness: 16}
	data, tm, ok, err := r.Tile(spec, 0, 0, 0)
	if err != nil || !ok {
		t.Fatalf("ok %v, error %v", ok, err)
	}
	img := decodeTile(t, data)
	stone := color.NRGBA{0x7d, 0x7d, 0x7d, 255}
	// Away from any edge of a step, a block is its colour at full strength
	// where it is at the cut.
	if got := img.NRGBAAt(5, 5); got != stone {
		t.Errorf("stone at the cut is %v, want %v", got, stone)
	}
	// Dirt four blocks under the cut is darker than dirt at it would be:
	// down to 60% over the band, so four of sixteen is 90% of it.
	dirt := img.NRGBAAt(20, 5)
	if want := (color.NRGBA{0x86 * 230 / 256, 0x60 * 230 / 256, 0x43 * 230 / 256, 255}); abs(int(dirt.R)-int(want.R)) > 1 || abs(int(dirt.G)-int(want.G)) > 1 {
		t.Errorf("dirt four under the cut is %v, want about %v", dirt, want)
	}
	// Lava is drawn at its own brightness, however far down.
	lava := img.NRGBAAt(26, 5)
	if lava.R != 0xcf || lava.G != 0x5c || lava.A != 255 {
		t.Errorf("lava is %v", lava)
	}
	// A chunk the world does not have is clear.
	if got := img.NRGBAAt(100, 100); got.A != 0 {
		t.Errorf("an ungenerated chunk is %v", got)
	}
	// Reading 17 by 17 chunks is the tile's and the row and the column
	// north and west that light its edge.
	if tm.Chunks != 17*17 || tm.Slices == 0 || tm.Bytes != len(data) || tm.Read <= 0 || tm.Paint <= 0 || tm.Encode <= 0 {
		t.Errorf("timings %+v for %d bytes", tm, len(data))
	}
}

func abs(n int) int { return max(n, -n) }

// A tile is lit from its neighbours to the north and west, including over
// its own edge, or the picture would have a seam at every tile.
func TestTile_AnEdgeIsLitFromTheTileBesideIt(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 15, Z: 0}, 10, upTo(10, "stone"))
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 16, Z: 0}, 14, upTo(14, "stone"))
	r := renderer(f)
	spec := Spec{Dim: chunks.Overworld, Cut: 14, Thickness: 16}
	data, _, ok, err := r.Tile(spec, 0, 1, 0)
	if err != nil || !ok {
		t.Fatalf("ok %v, error %v", ok, err)
	}
	img := decodeTile(t, data)
	// The column at the edge stands four above its western neighbour, and
	// level with its northern; the next column in stands level with both.
	edge, inside := img.NRGBAAt(0, 5), img.NRGBAAt(1, 5)
	if edge.R <= inside.R {
		t.Errorf("the edge is %v and the column beside it %v: the step was not lit", edge, inside)
	}
	if want := 0x7d * (256 + 4*9) / 256; abs(int(edge.R)-want) > 1 {
		t.Errorf("the edge is %d, want %d", edge.R, want)
	}
	// With nothing to the west the edge is lit as flat ground.
	alone := newFake(t)
	alone.chunk(chunks.Pos{Dim: chunks.Overworld, X: 16, Z: 0}, 14, upTo(14, "stone"))
	data, _, _, _ = renderer(alone).Tile(spec, 0, 1, 0)
	if got := decodeTile(t, data).NRGBAAt(0, 5); got.R != 0x7d {
		t.Errorf("an edge with no neighbour is %v", got)
	}
}

func TestTile_AZoomOutAveragesTheBlocksOfEachPixel(t *testing.T) {
	f := newFake(t)
	// Chunk pairs of stone and dirt: a pixel at zoom -1 covers 2 by 2
	// blocks, so one is a single block's colour and the rest are alike.
	for cx := range 4 {
		block := "stone"
		if cx%2 == 1 {
			block = "sand"
		}
		f.chunk(chunks.Pos{Dim: chunks.Overworld, X: int32(cx), Z: 0}, 10, upTo(10, block))
	}
	r := renderer(f)
	spec := Spec{Dim: chunks.Overworld, Cut: 10, Thickness: 16}
	data, tm, ok, err := r.Tile(spec, -1, 0, 0)
	if err != nil || !ok {
		t.Fatalf("ok %v, error %v", ok, err)
	}
	img := decodeTile(t, data)
	// A chunk is 8 pixels; its middle is its block's colour.
	for i, want := range []uint8{0x7d, 0xdb, 0x7d, 0xdb} {
		if got := img.NRGBAAt(i*8+4, 4); got.R != want || got.A != 255 {
			t.Errorf("chunk %d is %v, want red %d", i, got, want)
		}
	}
	// The edge of the world is half covered where a pixel straddles it,
	// and clear where none of its blocks are there.
	if got := img.NRGBAAt(40, 4); got.A != 0 {
		t.Errorf("past the world's edge is %v", got)
	}
	if tm.Chunks != 33*33 {
		t.Errorf("a tile at zoom -1 read %d chunks", tm.Chunks)
	}
}

func TestTile_HasNoPictureForAnEmptyTileAndRefusesWhatItCannotDraw(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 0, Z: 0}, 10, upTo(10, "stone"))
	r := renderer(f)
	good := Spec{Dim: chunks.Overworld, Cut: 10, Thickness: 16}
	if _, _, ok, err := r.Tile(good, 0, 5, 5); ok || err != nil {
		t.Errorf("an empty tile: ok %v, error %v", ok, err)
	}
	// A cut above the world's ground in a world of stone at 10 holds
	// ground only in the band that reaches it.
	if _, _, ok, _ := r.Tile(Spec{Dim: chunks.Overworld, Cut: 100, Thickness: 16}, 0, 0, 0); ok {
		t.Error("a band over the ground drew a tile")
	}
	for _, c := range []struct {
		name string
		spec Spec
		zoom int
	}{
		{"a zoom past the finest", good, 1},
		{"a zoom past the widest", good, MinZoom - 1},
		{"a cut outside the dimension", Spec{Dim: chunks.Overworld, Cut: 400, Thickness: 16}, 0},
		{"no band", Spec{Dim: chunks.Overworld, Cut: 10}, 0},
	} {
		if _, _, ok, err := r.Tile(c.spec, c.zoom, 0, 0); ok || err == nil {
			t.Errorf("%s: ok %v, error %v", c.name, ok, err)
		}
	}
}

// A slice that will not read leaves a gap in the chunk and does not take
// the tile with it.
func TestTile_ADamagedSliceIsAGapAndNotAFailure(t *testing.T) {
	f := newFake(t)
	pos := chunks.Pos{Dim: chunks.Overworld, X: 0, Z: 0}
	f.chunk(pos, 40, upTo(40, "stone"))
	f.put(pos, 1, []byte{9, 1, 1, 0xff})
	r := renderer(f)
	data, tm, ok, err := r.Tile(Spec{Dim: chunks.Overworld, Cut: 40, Thickness: 40}, 0, 0, 0)
	if err != nil || !ok {
		t.Fatalf("ok %v, error %v", ok, err)
	}
	if tm.Unreadable != 1 {
		t.Errorf("%d chunks unreadable", tm.Unreadable)
	}
	// The damaged slice is y 16 to 31; the column is seen through it as
	// stone from the slice below, since the slices above are stone too.
	if got := decodeTile(t, data).NRGBAAt(3, 3); got.A != 255 {
		t.Errorf("the chunk is %v", got)
	}
}

func TestTile_TheFloorStyleDimsRockAndShowsASpace(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 0, Z: 0}, 20, func(x, y, z int) string {
		switch {
		case x >= 8 && y >= 0 && y <= 3:
			return "air"
		case y <= 20:
			return "stone"
		}
		return "air"
	})
	r := renderer(f)
	data, _, ok, err := r.Tile(Spec{Dim: chunks.Overworld, Cut: 3, Thickness: 16, Style: Floor}, 0, 0, 0)
	if err != nil || !ok {
		t.Fatalf("ok %v, error %v", ok, err)
	}
	img := decodeTile(t, data)
	rock, space := img.NRGBAAt(3, 3), img.NRGBAAt(12, 3)
	// Rock with a roof over it is drawn at about half; the floor of the
	// space, a block under the cut, is nearly at full.
	if int(rock.R)*3 > int(space.R)*2 {
		t.Errorf("rock %v is not much dimmer than a floor %v", rock, space)
	}
}
