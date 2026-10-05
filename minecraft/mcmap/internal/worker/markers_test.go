package worker

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/generations"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

type fakeMarkers struct {
	order *[]string
	dirs  []string
	ats   []time.Time
	err   error
}

func (f *fakeMarkers) Extract(_ context.Context, dbDir string, at time.Time) (markers.Stats, error) {
	if f.order != nil {
		*f.order = append(*f.order, "markers")
	}
	f.dirs = append(f.dirs, dbDir)
	f.ats = append(f.ats, at)
	return markers.Stats{}, f.err
}

// The count and the retained copy are what protect the world, so neither
// waits on the markers; the renders do, since the markers go on top of them.
func TestCycle_ReadsMarkersAfterTheCountAndTheCopy(t *testing.T) {
	var order []string
	m := &fakeMarkers{order: &order}
	w := newWorker(&fakeSyncer{}, &fakeRenderer{order: &order}, "")
	w.Census, w.Keeper, w.Markers = &fakeCensus{order: &order}, &fakeKeeper{order: &order, outcome: generations.Promoted}, m

	w.Cycle(context.Background(), noon)

	if !reflect.DeepEqual(order, []string{"census", "keep", "markers", "overworld", "nether", "end"}) {
		t.Fatalf("order = %v", order)
	}
	if len(m.dirs) != 1 || m.dirs[0] != filepath.Join("/data/mirror", "FWB", "db") || !m.ats[0].Equal(noon) {
		t.Errorf("read %v at %v", m.dirs, m.ats)
	}
}

func TestCycle_MarkersThatCannotBeReadStopNothing(t *testing.T) {
	r := &fakeRenderer{}
	w := newWorker(&fakeSyncer{}, r, "")
	w.Markers = &fakeMarkers{err: errors.New("deadline exceeded")}

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Errorf("outcome = %v, want applied", got)
	}
	if len(r.rendered) != 3 || w.Status.Snapshot().Problem != "" {
		t.Errorf("rendered %v, problem %q", r.rendered, w.Status.Snapshot().Problem)
	}
}

func TestCycle_NoSnapshotNoMarkerScan(t *testing.T) {
	m := &fakeMarkers{}
	w := newWorker(&fakeSyncer{err: errors.New("bridge down")}, &fakeRenderer{}, "")
	w.Markers = m
	w.Cycle(context.Background(), noon)
	if len(m.dirs) != 0 {
		t.Errorf("scanned %v after a snapshot that failed", m.dirs)
	}
}
