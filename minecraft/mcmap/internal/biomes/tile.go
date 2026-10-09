package biomes

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// TileSize is a tile's width and height in pixels, as the terrain's is.
const TileSize = 256

// dimmed is what every other biome is drawn as while one is picked out:
// dark enough to push the terrain under it back, clear enough to read it.
var dimmed = color.NRGBA{A: 150}

// Pick says which biomes a tile is drawn with. The zero value is all of
// them.
type Pick struct {
	// Only keeps one biome its colour and dims every other: one picked out
	// against the rest.
	Only *uint32
	// IDs are the biomes drawn, and nothing else is; or with Except, the
	// biomes left out, which are left clear as ungenerated ground is.
	IDs    []uint32
	Except bool
}

// colour is what a biome is drawn in under the pick.
func (p Pick) colour(id uint32) color.Color {
	if p.Only != nil {
		if *p.Only != id {
			return dimmed
		}
	} else if len(p.IDs) > 0 || p.Except {
		listed := false
		for _, other := range p.IDs {
			if other == id {
				listed = true
				break
			}
		}
		if listed == p.Except {
			return color.NRGBA{}
		}
	}
	rgb := rgbOf(id)
	return color.NRGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
}

// Tile draws the biomes of one map tile as a PNG, addressed exactly as the
// terrain tiles are: at zoom 0 a pixel is a block and tile x, y starts at
// block x*256, z y*256; each zoom below doubles the blocks to a pixel, and
// each above halves them. A pixel is the biome of the column under its
// middle. Chunks the world has not generated are left clear.
//
// With only set, that biome keeps its colour and every other is dimmed.
// A tile with no generated chunk in it has no picture, and ok is false.
func (w *World) Tile(d chunks.Dimension, zoom, tx, ty int, only *uint32) (data []byte, ok bool) {
	return w.TilePicked(d, zoom, tx, ty, Pick{Only: only})
}

// TilePicked is Tile with any pick of biomes. A tile whose chunks hold
// only biomes the pick leaves out is still a picture, a clear one: the
// ground there is generated, and saying so keeps the page from asking
// again as it would for a tile that is not there.
func (w *World) TilePicked(d chunks.Dimension, zoom, tx, ty int, pick Pick) (data []byte, ok bool) {
	l := w.layer(d)
	if l == nil || len(l.cells) == 0 {
		return nil, false
	}
	// Entry 0 is clear; a kind is one more than its byte.
	palette := make(color.Palette, 1, len(l.ids)+1)
	palette[0] = color.NRGBA{}
	for _, id := range l.ids {
		palette = append(palette, pick.colour(id))
	}
	img := image.NewPaletted(image.Rect(0, 0, TileSize, TileSize), palette)

	blocks := math.Ldexp(1, -zoom)
	column := func(tile, pixel int) (int32, bool) {
		v := math.Floor((float64(tile)*TileSize + float64(pixel) + 0.5) * blocks)
		return int32(v), math.Abs(v) <= maxChunkCoordinate*16
	}
	var xs [TileSize]int32
	var inX [TileSize]bool
	for px := range xs {
		xs[px], inX[px] = column(tx, px)
	}
	drawn := false
	// Neighbouring pixels are mostly in one chunk, which is then looked
	// up once.
	lastKey, lastRef, lastHeld := uint64(0), uint32(0), false
	have := false
	for py := range TileSize {
		z, inZ := column(ty, py)
		if !inZ {
			continue
		}
		row := img.Pix[py*img.Stride:]
		for px, x := range xs {
			if !inX[px] {
				continue
			}
			if key := cellKey(x>>4, z>>4); !have || key != lastKey {
				lastKey, have = key, true
				lastRef, lastHeld = l.cells[key]
			}
			if !lastHeld {
				continue
			}
			kind := uint8(lastRef)
			if lastRef&mixedFlag != 0 {
				kind = l.mixed[int(lastRef&^mixedFlag)*Columns+int(z&15)*16+int(x&15)]
			}
			row[px] = kind + 1
			drawn = true
		}
	}
	if !drawn {
		return nil, false
	}
	var out bytes.Buffer
	// These are flat areas of a few colours, which the fastest setting
	// already packs to a kilobyte or two.
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, img); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}
