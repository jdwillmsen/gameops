package players

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// record is the smallest NBT a player's record can be: a decoder that reads
// it wants only the id.
func record(t testing.TB, id int64) []byte {
	t.Helper()
	b, err := nbt.MarshalEncoding(map[string]any{"UniqueID": id}, nbt.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// world builds a LevelDB holding entries, closed and without a LOCK file,
// the way the mirror sits on disk.
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

func TestExtractReadsOnlyPlayerRecordsFromTheMirror(t *testing.T) {
	db := world(t, map[string][]byte{
		"player_server_aaaa":   record(t, 11),
		"player_server_bbbb":   record(t, -12),
		"player_server_broken": []byte("garbage"),
		"player_0000-0000":     record(t, 30),
		"~local_player":        record(t, 13),
		"unrelated":            []byte("x"),
	})
	var s Store
	e := &Extractor{WorkDir: t.TempDir(), Store: &s}
	at := time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)
	stats, err := e.Extract(context.Background(), db, at)
	if err != nil || stats.Players != 2 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	for _, id := range []string{"11", "-12"} {
		if _, when, ok := s.Lookup(id); !ok || !when.Equal(at) {
			t.Errorf("player %s not held: %v %v", id, ok, when)
		}
	}
	if _, _, ok := s.Lookup("13"); ok {
		t.Error("the local player's record was read")
	}
	if _, err := os.Stat(filepath.Join(db, "LOCK")); err == nil {
		t.Error("the mirror was opened in place and left a lock behind")
	}
}

func TestAFailedScanLeavesThePlayersOfTheLastOne(t *testing.T) {
	var s Store
	s.Set(time.Now(), map[int64]Record{5: {}})
	e := &Extractor{WorkDir: t.TempDir(), Store: &s}
	if _, err := e.Extract(context.Background(), filepath.Join(t.TempDir(), "missing"), time.Now()); err == nil {
		t.Fatal("a world that is not there was read")
	}
	if _, _, ok := s.Lookup("5"); !ok {
		t.Error("the last scan's players were lost")
	}
}
