package layers

import (
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Style is which block of a column a cut shows.
type Style uint8

const (
	// Cut shows the highest block at or below the cut that is not air or
	// a plant: the world with everything above the cut taken away. Rock
	// shows as rock and a cave shows as the floor it opens onto, deeper and
	// so darker.
	Cut Style = iota
	// Floor shows the ground of the highest enclosed space at or below the
	// cut: the highest air with something to stand on and a roof somewhere
	// over it. Solid rock with no space in the band is drawn dim, so that
	// the spaces are what stands out. A column open to the sky shows as the
	// Cut would.
	Floor
)

// Spec says what a cut of a dimension is.
type Spec struct {
	Dim chunks.Dimension
	// Cut is the highest y drawn.
	Cut int
	// Thickness is how many blocks, counting the cut's, are looked at: the
	// band the cut shows. A column with nothing in it is left clear.
	Thickness int
	Style     Style
}

// DefaultThickness is the height of a sub-chunk, which is the band a cave
// view of a level is read in.
const DefaultThickness = 16

// Valid says a cut is inside its dimension and its band is a reasonable one.
func (s Spec) Valid() error {
	if _, _, ok := span(s.Dim); !ok {
		return fmt.Errorf("not a dimension of the world")
	}
	if s.Cut < MinY(s.Dim) || s.Cut > MaxY(s.Dim) {
		return fmt.Errorf("cut %d is outside %s, which is %d to %d", s.Cut, s.Dim.Name(), MinY(s.Dim), MaxY(s.Dim))
	}
	if s.Thickness < 1 || s.Thickness > MaxY(s.Dim)-MinY(s.Dim)+1 {
		return fmt.Errorf("thickness %d is not a band of %s", s.Thickness, s.Dim.Name())
	}
	if s.Style != Cut && s.Style != Floor {
		return fmt.Errorf("unknown style")
	}
	return nil
}

func (s Spec) bottom() int { return max(s.Cut-s.Thickness+1, MinY(s.Dim)) }

// Kind says what a column came to.
type Kind uint8

const (
	// Nothing is in the column's band at all.
	Nothing Kind = iota
	// Ground is a block seen from above.
	Ground
	// Mass is rock with no space in the band, in the Floor style.
	Mass
)

// Sight is what one column shows.
type Sight struct {
	Kind Kind
	// Y is the top of the block seen. ID is the block, and Bed the block
	// under it where it is water, which is drawn over it, Depth blocks of
	// water deep.
	Y     int
	ID    ID
	Bed   ID
	Depth int
	Wet   bool
}

// see works out what the column at x, z of c shows under spec.
func (c *Column) see(blocks []Block, x, z int, spec Spec) Sight {
	bottom := spec.bottom()
	if spec.Style == Floor {
		if s, found := c.floor(blocks, x, z, spec.Cut, bottom); found {
			return s
		}
		// No floor in the band. With nothing over the band either this is
		// open ground and is drawn as such; with something over it, it is
		// rock between the spaces.
		s := c.top(blocks, x, z, spec.Cut, bottom)
		if s.Kind == Ground && c.roofedAbove(blocks, x, z, s.Y) {
			s.Kind = Mass
		}
		return s
	}
	return c.top(blocks, x, z, spec.Cut, bottom)
}

// quiet says the slice that y is in has nothing in it that a view from
// above cares for, and so it can be stepped over whole.
func (c *Column) quiet(blocks []Block, y int) bool {
	s := y>>4 - c.lowest
	if s < 0 || s >= c.count {
		return true
	}
	sl := &c.slots[s]
	if !sl.present {
		return true
	}
	return sl.uniform && blocks[sl.id].Flags&(Air|Plant) != 0
}

// top is the highest block from y down to bottom that is neither air nor a
// plant.
func (c *Column) top(blocks []Block, x, z, y, bottom int) Sight {
	for y >= bottom {
		if c.quiet(blocks, y) {
			y = y>>4<<4 - 1
			continue
		}
		id := c.ID(x, y, z)
		b := &blocks[id]
		switch {
		case b.Flags&(Air|Plant) != 0:
			y--
			continue
		case b.Flags&Water != 0:
			depth, bed := 1, AirID
			for yy := y - 1; yy >= bottom; yy-- {
				below := c.ID(x, yy, z)
				f := blocks[below].Flags
				if f&Water != 0 {
					depth++
					continue
				}
				if f&(Air|Plant) != 0 {
					continue
				}
				bed = below
				break
			}
			return Sight{Kind: Ground, Y: y, ID: id, Bed: bed, Depth: depth}
		}
		return Sight{Kind: Ground, Y: y, ID: id, Wet: c.Wet(x, y, z)}
	}
	return Sight{}
}

// floor is the highest place at or below y, down to bottom, where there is
// air to stand in over a block to stand on and something solid somewhere
// over it.
func (c *Column) floor(blocks []Block, x, z, y, bottom int) (Sight, bool) {
	roof := c.roof(blocks, x, z)
	y = min(y, roof-1)
	for ; y >= bottom; y-- {
		// In a slice of nothing the only place that can be a floor is its
		// lowest, over the slice below.
		if y&15 != 0 && c.quiet(blocks, y) {
			y = y >> 4 << 4
			if y < bottom {
				break
			}
		}
		if blocks[c.ID(x, y, z)].Flags&Passable == 0 {
			continue
		}
		under := c.ID(x, y-1, z)
		f := blocks[under].Flags
		if f&Passable != 0 {
			continue
		}
		if f&Water != 0 {
			// A flooded floor is the water, drawn over what it floods.
			s := c.top(blocks, x, z, y-1, max(bottom-1, MinY(c.Pos.Dim)))
			return s, true
		}
		return Sight{Kind: Ground, Y: y - 1, ID: under, Wet: c.Wet(x, y-1, z)}, true
	}
	return Sight{}, false
}

// roof is the y of the highest solid block of the column, or the lowest
// possible y when it has none: nothing is enclosed at or above it.
func (c *Column) roof(blocks []Block, x, z int) int {
	for y := MaxY(c.Pos.Dim); y >= MinY(c.Pos.Dim); y-- {
		if c.quiet(blocks, y) {
			y = y >> 4 << 4
			continue
		}
		if blocks[c.ID(x, y, z)].Solid() {
			return y
		}
	}
	return MinY(c.Pos.Dim)
}

func (c *Column) roofedAbove(blocks []Block, x, z, y int) bool {
	return c.roof(blocks, x, z) > y
}
