package subchunk

import "fmt"

// maxBiomeSections bounds how many storages a biome payload is read for: the
// overworld has 24 sections, and anything after them is the border byte and
// block entities.
const maxBiomeSections = 24

// ColumnBiomes is one biome id per column, indexed x<<4|z.
type ColumnBiomes [256]uint16

// Skip decodes count sections from the front of a legacy chunk payload and
// returns what follows them, which is where the biomes start.
func Skip(payload []byte, count int) ([]byte, error) {
	rest := payload
	for i := 0; i < count; i++ {
		sc, err := Decode(rest)
		if err != nil {
			return nil, fmt.Errorf("section %d: %w", i, err)
		}
		rest = sc.Trailer
	}
	return rest, nil
}

// TopBiomes reads the paletted biome storages that open a payload and returns
// the top plane of the last one. Sections above the terrain repeat the surface
// biome, so the highest section is the surface climate without knowing the
// heightmap; cave biomes live in the lower sections and are not drawn.
func TopBiomes(payload []byte) (ColumnBiomes, error) {
	r := &reader{b: payload}
	var top ColumnBiomes
	var have bool
	var last storageData
	for i := 0; i < maxBiomeSections && len(r.b) > 0; i++ {
		if r.b[0] == 0x7f<<1|1 { // same as the section below
			r.b = r.b[1:]
			if !have {
				return ColumnBiomes{}, fmt.Errorf("%w: first biome storage repeats a previous one", ErrMalformed)
			}
			continue
		}
		if r.b[0]&1 == 0 { // the border byte: no more storages
			break
		}
		s, err := readRawStorage(r)
		if err != nil {
			return ColumnBiomes{}, fmt.Errorf("biome section %d: %w", i, err)
		}
		last = s
		have = true
	}
	if !have {
		return ColumnBiomes{}, fmt.Errorf("%w: no biome storage", ErrMalformed)
	}
	// Only the plane that is kept is unpacked and checked: the other storages
	// would be thrown away, and there are up to 24 of them a chunk.
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			ix := int(last.index(x<<8 | z<<4 | 15))
			if ix >= len(last.palette) {
				return ColumnBiomes{}, fmt.Errorf("%w: biome index %d outside palette of %d", ErrMalformed, ix, len(last.palette))
			}
			top[x<<4|z] = uint16(last.palette[ix])
		}
	}
	return top, nil
}
