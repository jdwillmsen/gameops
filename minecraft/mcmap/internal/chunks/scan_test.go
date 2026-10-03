package chunks

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/df-mc/goleveldb/leveldb"
)

func chunkKey(dim, x, z int32, tag byte, rest ...byte) []byte {
	k := binary.LittleEndian.AppendUint32(nil, uint32(x))
	k = binary.LittleEndian.AppendUint32(k, uint32(z))
	if dim != 0 {
		k = binary.LittleEndian.AppendUint32(k, uint32(dim))
	}
	return append(append(k, tag), rest...)
}

// writeWorld builds a LevelDB holding keys, closed and ready to scan, the
// way a mirrored world sits on disk: no LOCK file.
func writeWorld(t *testing.T, keys ...[]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := db.Put(k, []byte("v"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "LOCK")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestChunkOf(t *testing.T) {
	for _, c := range []struct {
		name string
		key  []byte
		want Pos
		ok   bool
	}{
		{"overworld version", chunkKey(0, 3, -7, 0x2c), Pos{Overworld, 3, -7}, true},
		{"overworld subchunk", chunkKey(0, -1, 2, 0x2f, 4), Pos{Overworld, -1, 2}, true},
		{"nether", chunkKey(1, 10, 20, 0x2b), Pos{Nether, 10, 20}, true},
		{"end subchunk", chunkKey(2, -500, 0, 0x2f, 0xfc), Pos{End, -500, 0}, true},
		// Named records that happen to be chunk-key lengths.
		{"Overworld", []byte("Overworld"), Pos{}, false},
		{"BiomeData", []byte("BiomeData"), Pos{}, false},
		{"mobevents", []byte("mobevents"), Pos{}, false},
		{"scoreboard", []byte("scoreboard"), Pos{}, false},
		{"actor", append([]byte("actorprefix"), 1, 2, 3, 4, 5, 6, 7, 8), Pos{}, false},
		{"digp", append([]byte("digp"), chunkKey(0, 1, 1, 0)[:8]...), Pos{}, false},
		{"unknown dimension", chunkKey(7, 1, 1, 0x2c), Pos{}, false},
		{"tag out of range", chunkKey(0, 1, 1, 0x10), Pos{}, false},
		{"too short", []byte{1, 2, 3}, Pos{}, false},
	} {
		got, ok := chunkOf(c.key)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: chunkOf = %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestScan_CountsEachChunkOnceAndLeavesTheWorldAsItFoundIt(t *testing.T) {
	dir := writeWorld(t,
		chunkKey(0, 0, 0, 0x2c), chunkKey(0, 0, 0, 0x2f, 0), chunkKey(0, 0, 0, 0x2f, 1),
		chunkKey(0, 1, 0, 0x2c),
		chunkKey(1, 0, 0, 0x2c),
		chunkKey(2, -3, 4, 0x2c),
		[]byte("BiomeData"), []byte("~local_player"),
	)
	before := listing(t, dir)

	got, err := Scan(context.Background(), dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := Set{{Overworld, 0, 0}: {}, {Overworld, 1, 0}: {}, {Nether, 0, 0}: {}, {End, -3, 4}: {}}
	if len(got) != len(want) {
		t.Fatalf("Scan = %v, want %v", got, want)
	}
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("missing %+v", p)
		}
	}
	// The mirror is the map's copy of the world and is compared file by file
	// with the server's; a LOCK appearing in it would be a file the server
	// does not have.
	if after := listing(t, dir); !slices.Equal(before, after) {
		t.Errorf("the scanned directory changed: %v then %v", before, after)
	}
}

func TestScan_CleansUpAfterItself(t *testing.T) {
	dir := writeWorld(t, chunkKey(0, 0, 0, 0x2c))
	work := t.TempDir()
	if _, err := Scan(context.Background(), dir, work); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("left %d entries in the work directory", len(entries))
	}
}

func TestScan_StopsWhenCancelled(t *testing.T) {
	dir := writeWorld(t, chunkKey(0, 0, 0, 0x2c))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, dir, t.TempDir()); err == nil {
		t.Error("a cancelled scan reported success")
	}
}

func TestScan_NoWorldIsAnError(t *testing.T) {
	if _, err := Scan(context.Background(), filepath.Join(t.TempDir(), "nothing"), t.TempDir()); err == nil {
		t.Error("scanning a directory that does not exist succeeded")
	}
}

func listing(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// On a fresh volume nothing has created the work directory yet.
func TestScan_CreatesItsWorkDirectory(t *testing.T) {
	dir := writeWorld(t, chunkKey(0, 0, 0, 0x2c))
	work := filepath.Join(t.TempDir(), "chunks")
	if got, err := Scan(context.Background(), dir, work); err != nil || len(got) != 1 {
		t.Fatalf("Scan = %v, %v", got, err)
	}
}

func TestScan_ClearsAViewAKilledScanLeftBehind(t *testing.T) {
	dir := writeWorld(t, chunkKey(0, 0, 0, 0x2c))
	work := t.TempDir()
	left := filepath.Join(work, "scan-123")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, "000001.ldb"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(context.Background(), dir, work); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the leftover view is still there: %v", err)
	}
}
