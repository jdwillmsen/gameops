package skill

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// UnknownBlock names a cell the unit could not read: one in a sub-chunk the
// server has not sent, or one whose hash the name table lacks.
const UnknownBlock = "unknown"

// maxPaletteEntries is the width of the index the wire format carries.
const maxPaletteEntries = 256

// maxBlocksBytes is the most one observation may take of the 1024-byte event
// it travels in, leaving room for that event's own id, state, progress and a
// reason clipped to 400 bytes.
const maxBlocksBytes = 470

// errTooLarge is the one answer the planner is told to expect: observe less,
// a layer or a smaller box, and the report will fit.
var errTooLarge = errors.New("observation too large")

// Vec is a point or an extent of a box, spelled the way the controller reads
// it on the wire.
type Vec struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

// Blocks is a box of cells as the unit saw them, in the shape the controller
// reads.
type Blocks struct {
	Min     Vec      `json:"min"`
	Size    Vec      `json:"size"`
	Palette []string `json:"palette"`
	// Cells is base64 of (run length uvarint, palette index byte) pairs, the
	// cells ordered x fastest, then z, then y.
	Cells string `json:"cells"`
}

// BlockReporter is implemented by skills that observe blocks, so the runner
// can pass the observation on with the task.
type BlockReporter interface {
	// Blocks is false until the box has been read.
	Blocks() (Blocks, bool)
}

// observeBlocks reads every cell of the inclusive box and encodes it for the
// wire. Whatever makes the report unable to travel in one event, too many
// cells that differ or a palette wider than its index, is too large.
func observeBlocks(w ChestWorld, min, max protocol.BlockPos) (Blocks, error) {
	size := Vec{max.X() - min.X() + 1, max.Y() - min.Y() + 1, max.Z() - min.Z() + 1}
	var (
		pal   []string
		index = map[string]byte{}
		cells = make([]byte, 0, int(size.X)*int(size.Y)*int(size.Z))
	)
	for y := min.Y(); y <= max.Y(); y++ {
		for z := min.Z(); z <= max.Z(); z++ {
			for x := min.X(); x <= max.X(); x++ {
				name, known := w.Block(protocol.BlockPos{x, y, z})
				if !known {
					name = UnknownBlock
				}
				// The store strips the prefix; trimming here keeps the
				// palette honest whatever world is wired in.
				name = strings.TrimPrefix(name, "minecraft:")
				i, ok := index[name]
				if !ok {
					if len(pal) == maxPaletteEntries {
						return Blocks{}, errTooLarge
					}
					i = byte(len(pal))
					index[name] = i
					pal = append(pal, name)
				}
				cells = append(cells, i)
			}
		}
	}
	var stream []byte
	for i := 0; i < len(cells); {
		run := 1
		for i+run < len(cells) && cells[i+run] == cells[i] {
			run++
		}
		stream = binary.AppendUvarint(stream, uint64(run))
		stream = append(stream, cells[i])
		i += run
	}
	b := Blocks{
		Min:     Vec{min.X(), min.Y(), min.Z()},
		Size:    size,
		Palette: pal,
		Cells:   base64.StdEncoding.EncodeToString(stream),
	}
	enc, err := json.Marshal(b)
	if err != nil {
		return Blocks{}, err
	}
	if len(enc) > maxBlocksBytes {
		return Blocks{}, errTooLarge
	}
	return b, nil
}
