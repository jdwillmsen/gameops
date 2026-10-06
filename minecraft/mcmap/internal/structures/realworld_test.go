package structures

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to check the placement rules after a
// game update, and to measure a survey:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/structures/
//
// MCMAP_STRUCTURE_SEED, if set, is used instead of the seed in level.dat.
// The seed itself is never printed.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	s := &Surveyor{WorkDir: t.TempDir(), Predictors: Predictors, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if v := os.Getenv("MCMAP_STRUCTURE_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		seed := uint32(n)
		s.StructureSeed = &seed
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	survey, err := s.Take(context.Background(), world, started)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	t.Logf("took %s, allocated %d MiB in total, heap in use after %d MiB", time.Since(started).Round(time.Millisecond),
		(after.TotalAlloc-before.TotalAlloc)>>20, after.HeapInuse>>20)
	t.Logf("areas %d, malformed %d, unknown %d, over limit %d", survey.Areas, survey.Malformed, survey.Unknown, survey.OverLimit)
	t.Logf("level.dat read: %v, spawn %d, %d (height known: %v)", survey.HasLevel, survey.Level.SpawnX, survey.Level.SpawnZ, survey.Level.SpawnYKnown)
	t.Logf("seed %s: %d recorded structures agree, %d disagree, %d findings", survey.Check.State, survey.Check.Agree, survey.Check.Disagree, survey.Check.Total)
	for _, finding := range survey.Check.Findings {
		t.Log("  finding: ", finding)
	}
	t.Logf("villages %+v, read in %.4f s", survey.Villages, testutil.ToFloat64(metricVillageSeconds))
	for _, d := range chunks.Dimensions {
		layer := survey.Layers[d]
		t.Logf("%s: %d recorded (+%d), %d predicted (+%d)", d.Name(), len(layer.Recorded), layer.RecordedMore, len(layer.Predicted), layer.PredictedMore)
		for _, r := range layer.Recorded {
			if r.Village != nil {
				t.Logf("  recorded %-9s %6d,%4d,%6d to %6d,%4d,%6d  %+v", r.Kind, r.MinX, r.MinY, r.MinZ, r.MaxX, r.MaxY, r.MaxZ, *r.Village)
				continue
			}
			t.Logf("  recorded %-9s %6d,%4d,%6d to %6d,%4d,%6d  areas %d", r.Kind, r.MinX, r.MinY, r.MinZ, r.MaxX, r.MaxY, r.MaxZ, r.Areas)
		}
		for _, p := range layer.Predicted {
			t.Logf("  predicted %-9s %6d,%6d generated=%v", p.Kind, p.X, p.Z, p.Generated)
		}
	}
}
