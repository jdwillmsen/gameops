package biomes

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// hills is a world whose first air is at a different place in each of
// its chunks: over the sea, in the overworld's negative y, spanning more
// than a byte in one chunk, and in the other two dimensions.
func hills() testWorld {
	slope := func(x, z int) int { return 20 + x*30 + z }
	return testWorld{
		at(chunks.Overworld, 0, 0):  record(flat(127), uniform(ocean)),
		at(chunks.Overworld, -1, 0): record(slope, uniform(plains)),
		at(chunks.Overworld, 0, -1): record(flat(1), uniform(plains)),
		at(chunks.Nether, 0, 0):     record(flat(128), uniform(hell)),
		at(chunks.End, 3, 3):        record(func(x, z int) int { return (x + z) % 2 }, uniform(theEnd)),
	}
}

func TestScanKeepsEachColumnsSurfaceHeightFromTheSameRecords(t *testing.T) {
	h := scanOf(t, hills(), nil).Heights()
	if h == nil {
		t.Fatal("no heights")
	}
	for _, tc := range []struct {
		d    chunks.Dimension
		x, z int32
		y    int32
		ok   bool
	}{
		{chunks.Overworld, 5, 5, 62, true},
		{chunks.Overworld, -16, 0, -45, true},
		// x 15 of the chunk at -1, z 15: 20+450+15 first air, from -64.
		{chunks.Overworld, -1, 15, 20 + 15*30 + 15 - 64 - 1, true},
		{chunks.Overworld, 0, -1, -64, true},
		{chunks.Nether, 7, 7, 127, true},
		{chunks.End, 48, 48, 0, false},
		{chunks.End, 49, 48, 0, true},
		{chunks.Overworld, 1000, 0, 0, false},
	} {
		if y, ok := h.At(tc.d, tc.x, tc.z); y != tc.y || ok != tc.ok {
			t.Errorf("%v %d, %d: %d, %v; want %d, %v", tc.d, tc.x, tc.z, y, ok, tc.y, tc.ok)
		}
	}
	if h.Chunks(chunks.Overworld) != 3 || h.Chunks(chunks.Nether) != 1 || h.Chunks(chunks.End) != 1 {
		t.Errorf("held %d, %d, %d chunks", h.Chunks(chunks.Overworld), h.Chunks(chunks.Nether), h.Chunks(chunks.End))
	}
}

func TestARecordTheBiomePassRefusesGivesNoHeightsEither(t *testing.T) {
	w := scanOf(t, testWorld{
		at(chunks.Overworld, 0, 0): record(flat(100), uniform(plains)),
		at(chunks.Overworld, 1, 0): record(flat(100), []byte{0x7e}),
	}, nil)
	if w.Stats.Malformed != 1 || w.Heights().Chunks(chunks.Overworld) != 1 {
		t.Errorf("%d malformed, %d chunks of heights", w.Stats.Malformed, w.Heights().Chunks(chunks.Overworld))
	}
}

func TestAWorldMadeByBuildHasNoHeightsUntilGivenThem(t *testing.T) {
	w := Build(snapshotAt, map[chunks.Pos][Columns]uint32{at(chunks.Overworld, 0, 0): *columns(all(plains))})
	if w.Heights() != nil || w.Heights().Chunks(chunks.Overworld) != 0 {
		t.Error("a built world has heights")
	}
	var nobody *World
	if nobody.Heights() != nil {
		t.Error("no world has heights")
	}
	b := heights.NewBuilder(snapshotAt, 1)
	_ = b.AddColumns(chunks.Overworld, 0, 0, &[Columns]int16{})
	if got := w.WithHeights(b.Map()); got != w || w.Heights() == nil {
		t.Error("heights not given")
	}
}

// 256 is the size the tile addresses are worked out in and the one the
// API tells the page its tiles are.
func TestBiomeAndHeightTilesAreThePublishedSize(t *testing.T) {
	if TileSize != 256 || heights.TileSize != 256 {
		t.Errorf("biome tiles are %d and height tiles %d, want 256", TileSize, heights.TileSize)
	}
}

func TestHeightsSurviveARestartWithTheirBiomes(t *testing.T) {
	e := extractor(t, nil)
	if _, err := e.Extract(context.Background(), hills().write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	next := &Extractor{WorkDir: e.WorkDir, Store: &Store{}, Logger: e.Logger}
	next.Load()
	if y, ok := next.Store.World().Heights().At(chunks.Overworld, 5, 5); !ok || y != 62 {
		t.Errorf("after a restart: %d, %v", y, ok)
	}
	// Both are held, so a world that has not grown is still not read.
	if stats, err := next.Extract(context.Background(), "/nonexistent", snapshotAt.Add(time.Minute), 5); err != nil || !stats.Unchanged {
		t.Errorf("after a restart: stats %+v, err %v", stats, err)
	}
}

// What production looks like the first time this version starts: a saved
// reading of the biomes and no heights. The first cycle has to read the
// world though its chunk count has not moved.
func TestABiomeReadingWithoutHeightsIsReadAgainAtTheFirstCycle(t *testing.T) {
	e := extractor(t, nil)
	dir := hills().write(t, nil)
	if _, err := e.Extract(context.Background(), dir, snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.heightsFile()); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	next := &Extractor{WorkDir: e.WorkDir, Store: &Store{}, Logger: extractor(t, &log).Logger}
	next.Load()
	if next.Store.World() == nil || next.Store.World().Heights() != nil || log.Len() != 0 {
		t.Fatalf("loaded %v, heights %v, logged %q", next.Store.World(), next.Store.World().Heights(), log.String())
	}
	if _, ok := next.Store.At(chunks.Overworld, 5, 5); !ok {
		t.Error("the biomes are not served from the start")
	}
	stats, err := next.Extract(context.Background(), dir, snapshotAt.Add(time.Minute), 5)
	if err != nil || stats.Unchanged {
		t.Fatalf("the first cycle: stats %+v, err %v", stats, err)
	}
	if y, ok := next.Store.World().Heights().At(chunks.Overworld, 5, 5); !ok || y != 62 {
		t.Errorf("heights after the first cycle: %d, %v", y, ok)
	}
	if _, err := os.Stat(next.heightsFile()); err != nil {
		t.Errorf("not saved: %v", err)
	}
	// And only the once.
	if stats, err := next.Extract(context.Background(), "/nonexistent", snapshotAt.Add(2*time.Minute), 5); err != nil || !stats.Unchanged {
		t.Errorf("the second cycle: stats %+v, err %v", stats, err)
	}
}

func TestHeightsFromAnotherVersionOrAnotherSnapshotAreNotUsed(t *testing.T) {
	saved := extractor(t, nil)
	if _, err := saved.Extract(context.Background(), hills().write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(saved.heightsFile())
	if err != nil {
		t.Fatal(err)
	}
	older := sealed(append([]byte("MCHEIGH0"), good[8:len(good)-4]...))
	other := heights.NewBuilder(snapshotAt.Add(time.Hour), 5).Map().Encode()
	counted := heights.NewBuilder(snapshotAt, 6).Map().Encode()

	for name, c := range map[string]struct {
		file []byte
		logs string
	}{
		"an older version":    {older, "saved heights not used"},
		"garbage":             {[]byte("not a file"), "saved heights not used"},
		"another snapshot":    {other, "not of the snapshot"},
		"another chunk count": {counted, "not of the snapshot"},
	} {
		var log bytes.Buffer
		e := extractor(t, &log)
		if err := os.MkdirAll(e.WorkDir, 0o755); err != nil {
			t.Fatal(err)
		}
		biomesFile, _ := os.ReadFile(saved.file())
		if err := os.WriteFile(e.file(), biomesFile, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.heightsFile(), c.file, 0o600); err != nil {
			t.Fatal(err)
		}
		e.Load()
		if e.Store.World() == nil || e.Store.World().Heights() != nil || !strings.Contains(log.String(), c.logs) {
			t.Errorf("%s: loaded %v, heights %v, logged %q", name, e.Store.World() != nil, e.Store.World().Heights(), log.String())
		}
		// The next reading writes over it.
		if _, err := e.Extract(context.Background(), hills().write(t, nil), snapshotAt.Add(time.Minute), 5); err != nil {
			t.Fatal(err)
		}
		if _, err := heightsOf(e); err != nil {
			t.Errorf("%s: not rebuilt: %v", name, err)
		}
	}
}

func heightsOf(e *Extractor) (*heights.Map, error) { return heights.Load(e.heightsFile()) }

func TestAFailedSaveOfHeightsIsCountedAndTheyAreStillServed(t *testing.T) {
	e := extractor(t, nil)
	if err := os.MkdirAll(e.WorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory where the file goes cannot be renamed over.
	if err := os.MkdirAll(e.heightsFile()+"/inside", 0o755); err != nil {
		t.Fatal(err)
	}
	failures := testutil.ToFloat64(metricSaveFailures)
	if _, err := e.Extract(context.Background(), hills().write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metricSaveFailures); got != failures+1 {
		t.Errorf("save failures = %v, want %v", got, failures+1)
	}
	if y, ok := e.Store.World().Heights().At(chunks.Overworld, 5, 5); !ok || y != 62 {
		t.Errorf("not served: %d, %v", y, ok)
	}
}

func TestHeightsAreCountedByDimension(t *testing.T) {
	e := extractor(t, nil)
	if _, err := e.Extract(context.Background(), hills().write(t, nil), snapshotAt, 5); err != nil {
		t.Fatal(err)
	}
	got := []float64{}
	for _, d := range chunks.Dimensions {
		got = append(got, testutil.ToFloat64(metricHeightChunks.WithLabelValues(d.Name())))
	}
	if !slices.Equal(got, []float64{3, 1, 1}) || testutil.ToFloat64(metricHeightBytes) <= 0 {
		t.Errorf("chunks %v, bytes %v", got, testutil.ToFloat64(metricHeightBytes))
	}
}
