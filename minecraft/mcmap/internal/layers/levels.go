package layers

import (
	"strconv"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// A Level is a cut the page offers, and how much of the world under it a
// tile looks at.
type Level struct {
	// ID is what a request names the level by: the height of the cut. It is
	// the same as the id of the Nether's own levels, which are cuts of the
	// same heights.
	ID    string
	Y     int
	Label string
	// Band is the thickness looked at, in blocks, where it is not all of
	// what is under the cut, which is what it is for 0.
	Band int
}

// Bands a request may ask for in place of a level's own, in blocks; all of
// what is under the cut is "all". Few, so that the cache holds a few
// pictures of each tile and not one for every number a request can write.
var Bands = []int{16, 32, 64}

// The overworld's levels: sea level, which shows the surface, then bands
// of sixteen down through the stone to the deep dark, which is what a
// slice of the world is in a sub-chunk.
var overworldLevels = []Level{
	{ID: "62", Y: 62, Label: "Sea level", Band: DefaultThickness},
	{ID: "40", Y: 40, Label: "40", Band: DefaultThickness},
	{ID: "16", Y: 16, Label: "16", Band: DefaultThickness},
	{ID: "0", Y: 0, Label: "0", Band: DefaultThickness},
	{ID: "-16", Y: -16, Label: "-16", Band: DefaultThickness},
	{ID: "-32", Y: -32, Label: "-32", Band: DefaultThickness},
	{ID: "-48", Y: -48, Label: "-48", Band: DefaultThickness},
}

// NetherCut is a cut of the Nether as it is configured, with the name it
// goes by.
type NetherCut struct {
	Y     int
	Label string
}

// LevelsOf is the cuts a dimension is offered at. The Nether's are the ones
// configured, since the levels of its map and of its cave view are one
// thing, and each looks at all of what is under it: it has several floors
// in a column and the cut says which of them is the first seen. The End has
// one island layer and so one cut.
func LevelsOf(d chunks.Dimension, nether []NetherCut) []Level {
	switch d {
	case chunks.Overworld:
		return append([]Level(nil), overworldLevels...)
	case chunks.Nether:
		out := make([]Level, 0, len(nether))
		for _, c := range nether {
			out = append(out, Level{ID: strconv.Itoa(c.Y), Y: c.Y, Label: c.Label})
		}
		return out
	case chunks.End:
		return []Level{{ID: strconv.Itoa(MaxY(chunks.End)), Y: MaxY(chunks.End), Label: "All"}}
	}
	return nil
}

// AllBelow, as a band, is all of what is under the cut.
const AllBelow = -1

// SpecOf is the cut that a level, a style and a band come to. A band of 0 is
// the level's own, and AllBelow is all of what is under the cut.
func SpecOf(d chunks.Dimension, l Level, style Style, band int) Spec {
	if band == 0 {
		band = l.Band
	}
	if band == 0 || band == AllBelow {
		band = l.Y - MinY(d) + 1
	}
	return Spec{Dim: d, Cut: l.Y, Thickness: band, Style: style}
}
