// Package topo draws the shape of the land as an overlay for the map: the
// light and shadow of its slopes, contour lines, and a tint by height. It
// draws from the surface heights alone, the game's own heightmap, which is
// the top of a tree, of ice or of the sea and not the ground under them,
// so it shows no more than the terrain tiles already do.
package topo

import (
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

const (
	// TileSize is a tile's width and height in pixels, as the terrain's is.
	TileSize = heights.TileSize
	// MaxZoom is the finest tile there is: 16 pixels to a block, where the
	// terrain's stop at one. Past a block to a pixel the land is not cut
	// into blocks but drawn smooth between their middles, so that a line
	// is a line and not a staircase of squares.
	MaxZoom = 4
	// MinZoom is the widest: a pixel is 32 blocks. Past it a tile covers
	// more than a world explored on foot, and what a slope comes to is
	// which side of a continent a tree stands on.
	MinZoom = -5
)

// Styles is which of the three drawings a tile carries.
type Styles uint8

const (
	// Shade is the light and shadow of the slopes.
	Shade Styles = 1 << iota
	// Contours are lines of equal height.
	Contours
	// Tint colours the ground by its height.
	Tint

	allStyles = Shade | Contours | Tint
)

// Pick says what a tile is drawn with.
type Pick struct {
	Styles Styles
	// Hidden is the bands of the tint that are left out, one bit each from
	// the lowest: they are drawn as the terrain under them is. It matters
	// only to a tile that has a tint.
	Hidden uint32
}

// Valid says it is some of the styles and nothing else.
func (s Styles) Valid() bool { return s != 0 && s&^allStyles == 0 }

// Offered says a dimension has land to draw. The heightmap of the Nether
// is the bedrock roof over the whole of it, so there is nothing to see
// there; the End has its islands over the void.
func Offered(d chunks.Dimension) bool { return d == chunks.Overworld || d == chunks.End }

// intervals is the height between two contour lines by how many zooms out
// a tile is. A pixel is twice as wide a zoom out, so a slope is half as
// many pixels across; the interval roughly doubles to keep lines about as
// far apart on the screen, in steps that read as numbers.
var intervals = [-MinZoom + 1]int{5, 10, 20, 50, 100, 200}

// HeavyEvery is how many contour lines there are to a heavy one.
const HeavyEvery = 5

// Interval is the height between two contour lines in tiles of a zoom. A
// tile nearer than a block to a pixel has the interval of one at a block.
func Interval(zoom int) int { return intervals[-min(max(zoom, MinZoom), 0)] }

// A Band is a range of surface heights drawn in one colour of the tint.
type Band struct {
	// From and To are the lowest and highest surface y of the band,
	// inclusive. The first has no lower end and the last no upper, which
	// are set to the limits of the heights.
	From int `json:"from"`
	To   int `json:"to"`
	// Colour is #rrggbb, drawn over the terrain at a fraction of its
	// strength; or empty for a band that is not drawn at all.
	Colour string `json:"colour"`
	// Label says what the band is where the range is not enough.
	Label string `json:"label,omitempty"`

	rgb   [3]uint8
	alpha float64
}

const (
	lowest  = -1 << 15
	highest = 1<<15 - 1
)

func band(from, to int, label string, r, g, b uint8, alpha float64) Band {
	out := Band{From: from, To: to, Label: label, rgb: [3]uint8{r, g, b}, alpha: alpha}
	if alpha > 0 {
		out.Colour = fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	return out
}

// The sea is flat at 62 and is most of an ocean world, so its band has no
// tint: the terrain's own water is left as it is. The rest follow the
// ground as a map's tints do, from green near the sea through yellow and
// brown to the white of the peaks.
var overworldBands = []Band{
	band(lowest, 61, "", 92, 84, 156, 0.45),
	band(62, 62, "sea level", 0, 0, 0, 0),
	band(63, 69, "", 150, 198, 96, 0.38),
	band(70, 89, "", 232, 214, 112, 0.38),
	band(90, 109, "", 222, 168, 84, 0.42),
	band(110, 139, "", 190, 112, 70, 0.45),
	band(140, 179, "", 150, 128, 136, 0.5),
	band(180, highest, "", 246, 246, 250, 0.55),
}

// The main island of the End lies between 56 and 63 with a few blocks of
// rise and fall, and its far islands and the pillars stand above.
var endBands = []Band{
	band(lowest, 51, "", 92, 84, 156, 0.45),
	band(52, 55, "", 150, 198, 96, 0.38),
	band(56, 59, "", 232, 214, 112, 0.38),
	band(60, 63, "", 222, 168, 84, 0.42),
	band(64, 79, "", 190, 112, 70, 0.45),
	band(80, 119, "", 150, 128, 136, 0.5),
	band(120, highest, "", 246, 246, 250, 0.55),
}

// The Nether's floors lie between the bedrock at the bottom and the roof
// over its caverns: the lava sea at 31 with its shores just over it, then
// the floors of the middle, and the high ones under the roof.
var netherBands = []Band{
	band(lowest, 30, "", 92, 84, 156, 0.45),
	band(31, 31, "lava sea level", 0, 0, 0, 0),
	band(32, 45, "", 150, 198, 96, 0.38),
	band(46, 69, "", 232, 214, 112, 0.38),
	band(70, 89, "", 222, 168, 84, 0.42),
	band(90, 109, "", 190, 112, 70, 0.45),
	band(110, highest, "", 246, 246, 250, 0.55),
}

// Bands are the tint's colours in a dimension, lowest first, which is
// what its legend says. It is none in a dimension that is not offered,
// and the Nether's are for the floors of a level and not for Tile.
func Bands(d chunks.Dimension) []Band {
	switch d {
	case chunks.Overworld:
		return overworldBands
	case chunks.End:
		return endBands
	case chunks.Nether:
		return netherBands
	}
	return nil
}
