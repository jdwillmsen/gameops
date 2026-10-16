package layers

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

func netherDB(t *testing.T) string {
	t.Helper()
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Nether, X: 0, Z: 0}, 127, shelves)
	f.chunk(chunks.Pos{Dim: chunks.Nether, X: 1, Z: 0}, 127, shelves)
	return f.closed()
}

func extractor(t *testing.T, levels ...int) *FloorExtractor {
	t.Helper()
	return &FloorExtractor{
		WorkDir: filepath.Join(t.TempDir(), "floors"),
		Levels:  levels,
		Store:   &FloorStore{},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestFloorExtractor_ReadsEachLevelOnceAndKeepsWhatItReadOnTheVolume(t *testing.T) {
	db := netherDB(t)
	e := extractor(t, 66, 100, 34, 100)
	at := time.Unix(1_700_000_000, 0)
	stats, err := e.Extract(context.Background(), db, at, 40)
	if err != nil || stats.Unchanged || stats.Chunks != 2 {
		t.Fatalf("%+v, %v", stats, err)
	}
	got := e.Store.Floors()
	if got == nil || len(got.Levels()) != 3 || got.Levels()[0] != 100 || got.Levels()[2] != 34 {
		t.Fatalf("levels %v", got.Levels())
	}
	if y, ok := got.Level(100).At(chunks.Nether, 3, 3); !ok || y != 70 {
		t.Errorf("the shelf from the top level: %d, %v", y, ok)
	}
	if got.Level(50) != nil || (*Floors)(nil).Level(100) != nil || (*Floors)(nil).Levels() != nil {
		t.Error("a level that was not read, or no reading at all, has a map")
	}
	if got.Bytes() == 0 {
		t.Error("the maps take no room")
	}

	// A restart serves what was kept, with no world to read.
	again := &FloorExtractor{WorkDir: e.WorkDir, Levels: []int{100, 66, 34}, Store: &FloorStore{}, Logger: e.Logger}
	again.Load()
	back := again.Store.Floors()
	if back == nil || !back.SnapshotAt.Equal(at) || back.CensusChunks != 40 {
		t.Fatalf("after a restart: %+v", back)
	}
	if y, ok := back.Level(66).At(chunks.Nether, 12, 3); !ok || y != 30 {
		t.Errorf("the floor from the middle level after a restart: %d, %v", y, ok)
	}

	// Other levels configured now are not the levels kept.
	other := &FloorExtractor{WorkDir: e.WorkDir, Levels: []int{100, 80}, Store: &FloorStore{}, Logger: e.Logger}
	other.Load()
	if other.Store.Floors() != nil {
		t.Error("a reading of other levels was used")
	}
}

func TestFloorExtractor_ASnapshotWithTheSameCountIsNotReadAgainUntilTheHourIsUp(t *testing.T) {
	db := netherDB(t)
	e := extractor(t, 100)
	at := time.Unix(1_700_000_000, 0)
	if _, err := e.Extract(context.Background(), db, at, 40); err != nil {
		t.Fatal(err)
	}
	first := e.Store.Floors()
	stats, err := e.Extract(context.Background(), db, at.Add(5*time.Minute), 40)
	if err != nil || !stats.Unchanged || e.Store.Floors() != first {
		t.Errorf("five minutes on with the same count: %+v, %v", stats, err)
	}
	if stats, _ := e.Extract(context.Background(), db, at.Add(5*time.Minute), 41); stats.Unchanged {
		t.Error("a changed count did not read the world")
	}
	second := e.Store.Floors()
	if second == first {
		t.Fatal("the reading was not replaced")
	}
	if stats, _ := e.Extract(context.Background(), db, at.Add(2*time.Hour), 41); stats.Unchanged || e.Store.Floors() == second {
		t.Error("a reading stood past the hour")
	}
	// A count that could not be taken cannot say nothing changed.
	third := e.Store.Floors()
	if stats, _ := e.Extract(context.Background(), db, at.Add(2*time.Hour+time.Minute), -1); stats.Unchanged || e.Store.Floors() == third {
		t.Error("an uncounted world was taken as unchanged")
	}
}

func TestFloorExtractor_AFailedReadingLeavesTheOneBefore(t *testing.T) {
	db := netherDB(t)
	e := extractor(t, 100)
	at := time.Unix(1_700_000_000, 0)
	if _, err := e.Extract(context.Background(), db, at, 40); err != nil {
		t.Fatal(err)
	}
	held := e.Store.Floors()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Extract(ctx, db, at.Add(3*time.Hour), 99); err == nil {
		t.Error("a cancelled reading succeeded")
	}
	if _, err := e.Extract(context.Background(), filepath.Join(t.TempDir(), "nothing"), at.Add(3*time.Hour), 99); err == nil {
		t.Error("a world that is not there was read")
	}
	if e.Store.Floors() != held {
		t.Error("a failed reading replaced the one before")
	}
}

func TestFloorExtractor_WithNoLevelsDoesNothing(t *testing.T) {
	e := extractor(t)
	stats, err := e.Extract(context.Background(), "/nowhere", time.Now(), 1)
	if err != nil || !stats.Unchanged || e.Store.Floors() != nil {
		t.Errorf("%+v, %v", stats, err)
	}
	e.Load()
}
