package heights

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// The heights are kept on the volume so that a restart serves them from its
// first request and not from the end of its first cycle, beside the biomes
// they were read with.
//
// Little-endian throughout: the magic, whose last byte is the format's
// version, the snapshot's time in Unix nanoseconds and the chunk count's
// total as 64-bit values, then for each dimension the number of chunks and
// each one's position and reference (twelve bytes, in position order), and
// the number of bytes of columns and the columns. A CRC-32 of all of it
// ends the file. A file of any other version is not read, and is written
// over by the next reading.
const fileMagic = "MCHEIGH1"

// maxFile is the largest file Encode can write: the magic, the snapshot's
// time and chunk count, two counts for each dimension and the checksum,
// and a table entry and a record at its largest for every chunk kept.
const maxFile = int64(len(fileMagic) + 16 + 3*8 + 4 + maxChunks*(12+Columns*2))

var errFile = errors.New("saved heights are damaged or from another version")

// Encode is the saved form of the heights.
func (m *Map) Encode() []byte {
	out := []byte(fileMagic)
	out = binary.LittleEndian.AppendUint64(out, uint64(m.SnapshotAt.UnixNano()))
	out = binary.LittleEndian.AppendUint64(out, uint64(int64(m.CensusChunks)))
	for _, l := range m.layers {
		keys := make([]uint64, 0, len(l.cells))
		for key := range l.cells {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(keys)))
		for _, key := range keys {
			out = binary.LittleEndian.AppendUint64(out, key)
			out = binary.LittleEndian.AppendUint32(out, l.cells[key])
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(len(l.data)))
		out = append(out, l.data...)
	}
	return binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// Decode reads what Encode wrote. The file is this service's own, on its
// own volume, and is still held to every bound a world is: a reference to
// columns that are not there would otherwise be a panic in the middle of
// some later request.
func Decode(raw []byte) (*Map, error) {
	if len(raw) < len(fileMagic)+16+4 || string(raw[:len(fileMagic)]) != fileMagic {
		return nil, errFile
	}
	body, sum := raw[:len(raw)-4], binary.LittleEndian.Uint32(raw[len(raw)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, errFile
	}
	b := body[len(fileMagic):]
	m := &Map{
		SnapshotAt:   time.Unix(0, int64(binary.LittleEndian.Uint64(b))),
		CensusChunks: int(int64(binary.LittleEndian.Uint64(b[8:]))),
	}
	b = b[16:]
	room := maxChunks
	for i := range m.layers {
		if len(b) < 4 {
			return nil, errFile
		}
		cells := int(binary.LittleEndian.Uint32(b))
		b = b[4:]
		if cells > room || len(b) < cells*12+4 {
			return nil, errFile
		}
		room -= cells
		refs := b[:cells*12]
		b = b[cells*12:]
		size := int(binary.LittleEndian.Uint32(b))
		b = b[4:]
		if size > maxData || len(b) < size {
			return nil, errFile
		}
		l := &layer{cells: make(map[uint64]uint32, cells), data: slices.Clone(b[:size])}
		b = b[size:]
		for ; len(refs) > 0; refs = refs[12:] {
			key, ref := binary.LittleEndian.Uint64(refs), binary.LittleEndian.Uint32(refs[8:])
			cx, cz := int32(key>>32), int32(key)
			if cx < -maxChunkCoordinate || cx > maxChunkCoordinate || cz < -maxChunkCoordinate || cz > maxChunkCoordinate {
				return nil, errFile
			}
			tier := int(ref >> tierShift)
			if tier >= len(tierBits) {
				return nil, errFile
			}
			if tier > 0 && int(ref&payloadMask)+recordSize(tier) > size {
				return nil, errFile
			}
			l.cells[key] = ref
		}
		if len(l.cells) != cells {
			return nil, errFile
		}
		m.layers[i] = l
	}
	if len(b) != 0 {
		return nil, errFile
	}
	return m, nil
}

// Save writes the heights to path so that the file there is always a whole
// one: the old or the new, never part of either.
func (m *Map) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(m.Encode())
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Load reads the heights Save left at path.
func Load(path string) (*Map, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxFile {
		return nil, fmt.Errorf("%w: %d bytes", errFile, info.Size())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(raw)
}
