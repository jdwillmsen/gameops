package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers"
)

type fakeLayerView struct {
	order    *[]string
	dirs     []string
	versions []string
	err      error
}

func (f *fakeLayerView) Refresh(dbDir, version string) error {
	f.dirs, f.versions = append(f.dirs, dbDir), append(f.versions, version)
	if f.order != nil {
		*f.order = append(*f.order, "layers")
	}
	return f.err
}

type fakeFloors struct {
	order  *[]string
	counts []int
	dirs   []string
	stats  layers.FloorStats
	err    error
	panics any
}

func (f *fakeFloors) Extract(_ context.Context, dbDir string, _ time.Time, count int) (layers.FloorStats, error) {
	f.dirs, f.counts = append(f.dirs, dbDir), append(f.counts, count)
	if f.order != nil {
		*f.order = append(*f.order, "floors")
	}
	if f.panics != nil {
		panic(f.panics)
	}
	return f.stats, f.err
}

func TestCycle_OpensTheCaveViewsOnTheSnapshotBeforeTheRendersAndReadsFloorsAfterTheBiomes(t *testing.T) {
	var order []string
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	w.Census = &fakeCensus{order: &order, report: chunks.Report{Present: map[chunks.Dimension]int{chunks.Overworld: 100, chunks.Nether: 20}}}
	w.Structures = &fakeSurveyor{order: &order}
	w.Biomes = &fakeBiomes{order: &order}
	view, floors := &fakeLayerView{order: &order}, &fakeFloors{order: &order}
	w.Layers, w.Floors = view, floors

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v", got)
	}
	// Open to viewers as soon as the world is in, and not after the minutes
	// the renders take; the floors are a pass of their own that nothing waits on.
	if want := []string{"census", "layers", "overworld", "nether", "end", "biomes", "floors", "survey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	db := filepath.Join("/data/mirror", "FWB", "db")
	if !reflect.DeepEqual(view.dirs, []string{db}) || !reflect.DeepEqual(floors.dirs, []string{db}) {
		t.Errorf("views %v, floors %v", view.dirs, floors.dirs)
	}
	if want := noon.Unix(); len(view.versions) != 1 || view.versions[0] != strconv.FormatInt(want, 36) {
		t.Errorf("version %v", view.versions)
	}
	// Only the Nether's chunks say whether its floors changed: exploring
	// the overworld must not read them again.
	if !reflect.DeepEqual(floors.counts, []int{20}) {
		t.Errorf("floors told of %v chunks, want the Nether's 20", floors.counts)
	}
}

// A snapshot that changed no file is the same world, and a new version
// would throw away every tile kept of it.
func TestCycle_ASnapshotThatChangedNothingKeepsTheCaveViewsAsTheyAre(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	view := &fakeLayerView{}
	w.Layers = view
	w.Cycle(context.Background(), noon)
	s.idle = true
	w.Cycle(context.Background(), noon.Add(5*time.Minute))
	if len(view.versions) != 1 {
		t.Errorf("opened %d times over a changed and an idle snapshot", len(view.versions))
	}
	s.idle = false
	w.Cycle(context.Background(), noon.Add(10*time.Minute))
	if len(view.versions) != 2 || view.versions[0] == view.versions[1] {
		t.Errorf("versions %v after the world changed again", view.versions)
	}
}

func TestCycle_AnIdleSnapshotStillOpensTheViewsTheFirstTime(t *testing.T) {
	s, r := &fakeSyncer{idle: true}, &fakeRenderer{}
	w := newWorker(s, r, "")
	view := &fakeLayerView{}
	w.Layers = view
	w.Cycle(context.Background(), noon)
	if len(view.versions) != 1 {
		t.Errorf("opened %d times", len(view.versions))
	}
}

func TestCycle_AViewThatWillNotOpenOrAFloorReadingThatFailsOrPanicsCostsNothingElse(t *testing.T) {
	var order []string
	var log bytes.Buffer
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	w.Structures = &fakeSurveyor{order: &order}
	w.Layers = &fakeLayerView{err: errors.New("no such table")}
	w.Floors = &fakeFloors{order: &order, panics: "index out of range"}
	before := testutil.ToFloat64(metricFloorPanics)

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v", got)
	}
	if want := []string{"overworld", "nether", "end", "floors", "survey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	for _, want := range []string{"cave views not opened", "no such table", "floor reading panicked", "stack="} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("%q is not in the log: %s", want, log.String())
		}
	}
	if testutil.ToFloat64(metricFloorPanics)-before != 1 {
		t.Error("the panic was not counted")
	}
	// A refused opening is retried by the next snapshot even if idle.
	if w.layersOpened {
		t.Error("a view that did not open is taken as open")
	}
	w.Floors = &fakeFloors{err: errors.New("deadline")}
	w.Cycle(context.Background(), noon.Add(time.Minute))
	if !strings.Contains(log.String(), "floors not read") {
		t.Errorf("a failed reading is not in the log: %s", log.String())
	}
}

func TestCycle_SaysWhenFloorsWereNotReadAgain(t *testing.T) {
	var log bytes.Buffer
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	w.Floors = &fakeFloors{stats: layers.FloorStats{Chunks: 19, Unchanged: true}}
	w.Cycle(context.Background(), noon)
	if !strings.Contains(log.String(), "floors unchanged") {
		t.Errorf("not in the log: %s", log.String())
	}
}

// A view that would not open is tried again by the next snapshot, though
// that one found the world as the last left it.
func TestCycle_AViewThatWouldNotOpenIsTriedAgainByAnIdleSnapshot(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	view := &fakeLayerView{}
	w.Layers = view
	w.Cycle(context.Background(), noon)
	// The world changes and the opening fails; the next snapshot finds it
	// as that one left it, and must still try again.
	view.err = errors.New("no such table")
	w.Cycle(context.Background(), noon.Add(5*time.Minute))
	s.idle = true
	w.Cycle(context.Background(), noon.Add(10*time.Minute))
	if len(view.versions) < 3 {
		t.Fatalf("opened %d times, want the failed opening retried", len(view.versions))
	}
	view.err = nil
	w.Cycle(context.Background(), noon.Add(15*time.Minute))
	opened := len(view.versions)
	w.Cycle(context.Background(), noon.Add(20*time.Minute))
	if len(view.versions) != opened {
		t.Error("an idle snapshot reopened a view that was open")
	}
}
