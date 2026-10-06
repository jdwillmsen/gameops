package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

type fakeBiomes struct {
	order  *[]string
	dirs   []string
	counts []int
	stats  biomes.Stats
	err    error
}

func (f *fakeBiomes) Extract(_ context.Context, dbDir string, _ time.Time, count int) (biomes.Stats, error) {
	f.dirs = append(f.dirs, dbDir)
	f.counts = append(f.counts, count)
	if f.order != nil {
		*f.order = append(*f.order, "biomes")
	}
	return f.stats, f.err
}

// The count and the retained copy protect the world and the tiles are the
// map; none of them waits on an overlay. The survey comes after, so that
// what it works out can ask about this snapshot's biomes.
func TestCycle_ReadsBiomesAfterTheTilesAndBeforeTheSurvey(t *testing.T) {
	var order []string
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	w.Census = &fakeCensus{order: &order, report: chunks.Report{Present: map[chunks.Dimension]int{chunks.Overworld: 120, chunks.Nether: 30, chunks.End: 3}}}
	w.Structures = &fakeSurveyor{order: &order}
	read := &fakeBiomes{order: &order}
	w.Biomes = read

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v", got)
	}
	if want := []string{"census", "overworld", "nether", "end", "biomes", "survey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if want := []string{filepath.Join("/data/mirror", "FWB", "db")}; !reflect.DeepEqual(read.dirs, want) {
		t.Errorf("read %v, want %v", read.dirs, want)
	}
	// The count is how the reader knows whether anything was generated.
	if !reflect.DeepEqual(read.counts, []int{153}) {
		t.Errorf("told of %v chunks, want 153", read.counts)
	}
}

func TestCycle_ABiomeReadingThatFailsCostsNothingElse(t *testing.T) {
	var order []string
	var log bytes.Buffer
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	w.Structures = &fakeSurveyor{order: &order}
	read := &fakeBiomes{order: &order, err: errors.New("context deadline exceeded")}
	w.Biomes = read

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v, want the cycle to count as applied", got)
	}
	if want := []string{"overworld", "nether", "end", "biomes", "survey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if w.Status.Snapshot().Problem != "" {
		t.Errorf("the page is told of a problem: %q", w.Status.Snapshot().Problem)
	}
	if !strings.Contains(log.String(), "biomes not read") {
		t.Errorf("the failure is not in the log: %s", log.String())
	}
	// With nothing to count the world, the reader is told so and reads it.
	if !reflect.DeepEqual(read.counts, []int{-1}) {
		t.Errorf("told of %v chunks, want -1", read.counts)
	}
}

func TestCycle_SaysWhenBiomesWereNotReadAgain(t *testing.T) {
	var log bytes.Buffer
	w := newWorker(&fakeSyncer{}, &fakeRenderer{}, "")
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	w.Biomes = &fakeBiomes{stats: biomes.Stats{Chunks: 144163, Unchanged: true}}
	w.Cycle(context.Background(), noon)
	if !strings.Contains(log.String(), "biomes unchanged") || strings.Contains(log.String(), "biomes read") {
		t.Errorf("log = %s", log.String())
	}
}

// A snapshot that did not happen leaves nothing new to read.
func TestCycle_ReadsNoBiomesWithoutASnapshot(t *testing.T) {
	w := newWorker(&fakeSyncer{err: errors.New("bridge down")}, &fakeRenderer{}, "")
	read := &fakeBiomes{}
	w.Biomes = read
	w.Cycle(context.Background(), noon)
	if len(read.dirs) != 0 {
		t.Errorf("read %v after a failed snapshot", read.dirs)
	}
}
