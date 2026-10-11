package structures

import (
	"encoding/binary"
	"errors"
)

// A world is not always generated from one seed. The game writes, with
// each chunk, which seed generated it: record 63 of the chunk is a hash,
// and the entry of that hash in LevelChunkMetaDataDictionary holds the
// chunk's GenerationSeed beside the versions that wrote it. A world whose
// seed was changed, as the FWB world's was when it was updated, has chunks
// of both, and each goes on being what its own seed made it.
//
// So a site is worked out from the seed of the chunk it falls in. A site in
// a chunk nobody has generated is worked out from the seed in level.dat,
// which is the one the game will generate that chunk from.
//
// The dictionary is a 32-bit count and then, for each entry, its eight-byte
// hash and an unnamed NBT compound.
const (
	// TagGeneration is the record naming a chunk's entry in the dictionary.
	TagGeneration = 63

	dictionaryKey = "LevelChunkMetaDataDictionary"
	// The FWB world's dictionary is 160 KB and 429 entries. These bound
	// what a damaged one can ask for.
	maxDictionary        = 32 << 20
	maxDictionaryEntries = 1 << 16
	// maxSeeds is how many seeds one world's chunks are told apart by. A
	// chunk of a seed past it is one whose seed is not known.
	maxSeeds = 8
)

// seedUnknown is the seed of a chunk that names none: one written before
// the game kept the record, or one whose entry could not be read.
const seedUnknown int8 = -1

// worldSeed is one seed chunks of the world were generated from.
type worldSeed struct {
	whole int64
	// narrow is set where only the low 32 bits are known: a seed an
	// operator supplied, or one a search found.
	narrow bool
}

// seedBook is the seeds the dictionary holds, and which of them each
// entry's chunks were generated from.
type seedBook struct {
	seeds  []int64
	byHash map[uint64]int8
	// over is how many entries name a seed past maxSeeds. Their chunks
	// are ones whose seed is not known.
	over int
}

var errDictionary = errors.New("chunk metadata dictionary is not a count followed by that many hashed compounds")

// readSeedBook reads the dictionary. One that is not laid out as above is
// refused whole: half of it would give some chunks a seed and leave others
// that have one looking as though they had none.
func readSeedBook(raw []byte) (*seedBook, error) {
	if len(raw) < 4 || len(raw) > maxDictionary {
		return nil, errDictionary
	}
	n := binary.LittleEndian.Uint32(raw)
	if n > maxDictionaryEntries {
		return nil, errDictionary
	}
	book := &seedBook{byHash: make(map[uint64]int8, n)}
	rest := raw[4:]
	for range n {
		if len(rest) < 8 {
			return nil, errDictionary
		}
		hash := binary.LittleEndian.Uint64(rest)
		at := seedUnknown
		var err error
		rest, err = fieldsAt(rest[8:], func(field []byte, tag byte, payload []byte) {
			if string(field) != "GenerationSeed" || tag != tagLong {
				return
			}
			at = book.indexOf(int64(binary.LittleEndian.Uint64(payload)))
		})
		if err != nil {
			return nil, errDictionary
		}
		book.byHash[hash] = at
	}
	if len(rest) != 0 {
		return nil, errDictionary
	}
	return book, nil
}

func (b *seedBook) indexOf(seed int64) int8 {
	for i, s := range b.seeds {
		if s == seed {
			return int8(i)
		}
	}
	if len(b.seeds) >= maxSeeds {
		b.over++
		return seedUnknown
	}
	b.seeds = append(b.seeds, seed)
	return int8(len(b.seeds) - 1)
}
