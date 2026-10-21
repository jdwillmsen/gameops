package work

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
)

const (
	// A palette index is one byte, so a wider palette is not an observation.
	maxPaletteEntries = 256
	// The unit's own template caps a box at this many cells, so a bigger
	// claim is not decoded, whatever the report says.
	maxBlocksCells = 4096
)

// Blocks is the box of the world a unit observed: the cells run-length coded
// against a palette of block names, in the shape the unit sends.
type Blocks struct {
	Min     [3]int32
	Size    [3]int32
	Palette []string
	Cells   string
}

// parseBlocks returns false for anything that is not a well-formed
// observation. The unit reports at most one box of at most 4096 cells whose
// names it can encode; anything else is dropped rather than failing the
// state report that carries it.
func parseBlocks(raw json.RawMessage) (Blocks, bool) {
	var b Blocks
	if len(raw) == 0 || string(raw) == "null" {
		return b, false
	}
	// Pointers so a missing coordinate is told apart from zero: a box at
	// the origin is a place, an absent corner is not an observation.
	var w struct {
		Min *struct {
			X, Y, Z *int32
		} `json:"min"`
		Size *struct {
			X, Y, Z *int32
		} `json:"size"`
		Palette []string `json:"palette"`
		Cells   string   `json:"cells"`
	}
	if decodeStrict(bytes.NewReader(raw), &w) != nil {
		return Blocks{}, false
	}
	if w.Min == nil || w.Size == nil || w.Min.X == nil || w.Min.Y == nil || w.Min.Z == nil ||
		w.Size.X == nil || w.Size.Y == nil || w.Size.Z == nil ||
		len(w.Palette) < 1 || len(w.Palette) > maxPaletteEntries {
		return Blocks{}, false
	}
	for _, name := range w.Palette {
		if name != "unknown" && !seenItemName.MatchString(name) {
			return Blocks{}, false
		}
	}
	var cells uint64 = 1
	for _, ax := range [3]*int32{w.Size.X, w.Size.Y, w.Size.Z} {
		// Each axis is capped first: three near-int32 spans would otherwise
		// wrap the product past the cap.
		if *ax < 1 || *ax > maxBlocksCells {
			return Blocks{}, false
		}
		cells *= uint64(*ax)
	}
	if cells > maxBlocksCells {
		return Blocks{}, false
	}
	stream, err := base64.StdEncoding.DecodeString(w.Cells)
	if err != nil {
		return Blocks{}, false
	}
	// The stream is walked, not expanded: this only decides whether it is
	// the box it claims, so a hostile report buys no large allocation.
	var total uint64
	for len(stream) > 0 {
		run, n := binary.Uvarint(stream)
		// The index is compared as an int: a palette of 256 entries is a
		// valid one, and a byte of its length would wrap to zero and
		// refuse every pair.
		if n <= 0 || run == 0 || n >= len(stream) || int(stream[n]) >= len(w.Palette) {
			return Blocks{}, false
		}
		if run > cells-total {
			return Blocks{}, false
		}
		total += run
		stream = stream[n+1:]
	}
	if total != cells {
		return Blocks{}, false
	}
	b.Min = [3]int32{*w.Min.X, *w.Min.Y, *w.Min.Z}
	b.Size = [3]int32{*w.Size.X, *w.Size.Y, *w.Size.Z}
	b.Palette, b.Cells = w.Palette, w.Cells
	return b, true
}
