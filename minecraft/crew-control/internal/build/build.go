// Package build checks what a unit observed of a box of the world against
// the blueprint the work was meant to realise.
package build

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

// maxCells matches the unit's cap on a box, so a decoded grid is bounded
// however its observation arrived.
const maxCells = 4096

// Unknown is the name a unit gives a cell it could not read.
const Unknown = "unknown"

// Grid is a decoded observation: one palette index per cell, the cells
// ordered x fastest, then z, then y.
type Grid struct {
	Size    [3]int
	Palette []string
	Cells   []byte
}

// Decode expands the run-length coding of an observation into its cells. The
// report was validated on the way in; the checks here keep a hand-built one
// from panicking rather than re-establishing the wire contract.
func Decode(b work.Blocks) (Grid, error) {
	var size [3]int
	cells := 1
	for i, ax := range [3]int32{b.Size[0], b.Size[1], b.Size[2]} {
		// Each axis is capped first: three near-int32 spans would otherwise
		// wrap the product past the cap.
		if ax < 1 || ax > maxCells {
			return Grid{}, fmt.Errorf("build: size axis %d is %d, want 1 to %d", i, ax, maxCells)
		}
		size[i] = int(ax)
		cells *= int(ax)
	}
	if cells > maxCells {
		return Grid{}, fmt.Errorf("build: %d cells is over the %d a unit observes", cells, maxCells)
	}
	if len(b.Palette) == 0 {
		return Grid{}, errors.New("build: the palette is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(b.Cells)
	if err != nil {
		return Grid{}, fmt.Errorf("build: the cells are not base64: %w", err)
	}
	g := Grid{Size: size, Palette: b.Palette, Cells: make([]byte, 0, cells)}
	for len(raw) > 0 {
		run, n := binary.Uvarint(raw)
		// n >= len(raw) would leave the pair without its palette index, and
		// run == 0 would loop without filling anything.
		if n <= 0 || run == 0 || n >= len(raw) {
			return Grid{}, errors.New("build: the cell stream is truncated or malformed")
		}
		idx := raw[n]
		if int(idx) >= len(b.Palette) {
			return Grid{}, fmt.Errorf("build: palette index %d is outside the palette", idx)
		}
		if run > uint64(cells-len(g.Cells)) {
			return Grid{}, errors.New("build: the cell stream holds more cells than the box")
		}
		for range run {
			g.Cells = append(g.Cells, idx)
		}
		raw = raw[n+1:]
	}
	if len(g.Cells) != cells {
		return Grid{}, errors.New("build: the cell stream does not fill the box")
	}
	return g, nil
}

// Compare counts how an observed grid stands against the expected one. A
// size difference is a failed comparison with nothing to count against. An
// observed unknown counts as a mismatch unless the expected cell is air: the
// server never sends a sub-chunk it holds entirely air, so the open half of
// a build reads back unknown, and counting that would fail every sound
// build. ok is mismatches within tolerance of the cell count.
func Compare(expected, observed Grid, tolerance float64) (ok bool, mismatches, unknown int) {
	if expected.Size != observed.Size {
		return false, 0, 0
	}
	cells := 1
	for _, ax := range expected.Size {
		cells *= ax
	}
	for i := range expected.Cells {
		want := name(expected, i)
		got := name(observed, i)
		if got == Unknown {
			unknown++
			if want == "air" {
				continue
			}
		}
		if want != got {
			mismatches++
		}
	}
	return float64(mismatches) <= tolerance*float64(cells), mismatches, unknown
}

// name is the block a grid's cell holds, or the empty name when the index is
// outside the grid, which only a hand-built grid can be.
func name(g Grid, i int) string {
	if i >= len(g.Cells) || int(g.Cells[i]) >= len(g.Palette) {
		return ""
	}
	return g.Palette[g.Cells[i]]
}
