package structures

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to check the placement rules after a
// game update, and to measure a survey:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/structures/
//
// MCMAP_STRUCTURE_SEED, if set, is used instead of the seed in level.dat.
// The seed itself is never printed, and neither is where anything is
// predicted: a few sites and the rule that made them are the seed.
//
// The world's biomes are read first, as the service reads them, so that
// each kind's sites are sorted the way they are when it runs. What comes
// out per kind is what the README's account of each rule was written from.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	reader := &biomes.Extractor{WorkDir: t.TempDir(), Store: &biomes.Store{}, Logger: quiet}
	started := time.Now()
	if _, err := reader.Extract(context.Background(), filepath.Join(world, "db"), started, -1); err != nil {
		t.Fatal(err)
	}
	t.Logf("biomes read in %s", time.Since(started).Round(time.Millisecond))
	s := &Surveyor{WorkDir: t.TempDir(), Predictors: Predictors, Logger: quiet, Biomes: func(d chunks.Dimension, x, z int32) (uint32, bool) {
		b, ok := reader.Store.At(d, x, z)
		return b.ID, ok
	}}
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
	started = time.Now()
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
	for _, p := range Predictors {
		k := survey.Check.Kinds[p.Kind()]
		offered := map[bool]int{}
		for _, site := range survey.Layers[p.Dimension()].Predicted {
			if site.Kind == p.Kind() {
				offered[site.Candidate]++
			}
		}
		t.Logf("  %-9s %-10s %d of %d recorded are on a site; %d of %d finished sites that suit it hold one; %d predicted and %d candidates offered, %d findings",
			p.Kind(), k.State, k.Agree, k.Agree+k.Disagree, k.Built, k.Built+k.Empty, offered[false], offered[true], k.Findings)
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
	}
}
