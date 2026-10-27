package camera

// Section is one decoded 16x16x16 sub-chunk, indexed (y*16+z)*16+x. Cells
// index into Palette, so a decoder hands over names and never its own runtime
// IDs: swapping the decoder must not change the hub.
type Section struct {
	Palette []string
	Cells   [4096]uint16
}

// PlacedSection is a Section with its sub-chunk Y index (world Y >> 4).
type PlacedSection struct {
	Y       int32
	Section Section
}

// Decoder turns the network block data of the world packets into names. It is
// an interface because the sub-chunk payload decoder is being rewritten.
type Decoder interface {
	// SubChunk decodes the payload of one SubChunk packet entry.
	SubChunk(payload []byte) (Section, error)
	// Chunk decodes a whole LevelChunk payload (the non-request path); empty
	// sections are omitted. rest is what follows the sections, which is where
	// the biomes start; returning it spares the caller decoding them again.
	Chunk(payload []byte, subChunkCount int) (secs []PlacedSection, rest []byte, err error)
	// BlockName resolves the block ID of a single-block update. ok is false for
	// a state the decoder does not know, which the hub draws as a placeholder.
	BlockName(networkID uint32) (name string, ok bool)
}
