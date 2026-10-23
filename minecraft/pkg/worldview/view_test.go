package worldview

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/df-mc/goleveldb/leveldb"
)

// world builds a LevelDB holding entries, closed and without a LOCK file,
// the way a world sits on disk for a reader.
func world(t testing.TB, entries map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range entries {
		if err := db.Put([]byte(k), v, nil); err != nil {
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

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestOpenReadsTheWorldAndLeavesItAsItWas(t *testing.T) {
	db := world(t, map[string][]byte{"chunk": []byte("v")})
	before := names(t, db)
	work := filepath.Join(t.TempDir(), "work")

	view, done, err := Open(db, work)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := view.Get([]byte("chunk"), nil); err != nil || string(v) != "v" {
		t.Errorf("read through the view = %q, %v", v, err)
	}
	if err := view.Put([]byte("k"), []byte("v"), nil); err == nil {
		t.Error("the view accepted a write")
	}
	done()

	if after := names(t, db); !slices.Equal(before, after) {
		t.Errorf("the world directory changed: %v, was %v", after, before)
	}
	if left := names(t, work); len(left) != 0 {
		t.Errorf("the view was left behind: %v", left)
	}
}

func TestOpenRemovesAViewAKilledRunLeft(t *testing.T) {
	db := world(t, map[string][]byte{"chunk": []byte("v")})
	work := t.TempDir()
	stale := filepath.Join(work, "view-killed")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	_, done, err := Open(db, work)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale view still there: %v", err)
	}
}
