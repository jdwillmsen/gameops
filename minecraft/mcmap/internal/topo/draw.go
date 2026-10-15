package topo

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// Light comes from the north-west and 45 degrees above the horizon, as
// maps have always been lit. These are the direction to the light in
// east, north and up.
var (
	lightEast  = -math.Sqrt2 / 2 * math.Cos(math.Pi/4)
	lightNorth = math.Sqrt2 / 2 * math.Cos(math.Pi/4)
	lightUp    = math.Sin(math.Pi / 4)
)

const (
	// The light on a slope is drawn by how far it is from the light on
	// flat ground, which is the most a slope can darken and brighten:
	// facing away from the light at 45 degrees is a departure of -0.7, and
	// toward it of 0.3. A slope past these is drawn no more so.
	darkest   = 0.70
	brightest = 0.30
	// How strongly the extremes are drawn. The terrain has relief of its
	// own, and this only adds to it, more gently than it would take away.
	darkAlpha  = 0.42
	lightAlpha = 0.18
	// maxShadeLevels is the most steps of shade either side of flat. More
	// are a smoother picture and a PNG several times the size.
	maxShadeLevels = 4
)

var (
	darkRGB  = [3]uint8{18, 20, 44}
	lightRGB = [3]uint8{255, 250, 232}
	lineRGB  = [3]uint8{58, 36, 16}
)

const (
	minorAlpha = 0.5
	heavyAlpha = 0.9
)

// plan is every colour a tile of some styles in some dimension can use, by
// code: the band of the tint, the level of the shade and whether there is
// a line, in that order of significance.
type plan struct {
	styles   Styles
	bands    []Band
	levels   int
	lines    int
	colours  []color.NRGBA
	bandOf   [1024]uint8
	bandBase int
}

func (p *plan) code(band, level, line int) int { return (band*p.levels+level)*p.lines + line }

// planFor works out the colours of a set of styles. The palette of a PNG
// holds 256, so the steps of shade are as many as leave room for the other
// two.
func planFor(d chunks.Dimension, styles Styles, hidden uint32) *plan {
	p := &plan{styles: styles, bands: []Band{{}}, levels: 1, lines: 1}
	if styles&Tint != 0 {
		p.bands = Bands(d)
	}
	if styles&Contours != 0 {
		p.lines = 3
	}
	if styles&Shade != 0 {
		// Odd, so that level ground is a level of its own with no shade.
		room := 255 / (len(p.bands) * p.lines)
		if room%2 == 0 {
			room--
		}
		p.levels = min(room, 2*maxShadeLevels+1)
	}
	p.colours = make([]color.NRGBA, len(p.bands)*p.levels*p.lines)
	half := p.levels / 2
	for b := range p.bands {
		for l := range p.levels {
			for line := range p.lines {
				var over [3]layer
				n := 0
				if styles&Tint != 0 {
					if hidden>>b&1 == 0 {
						over[n] = layer{p.bands[b].rgb, p.bands[b].alpha}
						n++
					}
				}
				if k := l - half; k < 0 {
					over[n] = layer{darkRGB, darkAlpha * float64(-k) / float64(half)}
					n++
				} else if k > 0 {
					over[n] = layer{lightRGB, lightAlpha * float64(k) / float64(half)}
					n++
				}
				switch line {
				case 1:
					over[n] = layer{lineRGB, minorAlpha}
					n++
				case 2:
					over[n] = layer{lineRGB, heavyAlpha}
					n++
				}
				p.colours[p.code(b, l, line)] = composite(over[:n])
			}
		}
	}
	// The tint's band by height, from the lowest y a column can have.
	p.bandBase = -128
	for i := range p.bandOf {
		y := p.bandBase + i
		for b := range p.bands {
			if y <= p.bands[b].To || b == len(p.bands)-1 {
				p.bandOf[i] = uint8(b)
				break
			}
		}
	}
	return p
}

type layer struct {
	rgb   [3]uint8
	alpha float64
}

// composite lays colours one over the next, the first at the bottom.
func composite(over []layer) color.NRGBA {
	var r, g, b, a float64
	for _, l := range over {
		r = float64(l.rgb[0])*l.alpha + r*(1-l.alpha)
		g = float64(l.rgb[1])*l.alpha + g*(1-l.alpha)
		b = float64(l.rgb[2])*l.alpha + b*(1-l.alpha)
		a = l.alpha + a*(1-l.alpha)
	}
	if a == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{R: uint8(r/a + 0.5), G: uint8(g/a + 0.5), B: uint8(b/a + 0.5), A: uint8(a*255 + 0.5)}
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Tile draws one tile of the overlay as a PNG, addressed exactly as the
// terrain's and the biomes' tiles are: at zoom 0 a pixel is a block and
// tile x, y starts at block x*256, z y*256; each zoom below doubles the
// blocks to a pixel. Ground that has no surface is left clear. A tile
// with no surface in it has no picture, and ok is false, as it is for a
// dimension that is not offered, a zoom there are no tiles at, and styles
// that are none.
func Tile(m *heights.Map, d chunks.Dimension, zoom, tx, ty int, pick Pick) (data []byte, ok bool) {
	if !Offered(d) {
		return nil, false
	}
	return draw(m, d, zoom, tx, ty, pick)
}

// FloorTile is Tile for a map of the floors under a cut, which the Nether
// has one of for each level, since its own heightmap is the roof. The
// caller vouches that m is such a map: nothing in it says so.
func FloorTile(m *heights.Map, d chunks.Dimension, zoom, tx, ty int, pick Pick) (data []byte, ok bool) {
	if d != chunks.Nether {
		return nil, false
	}
	return draw(m, d, zoom, tx, ty, pick)
}

func draw(m *heights.Map, d chunks.Dimension, zoom, tx, ty int, pick Pick) (data []byte, ok bool) {
	if !pick.Styles.Valid() || zoom < MinZoom || zoom > MaxZoom {
		return nil, false
	}
	pl := planFor(d, pick.Styles, pick.Hidden)
	zp := planOf(zoom)
	ring := zp.ring()
	side := TileSize + 2*ring
	w := works.Get().(*work)
	defer works.Put(w)
	w.raw, w.soft = grow(w.raw, side*side), grow(w.soft, side*side)
	if zp.scale > 1 {
		if !w.upsample(m, d, zp, tx, ty, ring, w.soft) {
			return nil, false
		}
	} else {
		if !w.sample(m, d, zp, tx, ty, ring, w.raw) {
			return nil, false
		}
		w.smooth(w.raw, w.soft, side, zp.smooth)
	}
	soft := w.soft

	interval := Interval(zoom)
	img := image.NewPaletted(image.Rect(0, 0, TileSize, TileSize), nil)
	// Codes are given palette entries as they are first drawn, so a tile
	// holds only the colours it uses and is packed to as few bits as that
	// takes.
	index := make([]int16, len(pl.colours))
	for i := range index {
		index[i] = -1
	}
	img.Palette = append(img.Palette, color.NRGBA{})

	half := pl.levels / 2
	// A step of shade is this much of departure from flat light.
	darkStep, lightStep := darkest/float64(max(half, 1)), brightest/float64(max(half, 1))
	run := zp.relief / (2 * zp.pixel() * fine)
	for py := range TileSize {
		row := img.Pix[py*img.Stride:][:TileSize]
		for px := range TileSize {
			at := (py+ring)*side + px + ring
			h := soft[at]
			if h == heights.Unknown {
				continue
			}
			bandIx, level, line := 0, half, 0
			if pl.styles&Tint != 0 {
				bandIx = int(pl.bandOf[min(max(floorDiv(int(h), fine)-pl.bandBase, 0), len(pl.bandOf)-1)])
			}
			if pl.styles&Shade != 0 {
				// A neighbour with no surface is not a cliff: the slope is
				// taken from the one side that is there.
				e, w, n, s := soft[at+1], soft[at-1], soft[at-side], soft[at+side]
				gx := slope(h, w, e) * run
				gz := slope(h, n, s) * run
				// Heights rise to the east with gx and to the south with
				// gz; the surface faces where it falls.
				lit := (-gx*lightEast + gz*lightNorth + lightUp) / math.Sqrt(1+gx*gx+gz*gz)
				delta := lit - lightUp
				if delta < 0 {
					level = half - min(int(-delta/darkStep+0.5), half)
				} else {
					level = half + min(int(delta/lightStep+0.5), half)
				}
			}
			if pl.styles&Contours != 0 {
				line = contour(soft, at, side, int(h), interval*fine)
			}
			c := pl.code(bandIx, level, line)
			if index[c] < 0 {
				index[c] = int16(len(img.Palette))
				img.Palette = append(img.Palette, pl.colours[c])
			}
			row[px] = uint8(index[c])
		}
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, img); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

// slope is the rise from one side of a pixel to the other, over two pixels
// where both are there and over one where only one is.
func slope(mid, before, after int16) float64 {
	switch {
	case before != heights.Unknown && after != heights.Unknown:
		return float64(after) - float64(before)
	case after != heights.Unknown:
		return 2 * (float64(after) - float64(mid))
	case before != heights.Unknown:
		return 2 * (float64(mid) - float64(before))
	}
	return 0
}

// contour says whether the pixel at is on a line: one of the levels that
// are multiples of interval lies between it and a lower neighbour, and it
// is the higher side of it. That is 1 for a line, 2 for a heavy one and 0
// for neither.
func contour(soft []int16, at, side, h, interval int) int {
	level := floorDiv(h, interval)
	lowest := level
	for _, other := range [...]int16{soft[at-1], soft[at+1], soft[at-side], soft[at+side]} {
		if other != heights.Unknown {
			lowest = min(lowest, floorDiv(int(other), interval))
		}
	}
	switch {
	case lowest == level:
		return 0
	case floorDiv(level, HeavyEvery) > floorDiv(lowest, HeavyEvery):
		return 2
	}
	return 1
}
