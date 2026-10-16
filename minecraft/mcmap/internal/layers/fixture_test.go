package layers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers/layerstest"
)

// Blocks of a made-up world are named by a function of where they are in
// the chunk: x and z from 0 to 15 and y in the slice from 0 to 15, or in
// the world where the function is given a world y.
type blockFunc func(x, y, z int) string

func entry(t testing.TB, name string) []byte { return layerstest.Entry(name) }

func storage(t testing.TB, names [blocksPerSub]string, width int) []byte {
	return layerstest.Storage(names, width)
}

// sliceRecord encodes a sub-chunk of the version, holding the layers,
// which are each a function of x, y, z in the slice.
func sliceRecord(t testing.TB, version byte, h int8, width int, layers ...blockFunc) []byte {
	t.Helper()
	fs := make([]layerstest.Blocks, len(layers))
	for i, l := range layers {
		fs[i] = layerstest.Blocks(l)
	}
	return layerstest.Slice(version, h, width, fs...)
}

// fake is a made-up world: chunks of slices written as a game writes them.
type fake struct {
	t   testing.TB
	db  *leveldb.DB
	dir string
}

func newFake(t testing.TB) *fake {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &fake{t: t, db: db, dir: dir}
}

func (f *fake) reader() Reader { return Reader{DB: f.db, Registry: NewRegistry()} }

// put writes one slice record of a chunk.
func (f *fake) put(pos chunks.Pos, h int8, record []byte) {
	f.t.Helper()
	key := append(chunks.RecordKey(pos, tagSubChunk), byte(h))
	if err := f.db.Put(key, record, nil); err != nil {
		f.t.Fatal(err)
	}
}

// chunk writes a whole chunk, a function of world x, y, z within it, from
// the lowest slice of the dimension to the highest that has anything.
func (f *fake) chunk(pos chunks.Pos, top int, world blockFunc) {
	f.t.Helper()
	lowest, _, _ := span(pos.Dim)
	for h := lowest; h <= top>>4; h++ {
		f.put(pos, int8(h), sliceRecord(f.t, 9, int8(h), 0, func(x, y, z int) string {
			return world(x, h*16+y, z)
		}))
	}
}

func all(n string) blockFunc { return func(int, int, int) string { return n } }

// closed finishes writing and gives the directory of the database as the
// mirror holds one: closed, and with no LOCK file.
func (f *fake) closed() string {
	f.t.Helper()
	dir := f.dir
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "LOCK")); err != nil && !errors.Is(err, os.ErrNotExist) {
		f.t.Fatal(err)
	}
	return dir
}
