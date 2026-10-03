// Package chunks counts the chunks in the world and remembers every one it
// has seen, so that a chunk which disappears is noticed within one snapshot
// rather than when the next nightly backup comes out small.
//
// A Bedrock world never deletes a chunk in normal play. One that vanishes
// went with a LevelDB table file, which is what happens when the volume
// under the server is lost mid-write and the server then "repairs" the
// database by dropping what it can no longer find.
package chunks

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/opt"
)

// Dimension is Bedrock's own number for a dimension, as stored in chunk keys.
type Dimension int32

const (
	Overworld Dimension = 0
	Nether    Dimension = 1
	End       Dimension = 2
)

// Dimensions in the order the rest of the service lists them.
var Dimensions = []Dimension{Overworld, Nether, End}

// Name is the dimension's id everywhere else in the service.
func (d Dimension) Name() string {
	switch d {
	case Overworld:
		return "overworld"
	case Nether:
		return "nether"
	case End:
		return "end"
	}
	return fmt.Sprintf("dimension-%d", int32(d))
}

// Pos is one chunk, in chunk coordinates.
type Pos struct {
	Dim  Dimension
	X, Z int32
}

type Set map[Pos]struct{}

// Every record a chunk owns starts with its position and, outside the
// overworld, its dimension, followed by a one-byte tag. The tags in use sit
// between Data3D ('+') and the legacy version byte ('v'); a sub-chunk record
// adds one more byte for its height.
const (
	firstTag = 0x2b
	lastTag  = 0x77
)

func chunkOf(k []byte) (Pos, bool) {
	var dim Dimension
	var rest []byte
	switch len(k) {
	case 9, 10:
		rest = k[8:]
	case 13, 14:
		dim = Dimension(int32(binary.LittleEndian.Uint32(k[8:12])))
		rest = k[12:]
	default:
		return Pos{}, false
	}
	// Named records such as "Overworld" or "BiomeData" can share a chunk
	// key's length. A chunk key is never all printable: the position's high
	// bytes are 0x00 or 0xff for any world smaller than millions of blocks.
	if printable(k) || dim < Overworld || dim > End || rest[0] < firstTag || rest[0] > lastTag {
		return Pos{}, false
	}
	return Pos{dim, int32(binary.LittleEndian.Uint32(k[0:4])), int32(binary.LittleEndian.Uint32(k[4:8]))}, true
}

func printable(k []byte) bool {
	for _, c := range k {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// Scan lists the chunks in the LevelDB at dbDir.
//
// Even a read-only open creates a LOCK file, and the mirror must hold only
// the files the server has, so the scan runs on hard links to them in a
// directory under workDir. Nothing writes the mirror while a cycle scans it.
func Scan(ctx context.Context, dbDir, workDir string) (Set, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	// A scan killed part-way leaves its view behind, and its links keep
	// tables the mirror has since dropped on the volume. Scans never overlap,
	// so any view still here is one of those.
	if stale, err := filepath.Glob(filepath.Join(workDir, "scan-*")); err == nil {
		for _, dir := range stale {
			os.RemoveAll(dir)
		}
	}
	view, err := os.MkdirTemp(workDir, "scan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(view)
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if err := linkOrCopy(filepath.Join(dbDir, e.Name()), filepath.Join(view, e.Name())); err != nil {
			return nil, err
		}
	}

	db, err := leveldb.OpenFile(view, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		return nil, fmt.Errorf("open world: %w", err)
	}
	defer db.Close()
	it := db.NewIterator(nil, nil)
	defer it.Release()
	found := Set{}
	for n := 0; it.Next(); n++ {
		if n%65536 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if p, ok := chunkOf(it.Key()); ok {
			found[p] = struct{}{}
		}
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("read world: %w", err)
	}
	return found, ctx.Err()
}

// linkOrCopy falls back to copying where a link cannot be made, such as
// across filesystems.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}
