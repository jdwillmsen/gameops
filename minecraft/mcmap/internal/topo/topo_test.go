package topo

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// A world is the surface of every column in a square of chunks, from f; a
// column f says has no surface is one the world holds with no block in it.
// Chunks outside the square are not generated.
func world(t testing.TB, d chunks.Dimension, from, to int32, f func(x, z int32) (y int32, ok bool)) *heights.Map {
	t.Helper()
	floor, _ := heights.Floor(d)
	b := heights.NewBuilder(time.Unix(1_700_000_000, 0), -1)
	for cz := from; cz < to; cz++ {
		for cx := from; cx < to; cx++ {
			var cols [heights.Columns]int16
			for i := range cols {
				if y, ok := f(cx*16+int32(i%16), cz*16+int32(i/16)); ok {
					cols[i] = int16(y - floor + 1)
				}
			}
			if err := b.AddColumns(d, cx, cz, &cols); err != nil {
				t.Fatal(err)
			}
		}
	}
	return b.Map()
}

func overworld(t testing.TB, from, to int32, f func(x, z int32) int32) *heights.Map {
	return world(t, chunks.Overworld, from, to, func(x, z int32) (int32, bool) { return f(x, z), true })
}

func decode(t testing.TB, data []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	for y := range out.Rect.Dy() {
		for x := range out.Rect.Dx() {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA))
		}
	}
	return out
}

func picture(t testing.TB, m *heights.Map, d chunks.Dimension, zoom, tx, ty int, styles Styles) *image.NRGBA {
	t.Helper()
	data, ok := Tile(m, d, zoom, tx, ty, Pick{Styles: styles})
	if !ok {
		t.Fatalf("no tile at zoom %d, %d, %d", zoom, tx, ty)
	}
	return decode(t, data)
}

func luma(c color.NRGBA) float64 {
	return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
}

// Light is from the north-west: what faces it is lit, what faces away is
// shadowed, what faces across it and what is flat are neither. A plane is
// said by the way it falls, as a slope is, so one that rises to the
// south-east falls to the north-west and faces the light.
func TestSlopesAreLitFromTheNorthWest(t *testing.T) {
	const rise = 0.25
	plane := func(east, south float64) *heights.Map {
		return overworld(t, -2, 18, func(x, z int32) int32 {
			return 100 + int32(math.Round(rise*(east*float64(x)+south*float64(z))))
		})
	}
	for _, c := range []struct {
		name        string
		east, south float64
		want        string
	}{
		{"falls to the north-west", 1, 1, "lit"},
		{"falls to the south-east", -1, -1, "dark"},
		{"falls to the north-east", -1, 1, "neither"},
		{"falls to the south-west", 1, -1, "neither"},
		{"flat", 0, 0, "neither"},
	} {
		got := picture(t, plane(c.east, c.south), chunks.Overworld, 0, 0, 0, Shade).NRGBAAt(128, 128)
		switch c.want {
		case "lit":
			if got.A == 0 || luma(got) < 200 {
				t.Errorf("a plane that %s is %v, want light", c.name, got)
			}
		case "dark":
			if got.A == 0 || luma(got) > 60 {
				t.Errorf("a plane that %s is %v, want shadow", c.name, got)
			}
		default:
			if got.A > 4 {
				t.Errorf("a plane that %s is %v, want nothing drawn", c.name, got)
			}
		}
	}
}

func TestASteeperSlopeIsLitMoreAndShadowedMore(t *testing.T) {
	at := func(rise float64) color.NRGBA {
		m := overworld(t, -2, 18, func(x, z int32) int32 { return 100 + int32(math.Round(rise*float64(x+z))) })
		return picture(t, m, chunks.Overworld, 0, 0, 0, Shade).NRGBAAt(128, 128)
	}
	if gentle, steep := at(0.25), at(0.5); steep.A <= gentle.A {
		t.Errorf("a plane twice as steep is drawn as strongly: %v against %v", steep, gentle)
	}
	if gentle, steep := at(-0.25), at(-0.5); steep.A <= gentle.A {
		t.Errorf("a shadowed plane twice as steep is drawn as strongly: %v against %v", steep, gentle)
	}
}

// A hill is its own oracle: its side to the light is brighter than its side
// from it, whichever way across the map the hill stands.
func TestAHillIsBrighterOnItsNorthWestSide(t *testing.T) {
	m := overworld(t, -2, 18, func(x, z int32) int32 {
		return 100 + int32(math.Max(0, 30-math.Hypot(float64(x-128), float64(z-128))/1.5))
	})
	img := picture(t, m, chunks.Overworld, 0, 0, 0, Shade)
	nw, se := img.NRGBAAt(128-20, 128-20), img.NRGBAAt(128+20, 128+20)
	if luma(nw) <= luma(se) || nw.A == 0 || se.A == 0 {
		t.Errorf("the hill is %v on its north-west side and %v on its south-east", nw, se)
	}
}

// What is drawn of ground does not depend on which tile it is drawn in:
// the same land read from tiles that cut it in other places is the same
// picture, which holds only if each tile reads past its own edge as far as
// a slope, a blur or a line looks.
func TestAdjacentTilesDrawTheSameLandAsOne(t *testing.T) {
	land := func(x, z int32) int32 {
		fx, fz := float64(x), float64(z)
		return 90 + int32(25*math.Sin(fx/23)+18*math.Cos(fz/17)+9*math.Sin((fx+fz)/6)) + (x*7+z*13)%3
	}
	for _, zoom := range []int{4, 3, 1, 0, -1, -2, -3} {
		// A tile is this many blocks across, and the land is moved by half
		// of it, so that what lay across the edge between two tiles is in the
		// middle of one.
		across := int32(TileSize) << max(-zoom, 0) >> max(zoom, 0)
		shift := across / 2
		const half = TileSize / 2
		// Two tiles across, and a chunk's width round them.
		edge := 2*across/16 + 2
		whole := overworld(t, -2, edge, land)
		moved := overworld(t, -2, edge, func(x, z int32) int32 { return land(x+shift, z+shift) })
		for _, styles := range []Styles{Shade, Contours, Tint, Shade | Contours | Tint} {
			joined := image.NewNRGBA(image.Rect(0, 0, 2*TileSize, 2*TileSize))
			for ty := range 2 {
				for tx := range 2 {
					tile := picture(t, whole, chunks.Overworld, zoom, tx, ty, styles)
					for y := range TileSize {
						for x := range TileSize {
							joined.SetNRGBA(tx*TileSize+x, ty*TileSize+y, tile.NRGBAAt(x, y))
						}
					}
				}
			}
			middle := picture(t, moved, chunks.Overworld, zoom, 0, 0, styles)
			differ, first, drawn := 0, "", 0
			for y := range TileSize {
				for x := range TileSize {
					a, b := middle.NRGBAAt(x, y), joined.NRGBAAt(x+int(half), y+int(half))
					if a.A != 0 {
						drawn++
					}
					if a != b {
						if differ == 0 {
							first = fmt.Sprintf("at %d, %d: %v against %v", x, y, a, b)
						}
						differ++
					}
				}
			}
			// A few lines across a tile are as much as lines are.
			if least := map[bool]int{false: TileSize * TileSize / 20, true: 4 * TileSize}[styles == Contours]; drawn < least {
				t.Errorf("zoom %d, styles %d: only %d pixels of the tile are drawn, which is no test of the edge", zoom, styles, drawn)
			}
			if differ != 0 {
				t.Errorf("zoom %d, styles %d: %d pixels differ between a tile cut across the edge and the two tiles beside it, the first %s", zoom, styles, differ, first)
			}
		}
	}
}

// A block that is several pixels is not drawn as a square of one colour: the
// land is smooth between the middles of blocks, so a line across a slope of
// whole blocks is a line a pixel or two wide and not a block wide, and the
// shade changes a step at a time across the face of a block.
func TestFinerThanABlockIsDrawnSmoothAndNotAsSquares(t *testing.T) {
	m := overworld(t, -2, 18, func(x, z int32) int32 { return 100 + (x+z)/4 })
	const zoom = 3
	lines := picture(t, m, chunks.Overworld, zoom, 0, 0, Contours)
	shade := picture(t, m, chunks.Overworld, zoom, 0, 0, Shade)
	runs, longest := 0, 0
	for y := 100; y < 156; y += 11 {
		run := 0
		for x := range TileSize {
			if lines.NRGBAAt(x, y).A > 0 {
				run++
				longest = max(longest, run)
				continue
			}
			if run > 0 {
				runs++
			}
			run = 0
		}
	}
	// A block is 8 pixels across here.
	if runs < 6 || longest > 3 {
		t.Errorf("%d lines across the slope, the widest %d pixels: want thin lines", runs, longest)
	}
	last := shade.NRGBAAt(0, 128)
	for x := 1; x < TileSize; x++ {
		got := shade.NRGBAAt(x, 128)
		if d := math.Abs(float64(got.A) - float64(last.A)); d > 12 {
			t.Fatalf("the shade jumps from %v to %v at pixel %d: it is blocks and not smooth", last, got, x)
		}
		last = got
	}
}

func TestFinerThanABlockStopsAtTheEdgeOfWhatIsKnown(t *testing.T) {
	m := world(t, chunks.Overworld, -2, 18, func(x, z int32) (int32, bool) { return 100 + (x+z)/4, x < 8 })
	img := picture(t, m, chunks.Overworld, 3, 0, 0, Shade|Contours|Tint)
	// 8 pixels to a block: block 8 is the first with no surface.
	for x := 64; x < TileSize; x++ {
		if got := img.NRGBAAt(x, 20); got.A != 0 {
			t.Fatalf("pixel %d is of a block with no surface and is drawn %v", x, got)
		}
	}
	if got := img.NRGBAAt(60, 20); got.A == 0 {
		t.Errorf("pixel 60 is of a block with a surface and is drawn %v", got)
	}
}

// Ground a world has not generated, and a column with no block in it, is
// left clear and is not a cliff for the ground beside it.
func TestGroundWithNoSurfaceIsLeftClearAndIsNoCliff(t *testing.T) {
	// A slope that ends at x 64, with nothing beyond; and the same slope
	// with its far half a column of no blocks.
	edge := func(x int32) bool { return x < 64 }
	slope := func(x, z int32) int32 { return 100 + (x+z)/4 }
	generated := world(t, chunks.Overworld, -2, 18, func(x, z int32) (int32, bool) { return slope(x, z), edge(x) })
	whole := overworld(t, -2, 18, slope)
	for _, styles := range []Styles{Shade, Contours, Tint, Shade | Contours | Tint} {
		img := picture(t, generated, chunks.Overworld, 0, 0, 0, styles)
		full := picture(t, whole, chunks.Overworld, 0, 0, 0, styles)
		for y := range 200 {
			for x := 64; x < 200; x++ {
				if got := img.NRGBAAt(x, y); got.A != 0 {
					t.Fatalf("styles %d: %d, %d has no surface and is drawn %v", styles, x, y, got)
				}
			}
			// Up to the edge it is what the whole slope is, but for the
			// few pixels the blur and a step to a neighbour reach from it.
			for x := 8; x < 56; x++ {
				if got, want := img.NRGBAAt(x, y), full.NRGBAAt(x, y); got != want {
					t.Fatalf("styles %d: %d, %d is %v, and %v where the ground goes on", styles, x, y, got, want)
				}
			}
		}
	}
	// A cliff drawn for the edge would be the darkest or the brightest
	// there is. Each column up to it is about as lit as the one 20 before.
	img := picture(t, generated, chunks.Overworld, 0, 0, 0, Shade)
	inside := img.NRGBAAt(40, 100)
	for x := 56; x < 64; x++ {
		if got := img.NRGBAAt(x, 100); int(got.A) > int(inside.A)+30 || luma(got) > luma(inside)+60 {
			t.Errorf("column %d at the edge is %v, and %v inside: a cliff", x, got, inside)
		}
	}
}

// Level ground with an edge is nothing at all, in every style but the tint.
func TestFlatGroundAtTheEdgeOfTheWorldDrawsNoLine(t *testing.T) {
	m := world(t, chunks.Overworld, -2, 18, func(x, z int32) (int32, bool) { return 100, x < 0 })
	for _, styles := range []Styles{Shade, Contours} {
		data, ok := Tile(m, chunks.Overworld, 0, -1, 0, Pick{Styles: styles})
		if !ok {
			t.Fatal("no tile")
		}
		img := decode(t, data)
		for y := range TileSize {
			for x := range TileSize {
				if got := img.NRGBAAt(x, y); got.A != 0 {
					t.Fatalf("styles %d: %d, %d of flat ground is drawn %v", styles, x, y, got)
				}
			}
		}
	}
}

// A cone is circles. Its lines are at the heights that are multiples of the
// interval, so for a cone that falls a block for each two of distance they
// are rings, which are heavy at every fifth.
func TestAConeHasClosedRingsAtTheInterval(t *testing.T) {
	const top = 180
	cone := func(x, z int32) int32 {
		return top - int32(math.Round(math.Hypot(float64(x-128), float64(z-128))/2))
	}
	img := picture(t, overworld(t, -2, 18, cone), chunks.Overworld, 0, 0, 0, Contours)
	// Along rays the lines are where the height is a multiple of five:
	// every 10 blocks out from the middle, offset by where the top falls.
	interval := Interval(0)
	if interval != 5 {
		t.Fatalf("the interval at the finest zoom is %d", interval)
	}
	for k := 2; k <= 7; k++ {
		level := top - interval*k
		radius := 2 * float64(top-level)
		heavy := (level/interval)%HeavyEvery == 0
		for deg := 0; deg < 360; deg += 15 {
			rad := float64(deg) * math.Pi / 180
			// A line is within a few pixels of its radius on every ray: whole
			// blocks and a blur move it a little.
			best := color.NRGBA{}
			for off := -3.0; off <= 3; off += 0.5 {
				x := 128 + int(math.Round((radius+off)*math.Cos(rad)))
				y := 128 + int(math.Round((radius+off)*math.Sin(rad)))
				// A line a pixel thick can be stepped across on a diagonal.
				for _, step := range [][2]int{{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					if got := img.NRGBAAt(x+step[0], y+step[1]); got.A > best.A {
						best = got
					}
				}
			}
			if best.A == 0 {
				t.Fatalf("level %d: no line at radius %.0f, %d degrees", level, radius, deg)
			}
			if got := float64(best.A) / 255; heavy != (got > 0.8) {
				t.Fatalf("level %d (heavy %v): the line at %d degrees is %.2f opaque", level, heavy, deg, got)
			}
		}
		// And between two rings there is none.
		mid := radius + float64(interval)
		x := 128 + int(math.Round(mid))
		if k < 7 {
			if got := img.NRGBAAt(x, 128); got.A != 0 {
				t.Errorf("between rings %d and %d at radius %.0f there is %v", k, k+1, mid, got)
			}
		}
	}
}

func TestContourIntervalGrowsAsTheMapIsLeftBehind(t *testing.T) {
	prev := 0
	for zoom := 0; zoom >= MinZoom; zoom-- {
		got := Interval(zoom)
		if got <= prev {
			t.Errorf("zoom %d has an interval of %d after %d", zoom, got, prev)
		}
		prev = got
	}
	// Finer than a block to a pixel the land is the same, and so are its lines.
	if Interval(MinZoom-3) != Interval(MinZoom) || Interval(MaxZoom) != Interval(0) {
		t.Error("the interval is not held at the widest zoom and at a block to a pixel")
	}
}

// Trees are what the heights hold and not what the land is: a noise of a
// few blocks about a plain, from one column to the next, is a plain. It
// draws no shadow, and at the wide zooms no line either.
func TestSpeckleOnAPlainIsNotASlopeOrALine(t *testing.T) {
	speckle := func(x, z int32) int32 { return 102 + (x*7+z*13)&1 + (x^z)&1 }
	m := overworld(t, -2, 18, speckle)
	for _, zoom := range []int{0, -1, -2, -3} {
		for _, styles := range []Styles{Shade, Contours} {
			img := picture(t, m, chunks.Overworld, zoom, 0, 0, styles)
			drawn := 0
			for y := 16; y < TileSize-16; y++ {
				for x := 16; x < TileSize-16; x++ {
					if img.NRGBAAt(x, y).A > 64 {
						drawn++
					}
				}
			}
			if drawn > 0 {
				t.Errorf("zoom %d, styles %d: %d pixels of a speckled plain are drawn strongly", zoom, styles, drawn)
			}
		}
	}
}

func TestEveryColourOfTheTintIsItsBandsAndOnlyItsBands(t *testing.T) {
	for _, d := range []chunks.Dimension{chunks.Overworld, chunks.End} {
		floor, _ := heights.Floor(d)
		bands := Bands(d)
		if len(bands) == 0 || len(bands) > 16 {
			t.Fatalf("%s has %d bands", d.Name(), len(bands))
		}
		if bands[0].From > -64 || bands[len(bands)-1].To < 319 {
			t.Errorf("%s: the bands do not cover every height: %d to %d", d.Name(), bands[0].From, bands[len(bands)-1].To)
		}
		for i, b := range bands[1:] {
			if b.From != bands[i].To+1 {
				t.Errorf("%s: band %d starts at %d after one that ends at %d", d.Name(), i+1, b.From, bands[i].To)
			}
		}
		seen := map[color.NRGBA]int{}
		for i, b := range bands {
			var heights []int
			for _, y := range []int{max(b.From, int(floor)), min(b.To, 319)} {
				heights = append(heights, y)
			}
			var first color.NRGBA
			for n, y := range heights {
				m := world(t, d, -2, 18, func(x, z int32) (int32, bool) { return int32(y), true })
				data, ok := Tile(m, d, 0, 0, 0, Pick{Styles: Tint})
				if !ok {
					t.Fatalf("%s: no tile at height %d", d.Name(), y)
				}
				got := decode(t, data).NRGBAAt(128, 128)
				if n == 0 {
					first = got
				} else if got != first {
					t.Errorf("%s: heights %d and %d are in band %d and are drawn %v and %v", d.Name(), heights[0], y, i, first, got)
				}
			}
			var want color.NRGBA
			if b.alpha > 0 {
				want = color.NRGBA{R: b.rgb[0], G: b.rgb[1], B: b.rgb[2], A: uint8(b.alpha*255 + 0.5)}
				if b.Colour != fmt.Sprintf("#%02x%02x%02x", want.R, want.G, want.B) {
					t.Errorf("%s: band %d is said to be %s and drawn %v", d.Name(), i, b.Colour, want)
				}
			}
			if d := first.A; d != want.A || (want.A > 0 && (absDiff(first.R, want.R) > 1 || absDiff(first.G, want.G) > 1 || absDiff(first.B, want.B) > 1)) {
				t.Errorf("band %d is drawn %v, want %v", i, first, want)
			}
			if b.alpha == 0 && b.Colour != "" {
				t.Errorf("%s: band %d is not drawn and says it is %s", d.Name(), i, b.Colour)
			}
			if prev, dup := seen[first]; dup && first.A > 0 {
				t.Errorf("bands %d and %d are drawn in one colour", prev, i)
			}
			seen[first] = i
		}
	}
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// The sea is the same height from shore to shore, so it is no band of the
// tint and has no shade: the terrain's own water shows as it is.
func TestTheSeaIsLeftAsItIs(t *testing.T) {
	m := overworld(t, -2, 18, func(x, z int32) int32 { return 62 })
	data, ok := Tile(m, chunks.Overworld, 0, 0, 0, Pick{Styles: Shade | Contours | Tint})
	if !ok {
		t.Fatal("no tile")
	}
	img := decode(t, data)
	for y := range TileSize {
		for x := range TileSize {
			if got := img.NRGBAAt(x, y); got.A != 0 {
				t.Fatalf("the sea at %d, %d is drawn %v", x, y, got)
			}
		}
	}
}

func TestAHiddenBandIsDrawnAsTheTerrainUnderIt(t *testing.T) {
	bands := Bands(chunks.Overworld)
	at := func(y int32, hidden uint32) color.NRGBA {
		m := overworld(t, -2, 18, func(x, z int32) int32 { return y })
		data, ok := Tile(m, chunks.Overworld, 0, 0, 0, Pick{Styles: Tint, Hidden: hidden})
		if !ok {
			t.Fatal("no tile")
		}
		return decode(t, data).NRGBAAt(128, 128)
	}
	const high = 150
	index := -1
	for i, b := range bands {
		if high >= b.From && high <= b.To {
			index = i
		}
	}
	if at(high, 0).A == 0 {
		t.Fatal("the band is not drawn to begin with")
	}
	if got := at(high, 1<<index); got.A != 0 {
		t.Errorf("a hidden band is drawn %v", got)
	}
	if at(high, 1<<index) == at(high, 0) || at(high, ^uint32(1<<index)) != at(high, 0) {
		t.Error("hiding the other bands changed this one, or hiding it changed nothing")
	}
}

func TestTheNetherAndNoStylesHaveNoTiles(t *testing.T) {
	roof := world(t, chunks.Nether, -2, 18, func(x, z int32) (int32, bool) { return 127, true })
	for _, c := range []struct {
		name  string
		d     chunks.Dimension
		zoom  int
		pick  Pick
		there bool
	}{
		{"the nether", chunks.Nether, 0, Pick{Styles: allStyles}, false},
		{"no styles", chunks.Overworld, 0, Pick{}, false},
		{"a style there is not", chunks.Overworld, 0, Pick{Styles: 8}, false},
		{"a zoom past the finest", chunks.Overworld, MaxZoom + 1, Pick{Styles: Shade}, false},
		{"a zoom past the widest", chunks.Overworld, MinZoom - 1, Pick{Styles: Shade}, false},
		{"the widest zoom", chunks.Overworld, MinZoom, Pick{Styles: Tint}, true},
		{"the finest zoom", chunks.Overworld, MaxZoom, Pick{Styles: Tint}, true},
	} {
		m := roof
		if c.d == chunks.Overworld {
			m = overworld(t, -2, 18, func(x, z int32) int32 { return 100 })
		}
		tx := 0
		if c.zoom < 0 {
			tx = -1
		}
		_, ok := Tile(m, c.d, c.zoom, tx, tx, c.pick)
		if ok != c.there {
			t.Errorf("%s: a tile is %v, want %v", c.name, ok, c.there)
		}
	}
	if Offered(chunks.Nether) || !Offered(chunks.Overworld) || !Offered(chunks.End) {
		t.Error("topography is offered in the wrong dimensions")
	}
}

func TestNoTileWhereNothingIsKnownAndNoneBeforeTheHeightsAre(t *testing.T) {
	m := overworld(t, 0, 16, func(x, z int32) int32 { return 100 + x/3 })
	if _, ok := Tile(m, chunks.Overworld, 0, 40, 40, Pick{Styles: Shade}); ok {
		t.Error("a tile far from any chunk has a picture")
	}
	if _, ok := Tile(nil, chunks.Overworld, 0, 0, 0, Pick{Styles: Shade}); ok {
		t.Error("a tile is drawn from heights that are not there")
	}
	// A tile that holds ground and only level ground is a picture.
	flat := overworld(t, 0, 16, func(x, z int32) int32 { return 100 })
	if _, ok := Tile(flat, chunks.Overworld, 0, 0, 0, Pick{Styles: Shade}); !ok {
		t.Error("a tile of level ground has no picture")
	}
}

// The End has islands over a void. Its heights are surveyed with those of
// the other two, and its tile is drawn, with nothing over the void.
func TestTheEndDrawsItsIslandsOverTheVoid(t *testing.T) {
	m := world(t, chunks.End, -4, 18, func(x, z int32) (int32, bool) {
		r := math.Hypot(float64(x-20), float64(z-20))
		if r > 40 {
			return 0, false
		}
		return 60 + int32(r/8), true
	})
	img := picture(t, m, chunks.End, 0, 0, 0, Shade|Contours|Tint)
	if got := img.NRGBAAt(20, 20); got.A == 0 {
		t.Errorf("the island is not drawn: %v", got)
	}
	if got := img.NRGBAAt(150, 150); got.A != 0 {
		t.Errorf("the void is drawn %v", got)
	}
}

func TestAPNGHasOnlyTheColoursItUses(t *testing.T) {
	flat := overworld(t, -2, 18, func(x, z int32) int32 { return 62 })
	data, _ := Tile(flat, chunks.Overworld, 0, 0, 0, Pick{Styles: Shade | Contours | Tint})
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := cfg.ColorModel.(color.Palette); !ok || len(p) > 2 {
		t.Errorf("a tile of one colour has a palette of %v", cfg.ColorModel)
	}
	hilly := overworld(t, -2, 18, func(x, z int32) int32 { return 90 + (x*x+z*z)/700 })
	data, _ = Tile(hilly, chunks.Overworld, 0, 0, 0, Pick{Styles: Shade | Contours | Tint})
	if cfg, _ = png.DecodeConfig(bytes.NewReader(data)); len(cfg.ColorModel.(color.Palette)) > 256 {
		t.Error("a palette is past 256 colours")
	}
}

func BenchmarkTile(b *testing.B) {
	m := overworld(b, -40, 40, func(x, z int32) int32 {
		fx, fz := float64(x), float64(z)
		return 90 + int32(25*math.Sin(fx/23)+18*math.Cos(fz/17)+9*math.Sin((fx+fz)/6)) + (x*7+z*13)%3
	})
	for _, c := range []struct {
		name   string
		styles Styles
	}{{"shade", Shade}, {"contours", Contours}, {"tint", Tint}, {"all", Shade | Contours | Tint}} {
		for zoom := MaxZoom; zoom >= MinZoom; zoom-- {
			b.Run(fmt.Sprintf("%s/zoom%d", c.name, zoom), func(b *testing.B) {
				var size int
				for range b.N {
					data, ok := Tile(m, chunks.Overworld, zoom, 0, 0, Pick{Styles: c.styles})
					if !ok {
						b.Fatal("no tile")
					}
					size = len(data)
				}
				b.ReportMetric(float64(size), "bytes/tile")
			})
		}
	}
}
