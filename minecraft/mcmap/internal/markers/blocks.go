package markers

import (
	"encoding/binary"
	"strings"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// A block entity says what a block holds, not always which block it is: a
// trapped chest's record is a chest's, and a shulker box's has no colour in
// it. Both are in the block's own name, which is in the sixteen-block-tall
// slice of the chunk that holds the block.
const (
	// tagSubChunk is that slice's record: the chunk's key, this tag, and
	// the slice's height index as one signed byte.
	tagSubChunk = 0x2f
	// blocksPerSubChunk is every block of a slice, indexed x, then z, then y.
	blocksPerSubChunk = 4096
)

// Colours is the sixteen dye colours in the order the game numbers them,
// which is the number a bed's record holds.
var Colours = []string{
	"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black",
}

// Undyed is the colour of a shulker box nobody has dyed.
const Undyed = "undyed"

func colourOf(index int32) string {
	if index < 0 || int(index) >= len(Colours) {
		return ""
	}
	return Colours[index]
}

// blockAt is the name of the block at a position, without the minecraft:
// prefix. A position whose slice is missing, or is not laid out as this
// reads it, has no name; the marker is then drawn without what the name
// would have added.
func blockAt(db *leveldb.DB, dim chunks.Dimension, x, y, z int32) (string, bool) {
	key := append(chunks.RecordKey(chunks.Pos{Dim: dim, X: x >> 4, Z: z >> 4}, tagSubChunk), byte(int8(y>>4)))
	sub, err := db.Get(key, nil)
	if err != nil || len(sub) > maxRecord {
		return "", false
	}
	return blockIn(sub, int(x&15), int(y&15), int(z&15))
}

// blockIn reads one block's name out of a slice. The slice is a version,
// then layers of blocks; only the first layer is read, the second being
// the water a block sits in. A layer is a width in bits, every block's
// index into a palette packed into 32-bit words at that width, and the
// palette: a count, then that many records each naming a block.
func blockIn(sub []byte, x, y, z int) (string, bool) {
	if len(sub) < 3 {
		return "", false
	}
	var layer []byte
	switch sub[0] {
	case 8:
		// A count of layers.
		if sub[1] == 0 {
			return "", false
		}
		layer = sub[2:]
	case 9:
		// A count of layers and the slice's own height index.
		if sub[1] == 0 {
			return "", false
		}
		layer = sub[3:]
	default:
		return "", false
	}
	if len(layer) < 1 {
		return "", false
	}
	// The low bit marks indexes meant for the network, which have no
	// palette to look anything up in. Nothing on disk is written so.
	bits := int(layer[0] >> 1)
	if layer[0]&1 != 0 {
		return "", false
	}
	layer = layer[1:]
	index := 0
	// At no width at all every block is the palette's one entry, and the
	// palette has no count.
	if bits != 0 {
		if bits > 16 || 32/bits == 0 {
			return "", false
		}
		perWord := 32 / bits
		words := (blocksPerSubChunk + perWord - 1) / perWord
		if len(layer) < words*4+4 {
			return "", false
		}
		at := x<<8 | z<<4 | y
		word := binary.LittleEndian.Uint32(layer[at/perWord*4:])
		index = int(word>>(at%perWord*bits)) & (1<<bits - 1)
		layer = layer[words*4:]
		if count := int32(binary.LittleEndian.Uint32(layer)); int32(index) >= count {
			return "", false
		}
		layer = layer[4:]
	}
	for range index {
		rest, err := fields(layer, func([]byte, byte, []byte) {})
		if err != nil {
			return "", false
		}
		layer = rest
	}
	name := ""
	if _, err := fields(layer, func(tagName []byte, tag byte, payload []byte) {
		if string(tagName) == "name" {
			name, _ = stringOf(tag, payload)
		}
	}); err != nil {
		return "", false
	}
	name = strings.TrimPrefix(name, "minecraft:")
	return name, name != ""
}

// shulkerColour reads a shulker box's colour from its block's name.
func shulkerColour(block string) string {
	colour, ok := strings.CutSuffix(block, "_shulker_box")
	if !ok {
		return ""
	}
	if colour == Undyed {
		return Undyed
	}
	for _, known := range Colours {
		if colour == known {
			return known
		}
	}
	return ""
}
