package chunks

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

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

func TestOpenView_ReadsTheWorldAndLeavesItAsItWas(t *testing.T) {
	db := writeWorld(t, chunkKey(0, 1, 2, 0x31), chunkKey(1, 3, 4, 0x2c))
	before := names(t, db)
	work := filepath.Join(t.TempDir(), "work")

	view, done, err := OpenView(db, work)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := view.Get(chunkKey(0, 1, 2, 0x31), nil); err != nil || string(v) != "v" {
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

func TestOpenView_RemovesAViewAKilledRunLeft(t *testing.T) {
	db := writeWorld(t, chunkKey(0, 1, 2, 0x2c))
	work := t.TempDir()
	stale := filepath.Join(work, "view-killed")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	_, done, err := OpenView(db, work)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale view still there: %v", err)
	}
}

func TestRecordOf(t *testing.T) {
	for _, c := range []struct {
		name string
		key  []byte
		pos  Pos
		tag  byte
		ok   bool
	}{
		{"overworld block entities", chunkKey(0, 3, -7, 0x31), Pos{Overworld, 3, -7}, 0x31, true},
		{"nether block entities", chunkKey(1, 10, 20, 0x31), Pos{Nether, 10, 20}, 0x31, true},
		{"end subchunk", chunkKey(2, -5, 0, 0x2f, 0xfc), Pos{End, -5, 0}, 0x2f, true},
		{"a named record", []byte("Overworld"), Pos{}, 0, false},
		{"an actor", []byte("actorprefix12345678"), Pos{}, 0, false},
	} {
		pos, tag, ok := RecordOf(c.key)
		if pos != c.pos || tag != c.tag || ok != c.ok {
			t.Errorf("%s: RecordOf = %v, %#x, %v; want %v, %#x, %v", c.name, pos, tag, ok, c.pos, c.tag, c.ok)
		}
	}
}
