package biomes

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

var (
	metricChunks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_biomes_chunks",
		Help: "Chunks whose biomes are held, by dimension, as of the last reading.",
	}, []string{"dimension"})
	metricKinds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_biomes_kinds",
		Help: "Different biomes held, by whether this version's list names them: known or unknown.",
	}, []string{"listed"})
	metricSkipped = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_biomes_skipped",
		Help: "What the last reading left out or cut down, by reason: malformed, out_of_range, limit, coarsened, kinds, unindexed.",
	}, []string{"reason"})
	metricReadAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_biomes_last_success_timestamp_seconds",
		Help: "When the snapshot the biomes were last read from was taken.",
	})
	metricReadSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_biomes_duration_seconds",
		Help: "How long the last successful reading of the world's biomes took.",
	})
	metricReads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_biomes_readings_total",
		Help: "Cycles by what became of the biomes: read, unchanged (nothing new was generated, so the world was not read), failed.",
	}, []string{"result"})
	metricSaveFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_biomes_save_failures_total",
		Help: "Readings that could not be written to the volume. They are still served; a restart would start without them.",
	})
)

// Store holds the last reading of the world's biomes.
type Store struct {
	current atomic.Pointer[World]
}

// World is the last completed reading, or nil before there has been one.
// What it returns is never changed afterwards.
func (s *Store) World() *World { return s.current.Load() }

// Set replaces the reading held.
func (s *Store) Set(w *World) { s.current.Store(w) }

// At is the biome at the top of the column at block x, z of a dimension,
// from the last completed reading. It is false before the first reading,
// and for a column in a chunk the world has not generated: only the seed
// knows what that will be, and nothing here reads the seed.
func (s *Store) At(d chunks.Dimension, x, z int32) (Biome, bool) {
	return s.World().At(d, x, z)
}

// maxStale is how long a reading stands while the chunk count stays the
// same. A world whose count has not moved has generated nothing, which is
// the only thing that adds a biome, so it is not read again. The count
// cannot see a chunk that had no biomes when it was first counted and has
// them now, and that is what this is for.
const maxStale = time.Hour

// Extractor reads a freshly mirrored world's biomes into a Store.
type Extractor struct {
	// WorkDir is where a reading builds its view of the world; on the same
	// filesystem as the mirror, and shared with no other reader. The last
	// reading is kept there too.
	WorkDir string
	Store   *Store
	// Timeout bounds one reading. One that takes longer is given up and
	// the reading before it stays; zero means no bound.
	Timeout time.Duration
	Logger  *slog.Logger
}

func (e *Extractor) file() string { return filepath.Join(e.WorkDir, "biomes.bin") }

// Load brings back the reading an earlier run left on the volume, if
// there is a whole one. Its absence is the first start; damage is logged
// and costs only the wait for the first cycle.
func (e *Extractor) Load() {
	w, err := load(e.file())
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		e.Logger.Warn("saved biomes not used", "error", err)
	default:
		e.Store.Set(w)
		export(w)
	}
}

// Extract reads the biomes of the LevelDB at dbDir, a snapshot taken at at
// in which the chunk count found censusChunks chunks, or -1 if it could
// not count. A reading that fails leaves the one before in place.
func (e *Extractor) Extract(ctx context.Context, dbDir string, at time.Time, censusChunks int) (Stats, error) {
	if held := e.Store.World(); held != nil && censusChunks >= 0 && held.CensusChunks == censusChunks &&
		at.Sub(held.SnapshotAt) < maxStale && !at.Before(held.SnapshotAt) {
		metricReads.WithLabelValues("unchanged").Inc()
		stats := held.Stats
		stats.Unchanged = true
		return stats, nil
	}
	started := time.Now()
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	w, err := e.scan(ctx, dbDir, at, censusChunks)
	if err != nil {
		metricReads.WithLabelValues("failed").Inc()
		return Stats{}, err
	}
	e.Store.Set(w)
	export(w)
	metricReads.WithLabelValues("read").Inc()
	metricReadSeconds.Set(time.Since(started).Seconds())
	if err := w.save(e.file()); err != nil {
		metricSaveFailures.Inc()
		e.Logger.Warn("biomes not saved to the volume; a restart would start without them", "error", err)
	}
	return w.Stats, nil
}

func (e *Extractor) scan(ctx context.Context, dbDir string, at time.Time, censusChunks int) (*World, error) {
	db, done, err := chunks.OpenView(dbDir, e.WorkDir)
	if err != nil {
		return nil, err
	}
	defer done()
	return Scan(ctx, db, at, censusChunks)
}

func export(w *World) {
	for _, d := range chunks.Dimensions {
		metricChunks.WithLabelValues(d.Name()).Set(float64(len(w.layers[d].cells)))
	}
	s := w.Stats
	metricKinds.WithLabelValues("known").Set(float64(s.Kinds - s.Unknown))
	metricKinds.WithLabelValues("unknown").Set(float64(s.Unknown))
	for reason, n := range map[string]int{
		"malformed": s.Malformed, "out_of_range": s.OutOfRange, "limit": s.OverLimit,
		"coarsened": s.Coarsened, "kinds": s.KindsLeftOut, "unindexed": s.Unindexed,
	} {
		metricSkipped.WithLabelValues(reason).Set(float64(n))
	}
	metricReadAt.Set(float64(w.SnapshotAt.Unix()))
}
