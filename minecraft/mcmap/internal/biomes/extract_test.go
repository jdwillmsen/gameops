package biomes

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

func extractor(t *testing.T, log io.Writer) *Extractor {
	t.Helper()
	if log == nil {
		log = io.Discard
	}
	return &Extractor{WorkDir: filepath.Join(t.TempDir(), "biomes"), Store: &Store{}, Timeout: time.Minute, Logger: slog.New(slog.NewTextHandler(log, nil))}
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

func TestExtractorFillsTheStoreAndLeavesTheWorldAlone(t *testing.T) {
	e := extractor(t, nil)
	if e.Store.World() != nil {
		t.Fatal("a store holds a world before any reading")
	}
	if _, ok := e.Store.At(chunks.Overworld, 0, 0); ok {
		t.Fatal("a store answers before any reading")
	}
	dir := plainsAndDesert().write(t, nil)
	before := listing(t, dir)

	stats, err := e.Extract(context.Background(), dir, snapshotAt, 5)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chunks != 5 || stats.Kinds != 2 || stats.Unchanged {
		t.Errorf("stats = %+v", stats)
	}
	if b, ok := e.Store.At(chunks.Overworld, 20, 3); !ok || b.Name != "desert" {
		t.Errorf("At = %+v, %v", b, ok)
	}
	if w := e.Store.World(); !w.SnapshotAt.Equal(snapshotAt) || w.CensusChunks != 5 {
		t.Errorf("world of %s with %d chunks counted", w.SnapshotAt, w.CensusChunks)
	}
	// The mirror holds only the server's files: not a LOCK, not a view.
	if after := listing(t, dir); !slices.Equal(before, after) {
		t.Errorf("the world directory changed: %v, then %v", before, after)
	}
	if left := listing(t, e.WorkDir); !slices.Equal(left, []string{"biomes.bin"}) {
		t.Errorf("the work directory holds %v", left)
	}
}

func TestExtractorKeepsTheLastReadingWhenOneFails(t *testing.T) {
	e := extractor(t, nil)
	if _, err := e.Extract(context.Background(), plainsAndDesert().write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	held := e.Store.World()
	failed := readings("failed")

	if _, err := e.Extract(context.Background(), filepath.Join(t.TempDir(), "no such world"), snapshotAt.Add(15*time.Minute), 9); err == nil {
		t.Fatal("reading a world that is not there succeeded")
	}
	if e.Store.World() != held {
		t.Error("a failed reading replaced the last good one")
	}
	if got := readings("failed"); got != failed+1 {
		t.Errorf("failures counted = %v, want %v", got, failed+1)
	}

	// Out of time before it starts is a failure like any other.
	e.Timeout = time.Nanosecond
	if _, err := e.Extract(context.Background(), plainsAndDesert().write(t, nil), snapshotAt.Add(30*time.Minute), 9); err == nil {
		t.Fatal("a reading past its deadline succeeded")
	}
	if e.Store.World() != held {
		t.Error("a reading given up replaced the last good one")
	}
}

func readings(result string) float64 {
	return testutil.ToFloat64(metricReads.WithLabelValues(result))
}

// A world that has generated nothing since the last reading holds no biome
// that reading did not, so it is not read again until the reading is old.
func TestExtractorDoesNotReadAWorldThatHasNotGrown(t *testing.T) {
	e := extractor(t, nil)
	first := plainsAndDesert()
	if _, err := e.Extract(context.Background(), first.write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	held := e.Store.World()
	// What is on disk now differs, which shows whether it was read.
	grown := plainsAndDesert()
	grown[at(chunks.Overworld, 9, 9)] = columnsOf(all(forest))
	dir := grown.write(t, nil)

	stats, err := e.Extract(context.Background(), dir, snapshotAt.Add(15*time.Minute), 5)
	if err != nil || !stats.Unchanged || stats.Chunks != 5 || e.Store.World() != held {
		t.Fatalf("same count, 15 minutes on: stats %+v, err %v, replaced %v", stats, err, e.Store.World() != held)
	}
	for name, c := range map[string]struct {
		at     time.Time
		census int
	}{
		"the count has grown":          {snapshotAt.Add(15 * time.Minute), 6},
		"nothing counted the world":    {snapshotAt.Add(15 * time.Minute), -1},
		"the reading is an hour old":   {snapshotAt.Add(time.Hour), 5},
		"the snapshot is an older one": {snapshotAt.Add(-time.Minute), 5},
	} {
		e.Store.current.Store(held)
		stats, err := e.Extract(context.Background(), dir, c.at, c.census)
		if err != nil || stats.Unchanged || stats.Chunks != 6 {
			t.Errorf("when %s: stats %+v, err %v; want the world read", name, stats, err)
		}
	}
}

func TestAReadingSurvivesARestart(t *testing.T) {
	e := extractor(t, nil)
	if _, err := e.Extract(context.Background(), plainsAndDesert().write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	next := &Extractor{WorkDir: e.WorkDir, Store: &Store{}, Logger: e.Logger}
	next.Load()
	w := next.Store.World()
	if w == nil {
		t.Fatal("nothing was loaded")
	}
	if !w.SnapshotAt.Equal(snapshotAt) || w.CensusChunks != 5 || w.Stats.Chunks != 5 || w.Stats.Kinds != 2 {
		t.Errorf("loaded a world of %s, census %d, stats %+v", w.SnapshotAt, w.CensusChunks, w.Stats)
	}
	if nameAt(w, chunks.Overworld, 5, 8) != "plains" || nameAt(w, chunks.Overworld, 6, 8) != "desert" || nameAt(w, chunks.Overworld, 99, 8) != "" {
		t.Error("the loaded world answers differently")
	}
	// The index is not saved; it has to have been built again.
	if hits, _ := w.Nearest(chunks.Overworld, plains, 40, 8, 10); len(hits) != 2 || hits[0].X != 48 || hits[1].X != 5 {
		t.Errorf("nearest in the loaded world = %+v", hits)
	}
	// And it is what stops the next cycle reading an unchanged world.
	if stats, err := next.Extract(context.Background(), "/nonexistent", snapshotAt.Add(time.Minute), 5); err != nil || !stats.Unchanged {
		t.Errorf("after a restart: stats %+v, err %v", stats, err)
	}
}

func TestLoadStartsEmptyWithoutAFile(t *testing.T) {
	var log bytes.Buffer
	e := extractor(t, &log)
	e.Load()
	if e.Store.World() != nil || log.Len() != 0 {
		t.Errorf("a first start loaded %v and logged %q", e.Store.World(), log.String())
	}
}

// sealed gives a body the checksum that makes it a file. The body is
// copied: appending to a slice of another would write over what follows.
func sealed(body []byte) []byte {
	return binary.LittleEndian.AppendUint32(slices.Clone(body), crc32.ChecksumIEEE(body))
}

// The saved file is read back into slices that requests then index. One
// that points outside itself has to be refused at the door.
func TestLoadRefusesADamagedFile(t *testing.T) {
	good := scanOf(t, plainsAndDesert(), nil).encode()
	if _, err := decode(good); err != nil {
		t.Fatal(err)
	}
	body := good[:len(good)-4]
	// The first layer: 2 kinds, 5 chunks at twelve bytes, 1 mixed chunk.
	const kindsAt, cellsAt = 24, 24 + 4 + 8 + 4
	mixedColumns := cellsAt + 5*12 + 4
	alter := func(at int, v byte) []byte {
		out := slices.Clone(body)
		out[at] = v
		return sealed(out)
	}
	firstRef := func(ref uint32) []byte {
		out := slices.Clone(body)
		binary.LittleEndian.PutUint32(out[cellsAt+8:], ref)
		return sealed(out)
	}
	for name, raw := range map[string][]byte{
		"empty":                         nil,
		"another format":                sealed(append([]byte("MCBIOME9"), body[8:]...)),
		"a flipped bit":                 append(slices.Clone(good[:40]), append([]byte{good[40] ^ 1}, good[41:]...)...),
		"cut short":                     good[:len(good)-9],
		"cut short and sealed":          sealed(body[:len(body)-5]),
		"something after the end":       sealed(append(slices.Clone(body), 0)),
		"more kinds than there are":     alter(kindsAt, 200),
		"more chunks than there are":    alter(cellsAt-4, 200),
		"a column of no kind":           alter(mixedColumns, 9),
		"a chunk of no kind":            firstRef(7),
		"a chunk with no columns":       firstRef(mixedFlag | 5),
		"more chunks than may be held":  sealed(append(slices.Clone(body[:cellsAt-4]), 0xff, 0xff, 0xff, 0x7f)),
		"more columns than may be held": sealed(append(slices.Clone(body[:mixedColumns-4]), 0xff, 0xff, 0xff, 0x7f)),
	} {
		if w, err := decode(raw); err == nil {
			t.Errorf("%s was loaded: %+v", name, w.Stats)
		}
	}

	var log bytes.Buffer
	e := extractor(t, &log)
	if err := os.MkdirAll(e.WorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.file(), alter(mixedColumns, 9), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Load()
	if e.Store.World() != nil || !strings.Contains(log.String(), "saved biomes not used") {
		t.Errorf("a damaged file was loaded, or not logged: %q", log.String())
	}
}
