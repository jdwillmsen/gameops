package repair

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/opt"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// Record tags of a Bedrock chunk in the world's LevelDB. The key is the chunk's
// x and z as little-endian int32, then the tag (overworld only; other
// dimensions add a dimension id before it, which the Colosseum never needs).
const (
	tagSubChunk      = 0x2f // followed by the slice's signed y index
	tagChunkVersion  = 0x2c
	tagLegacyVersion = 0x76
	cellsPerSubChunk = 4096
)

// WorldReader serves blocks from a copy of a Bedrock world's LevelDB. It reads
// the first (solid) block layer of every sub-chunk, so water held in a second
// layer is not seen; the plan's water is placed by fill and lives in the first.
//
// A cell in a chunk that was never generated is Unknown. A chunk that exists
// but lacks a sub-chunk is air, which is how the game stores it.
type WorldReader struct {
	db     *leveldb.DB
	match  *Matcher
	closer func() error
}

// OpenWorld opens the LevelDB directory read-only. plan supplies the spellings
// a scanned block is matched to; without one every block keeps its full states.
func OpenWorld(dir string, plan []Op) (*WorldReader, error) {
	db, err := leveldb.OpenFile(dir, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		return nil, fmt.Errorf("repair: open world db %s: %w", dir, err)
	}
	r := NewWorldReader(db, plan)
	r.closer = db.Close
	return r, nil
}

// NewWorldReader reads from an already open database.
func NewWorldReader(db *leveldb.DB, plan []Op) *WorldReader {
	return &WorldReader{db: db, match: NewMatcher(plan)}
}

func (r *WorldReader) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer()
}

func (r *WorldReader) Read(b Box, pal *Palette) (*Grid, error) {
	g := NewGrid(b)
	for cx := b.X0 >> 4; cx <= b.X1>>4; cx++ {
		for cz := b.Z0 >> 4; cz <= b.Z1>>4; cz++ {
			if err := r.readChunk(g, pal, cx, cz); err != nil {
				return nil, fmt.Errorf("chunk %d,%d: %w", cx, cz, err)
			}
		}
	}
	return g, nil
}

func chunkKey(cx, cz int, tag byte, y int8) []byte {
	k := binary.LittleEndian.AppendUint32(nil, uint32(int32(cx)))
	k = binary.LittleEndian.AppendUint32(k, uint32(int32(cz)))
	k = append(k, tag)
	if tag == tagSubChunk {
		k = append(k, byte(y))
	}
	return k
}

func (r *WorldReader) chunkExists(cx, cz int) (bool, error) {
	for _, tag := range []byte{tagChunkVersion, tagLegacyVersion} {
		ok, err := r.db.Has(chunkKey(cx, cz, tag, 0), nil)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (r *WorldReader) readChunk(g *Grid, pal *Palette, cx, cz int) error {
	exists, err := r.chunkExists(cx, cz)
	if err != nil || !exists {
		return err
	}
	airID := pal.ID("air")
	b := g.Box
	for sy := floorDiv(b.Y0, 16); sy <= floorDiv(b.Y1, 16); sy++ {
		raw, err := r.db.Get(chunkKey(cx, cz, tagSubChunk, int8(sy)), nil)
		var cells []int32
		switch {
		case err == leveldb.ErrNotFound:
			cells = nil // an absent slice is all air
		case err != nil:
			return err
		default:
			if cells, err = r.decodeSubChunk(raw, pal); err != nil {
				return fmt.Errorf("sub-chunk y %d: %w", sy, err)
			}
		}
		for x := max(b.X0, cx*16); x <= min(b.X1, cx*16+15); x++ {
			for z := max(b.Z0, cz*16); z <= min(b.Z1, cz*16+15); z++ {
				for y := max(b.Y0, sy*16); y <= min(b.Y1, sy*16+15); y++ {
					id := airID
					if cells != nil {
						id = cells[(x&15)<<8|(z&15)<<4|(y&15)]
					}
					g.Cells[g.index(x, y, z)] = id
				}
			}
		}
	}
	return nil
}

// decodeSubChunk returns the palette id of each of the slice's 4096 cells,
// indexed x, then z, then y. Layout: a version (8 or 9), a layer count, for 9
// the slice's y index, then per layer a width in bits, the indexes packed into
// 32-bit little-endian words, a palette count and that many NBT compounds.
// Only the first layer is read.
func (r *WorldReader) decodeSubChunk(raw []byte, pal *Palette) ([]int32, error) {
	if len(raw) < 3 {
		return nil, fmt.Errorf("record is %d bytes", len(raw))
	}
	var body []byte
	switch raw[0] {
	case 8:
		body = raw[2:]
	case 9:
		body = raw[3:]
	default:
		return nil, fmt.Errorf("unsupported sub-chunk version %d", raw[0])
	}
	if raw[1] == 0 || len(body) < 1 {
		return nil, fmt.Errorf("sub-chunk has no layers")
	}
	bits := int(body[0] >> 1)
	if body[0]&1 != 0 {
		return nil, fmt.Errorf("network-format palette on disk")
	}
	body = body[1:]
	idx := make([]int, cellsPerSubChunk)
	if bits != 0 {
		if bits > 16 {
			return nil, fmt.Errorf("palette width %d", bits)
		}
		per := 32 / bits
		words := (cellsPerSubChunk + per - 1) / per
		if len(body) < words*4+4 {
			return nil, fmt.Errorf("truncated indexes")
		}
		for i := range idx {
			w := binary.LittleEndian.Uint32(body[i/per*4:])
			idx[i] = int(w>>(i%per*bits)) & (1<<bits - 1)
		}
		body = body[words*4:]
	}
	count := 1
	if bits != 0 {
		count = int(int32(binary.LittleEndian.Uint32(body)))
		body = body[4:]
	}
	if count < 1 || count > cellsPerSubChunk {
		return nil, fmt.Errorf("palette of %d entries", count)
	}
	dec := nbt.NewDecoderWithEncoding(bytes.NewReader(body), nbt.LittleEndian)
	ids := make([]int32, count)
	for i := range ids {
		var e map[string]any
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("palette entry %d: %w", i, err)
		}
		name, _ := e["name"].(string)
		states, _ := e["states"].(map[string]any)
		if name == "" {
			return nil, fmt.Errorf("palette entry %d has no name", i)
		}
		ids[i] = pal.ID(r.match.Canonical(name, RenderStates(states)))
	}
	out := make([]int32, cellsPerSubChunk)
	for i, k := range idx {
		if k >= count {
			return nil, fmt.Errorf("cell %d indexes palette entry %d of %d", i, k, count)
		}
		out[i] = ids[k]
	}
	return out, nil
}
