package repair

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// BlockSource is a client's view of the world: the block at a cell and its
// states, and whether the client has been told about that cell at all.
type BlockSource interface {
	State(pos protocol.BlockPos) (name string, props map[string]any, known bool)
}

// BlocksReader serves blocks from what a unit has loaded. A cell the unit has
// not been sent stays Unknown, so it is never repaired.
type BlocksReader struct {
	src   BlockSource
	match *Matcher
}

// NewBlocksReader folds scanned blocks into the spellings plan uses, as the
// world-copy reader does.
func NewBlocksReader(src BlockSource, plan []Op) *BlocksReader {
	return &BlocksReader{src: src, match: NewMatcher(plan)}
}

func (r *BlocksReader) Read(b Box, pal *Palette) (*Grid, error) {
	g := NewGrid(b)
	for y := b.Y0; y <= b.Y1; y++ {
		for z := b.Z0; z <= b.Z1; z++ {
			i := g.index(b.X0, y, z)
			for x := b.X0; x <= b.X1; x++ {
				name, props, known := r.src.State(protocol.BlockPos{int32(x), int32(y), int32(z)})
				if known {
					g.Cells[i] = pal.ID(r.match.Canonical(name, RenderStates(props)))
				}
				i++
			}
		}
	}
	return g, nil
}
