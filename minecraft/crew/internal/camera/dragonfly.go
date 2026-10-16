package camera

import (
	"bytes"
	"sync"

	// Imported for the block states it registers; chunk decoding finds nothing
	// without them.
	_ "github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
)

const (
	// Overworld range: the only dimension the camera draws.
	minY, maxY = -64, 319
)

var finalizeRegistry sync.Once

type dragonflyDecoder struct{ rng cube.Range }

// NewDragonflyDecoder decodes with dragonfly's chunk code and block table.
// The table tracks a server version, so a server newer than it shows unknown
// blocks as placeholders.
func NewDragonflyDecoder() Decoder {
	finalizeRegistry.Do(world.DefaultBlockRegistry.Finalize)
	return dragonflyDecoder{rng: cube.Range{minY, maxY}}
}

func (d dragonflyDecoder) SubChunk(payload []byte) (Section, error) {
	// dragonfly only decodes sub-chunks inside a whole chunk, so the payload is
	// wrapped with a minimal biome section: one palette entry, then 23 "same
	// as previous" markers.
	buf := bytes.NewBuffer(append([]byte(nil), payload...))
	buf.Write([]byte{0x01, 0x00})
	buf.Write(bytes.Repeat([]byte{0xff}, 23))
	c, err := chunk.NetworkDecodeBuffer(world.DefaultBlockRegistry, buf, 1, d.rng)
	if err != nil {
		return Section{}, err
	}
	// The payload carries an absolute Y index, which dragonfly files at that
	// position in the chunk's range.
	for _, sub := range c.Sub() {
		if sub != nil && !sub.Empty() {
			return d.section(sub), nil
		}
	}
	return Section{Palette: []string{"air"}}, nil
}

func (d dragonflyDecoder) Chunk(payload []byte, count int) ([]PlacedSection, error) {
	c, err := chunk.NetworkDecode(world.DefaultBlockRegistry, payload, count, d.rng)
	if err != nil {
		return nil, err
	}
	var out []PlacedSection
	for i, sub := range c.Sub() {
		if sub == nil || sub.Empty() {
			continue
		}
		out = append(out, PlacedSection{Y: int32(c.SubY(int16(i))), Section: d.section(sub)})
	}
	return out, nil
}

func (d dragonflyDecoder) BlockName(networkID uint32) (string, bool) {
	rid, ok := world.DefaultBlockRegistry.HashToRuntimeID(networkID)
	if !ok {
		return "", false
	}
	name, _, ok := world.DefaultBlockRegistry.RuntimeIDToState(rid)
	return name, ok
}

// section reads the sub-chunk's first layer. The cells hold network block
// hashes, not runtime IDs, so every distinct one goes through BlockName.
func (d dragonflyDecoder) section(sub *chunk.SubChunk) Section {
	var s Section
	index := map[uint32]uint16{}
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				hash := sub.Block(uint8(x), uint8(y), uint8(z), 0)
				i, seen := index[hash]
				if !seen {
					name, ok := d.BlockName(hash)
					if !ok {
						name = unmappedBlock
					}
					i = uint16(len(s.Palette))
					s.Palette = append(s.Palette, name)
					index[hash] = i
				}
				s.Cells[(y*16+z)*16+x] = i
			}
		}
	}
	return s
}
