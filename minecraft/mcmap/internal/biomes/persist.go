package biomes

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

// A reading is kept on the volume so that a restart serves the overlay,
// and answers what biome a block is in, from its first request and not
// from the end of its first cycle. The file is the chunks alone: the
// search index is rebuilt from them in well under a second.
//
// Little-endian throughout: the magic, the snapshot's time in Unix
// nanoseconds and the chunk count's total as 64-bit values, then for each
// dimension the number of kinds and their ids, the number of chunks and
// each one's position and reference (twelve bytes), and the number of
// column-by-column chunks and their columns. A CRC-32 of all of it ends
// the file.
const fileMagic = "MCBIOME1"

// maxFile is more than a world at every bound comes to.
const maxFile = 400 << 20

var errFile = errors.New("saved biomes are damaged or from another version")

func (w *World) encode() []byte {
	out := []byte(fileMagic)
	out = binary.LittleEndian.AppendUint64(out, uint64(w.SnapshotAt.UnixNano()))
	out = binary.LittleEndian.AppendUint64(out, uint64(int64(w.CensusChunks)))
	for _, l := range w.layers {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(l.ids)))
		for _, id := range l.ids {
			out = binary.LittleEndian.AppendUint32(out, id)
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(len(l.cells)))
		for key, ref := range l.cells {
			out = binary.LittleEndian.AppendUint64(out, key)
			out = binary.LittleEndian.AppendUint32(out, ref)
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(len(l.mixed)/Columns))
		out = append(out, l.mixed...)
	}
	return binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// decode reads what encode wrote. The file is this service's own, on its
// own volume, and is still held to every bound a world is: a reference to
// a kind or a chunk that is not there would otherwise be a panic in the
// middle of some later request.
func decode(raw []byte) (*World, error) {
	if len(raw) < len(fileMagic)+16+4 || string(raw[:len(fileMagic)]) != fileMagic {
		return nil, errFile
	}
	body, sum := raw[:len(raw)-4], binary.LittleEndian.Uint32(raw[len(raw)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, errFile
	}
	b := body[len(fileMagic):]
	next32 := func() (uint32, bool) {
		if len(b) < 4 {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(b)
		b = b[4:]
		return v, true
	}
	w := &World{
		SnapshotAt:   time.Unix(0, int64(binary.LittleEndian.Uint64(b))),
		CensusChunks: int(int64(binary.LittleEndian.Uint64(b[8:]))),
	}
	b = b[16:]
	room := MaxChunks
	for i := range w.layers {
		l := &Layer{}
		kinds, ok := next32()
		if !ok || kinds > maxKinds+1 || len(b) < int(kinds)*4 {
			return nil, errFile
		}
		for ; kinds > 0; kinds-- {
			id, _ := next32()
			l.ids = append(l.ids, id)
		}
		cells, ok := next32()
		if !ok || int(cells) > room || len(b) < int(cells)*12 {
			return nil, errFile
		}
		room -= int(cells)
		refs := b[:cells*12]
		b = b[cells*12:]
		mixed, ok := next32()
		if !ok || mixed > maxMixed || len(b) < int(mixed)*Columns {
			return nil, errFile
		}
		// Copied, so that the chunks do not keep the whole file in memory.
		l.mixed, b = slices.Clone(b[:int(mixed)*Columns]), b[int(mixed)*Columns:]
		for _, k := range l.mixed {
			if int(k) >= len(l.ids) {
				return nil, errFile
			}
		}
		l.cells = make(map[uint64]uint32, cells)
		for ; len(refs) > 0; refs = refs[12:] {
			key, ref := binary.LittleEndian.Uint64(refs), binary.LittleEndian.Uint32(refs[8:])
			cx, cz := int32(key>>32), int32(key)
			if cx < -maxChunkCoordinate || cx > maxChunkCoordinate || cz < -maxChunkCoordinate || cz > maxChunkCoordinate {
				return nil, errFile
			}
			if at := ref &^ mixedFlag; (ref&mixedFlag != 0 && at >= mixed) || (ref&mixedFlag == 0 && int(at) >= len(l.ids)) {
				return nil, errFile
			}
			l.cells[key] = ref
		}
		w.Stats.Chunks += len(l.cells)
		w.layers[i] = l
	}
	if len(b) != 0 {
		return nil, errFile
	}
	w.finish()
	return w, nil
}

// save writes the world to path so that the file there is always a whole
// one: the old or the new, never part of either.
func (w *World) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(w.encode())
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

func load(path string) (*World, error) {
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
	return decode(raw)
}
