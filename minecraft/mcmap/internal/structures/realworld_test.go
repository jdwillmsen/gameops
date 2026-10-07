package structures

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// What the save holds inside the real world's structures, as counts and
// never as places or names:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorldDetails -v ./minecraft/mcmap/internal/structures/
//
// The survey is taken twice, with and without the part that reads what the
// world holds, so that what that part costs is the difference.
func TestRealWorldDetails(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Surveyor{WorkDir: t.TempDir(), Logger: quiet}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	survey, err := s.Take(context.Background(), world, started)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	runtime.ReadMemStats(&after)
	t.Logf("survey took %s, of which setting contents inside structures %.4f s; allocated %d MiB in total, heap in use after %d MiB",
		took.Round(time.Millisecond), testutil.ToFloat64(metricDetailSeconds), (after.TotalAlloc-before.TotalAlloc)>>20, after.HeapInuse>>20)
	t.Logf("contents %+v, detailed %v", survey.Contents, survey.Detailed)

	type tally struct {
		structures, withMobs, mobs, named        int
		withSpawners, spawners                   int
		withContainers, unopened, holding, empty int
		blocks                                   map[string]int
		elders                                   map[int]int
		evidence                                 []int
	}
	var villages struct {
		counted, withProfessions, villagers, professed, babies, missing int
		jobSites, withTick, withRaid, standings, withStandings          int
	}
	kinds := map[Kind]*tally{}
	for _, d := range chunks.Dimensions {
		layer := survey.Layers[d]
		for i, r := range layer.Recorded {
			k := kinds[r.Kind]
			if k == nil {
				k = &tally{blocks: map[string]int{}, elders: map[int]int{}}
				kinds[r.Kind] = k
			}
			k.structures++
			if r.Evidence > 0 {
				k.evidence = append(k.evidence, r.Evidence)
			}
			detail := layer.Details[i]
			if detail.MobsTotal > 0 {
				k.withMobs++
			}
			k.mobs += detail.MobsTotal
			k.named += len(detail.Named) + detail.NamedMore
			if n := len(detail.Spawners) + detail.SpawnersMore; n > 0 {
				k.withSpawners++
				k.spawners += n
			}
			for _, c := range detail.Containers {
				k.unopened += c.Unopened
				k.holding += c.Holding
				k.empty += c.Empty
			}
			if len(detail.Containers) > 0 {
				k.withContainers++
			}
			for name, n := range detail.Blocks {
				k.blocks[name] += n
			}
			if detail.Elders != nil {
				k.elders[*detail.Elders]++
			}
			if v := detail.Village; v != nil {
				villages.counted++
				professed := 0
				for _, p := range v.Professions {
					villages.villagers += p.Count
					if p.Profession != "" {
						professed += p.Count
					}
				}
				villages.professed += professed
				if professed > 0 {
					villages.withProfessions++
				}
				villages.babies += v.Babies
				villages.missing += v.Missing
				for _, j := range v.JobSites {
					villages.jobSites += j.Count
				}
				if v.IdleSeconds != nil {
					villages.withTick++
				}
				if v.Raid != nil {
					villages.withRaid++
				}
				villages.standings += v.Met
				if v.Met > 0 {
					villages.withStandings++
				}
			}
		}
	}
	for _, kind := range Kinds {
		k := kinds[kind]
		if k == nil {
			t.Logf("%-13s none", kind)
			continue
		}
		slices.Sort(k.evidence)
		t.Logf("%-13s %d: %d hold saved mobs (%d mobs, %d named); %d hold spawners (%d); %d hold containers (%d unopened, %d holding, %d empty); blocks %v; elders %v; evidence %v",
			kind, k.structures, k.withMobs, k.mobs, k.named, k.withSpawners, k.spawners, k.withContainers, k.unopened, k.holding, k.empty, k.blocks, k.elders, k.evidence)
	}
	t.Logf("villages %+v", villages)
}
