package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// The job writes into the history for good and unpacks whole worlds where
// it is told to, so a place it was not told in full is a refusal and not a
// guess. All of what is wrong is said at once: it is run by hand.
func TestBackfillRefusesToRunWithoutKnowingWhere(t *testing.T) {
	env := map[string]string{
		"HISTORY_DIR":             "history",
		"BACKFILL_WORK":           "/work",
		"LEVEL_NAME":              "../FWB",
		"RENDER_CHUNK_PROCESSORS": "0",
		"MARKERS_ENABLED":         "perhaps",
	}
	err := runBackfill(func(k string) string { return env[k] }, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"HISTORY_DIR", "BACKFILL_ARCHIVES", "LEVEL_NAME", "RENDER_CHUNK_PROCESSORS", "MARKERS_ENABLED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "BACKFILL_WORK") {
		t.Errorf("the error names a setting that was right: %v", err)
	}
}

// What a backfilled day keeps has to be what the map would have served
// from that world, in the same form, or the page could not draw it; and
// the world it is read from is drawn next, so it is left as it was
// unpacked.
func TestBackfillReadsAWorldsMarkersAsTheMapServesThemAndLeavesTheWorldAlone(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db")
	made, err := leveldb.OpenFile(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := made.Put([]byte("~local_player"), []byte{0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := made.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(db, "LOCK")); err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		entries, err := os.ReadDir(db)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	before := names()
	work := filepath.Join(t.TempDir(), "markers")
	at := time.Date(2026, 10, 1, 4, 0, 7, 0, time.UTC)

	bodies, err := scanMarkers(t.Context(), db, work, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, dimension := range render.Dimensions {
		var doc struct {
			At         time.Time
			Beds, Mobs []any
			Containers []any
		}
		if err := json.Unmarshal(bodies[dimension], &doc); err != nil {
			t.Fatalf("%s: %v in %q", dimension, err, bodies[dimension])
		}
		if !doc.At.Equal(at) || doc.Beds == nil || doc.Containers == nil || doc.Mobs == nil {
			t.Errorf("%s = %s, want the archive's time and a list of each kind", dimension, bodies[dimension])
		}
	}
	if after := names(); !slices.Equal(after, before) {
		t.Errorf("the world's database holds %v after the scan, and held %v", after, before)
	}
	if left, _ := os.ReadDir(work); len(left) != 0 {
		t.Errorf("the scan left %d entries in its work directory", len(left))
	}

	if _, err := scanMarkers(t.Context(), filepath.Join(t.TempDir(), "db"), work, at); err == nil {
		t.Error("a world with no database was read")
	}
}
