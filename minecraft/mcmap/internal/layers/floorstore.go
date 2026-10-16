package layers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

var (
	metricFloorChunks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_layers_floor_chunks",
		Help: "Chunks of the Nether whose floors under a level are held, by level.",
	}, []string{"level"})
	metricFloorBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_layers_floor_bytes",
		Help: "About how much memory the Nether's floor maps take, all levels together.",
	})
	metricFloorSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_layers_floor_duration_seconds",
		Help: "How long the last successful reading of the Nether's floors took.",
	})
	metricFloorReads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_layers_floor_readings_total",
		Help: "Cycles by what became of the Nether's floors: read, unchanged (nothing new was generated, so the world was not read), failed.",
	}, []string{"result"})
	metricFloorSaveFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_layers_floor_save_failures_total",
		Help: "Readings of the Nether's floors that could not be written to the volume. They are still served; a restart would start without them.",
	})
)

// Floors is the Nether's floor map of every level, all of one snapshot. It
// is never changed once built.
type Floors struct {
	SnapshotAt   time.Time
	CensusChunks int
	levels       []int
	maps         map[int]*heights.Map
}

// Levels are the cuts the floors were read for, highest first.
func (f *Floors) Levels() []int {
	if f == nil {
		return nil
	}
	return slices.Clone(f.levels)
}

// Level is the floors under a cut, or nil where that cut was not read.
func (f *Floors) Level(cut int) *heights.Map {
	if f == nil {
		return nil
	}
	return f.maps[cut]
}

// Bytes is about how much memory the maps take.
func (f *Floors) Bytes() int {
	n := 0
	if f != nil {
		for _, m := range f.maps {
			n += m.Bytes()
		}
	}
	return n
}

// FloorStore holds the last reading of the Nether's floors.
type FloorStore struct {
	current atomic.Pointer[Floors]
}

// Floors is the last completed reading, or nil before there has been one.
func (s *FloorStore) Floors() *Floors { return s.current.Load() }

// Set replaces the reading held.
func (s *FloorStore) Set(f *Floors) { s.current.Store(f) }

// maxStale is how long a reading stands while the chunk count stays the
// same, as the biomes' does: a count that has not moved generated nothing,
// but building changes floors without changing it, so the world is read
// again within the hour.
const maxStale = time.Hour

// FloorStats is what one reading came to.
type FloorStats struct {
	Chunks int
	// Unchanged means the world was not read: the reading held still stands.
	Unchanged bool
}

// FloorExtractor reads the Nether's floors at each level into a store, and
// keeps them on the volume so that a restart serves them from its first
// request.
type FloorExtractor struct {
	// WorkDir is where a reading builds its view of the world, shared with
	// no other reader. The last reading is kept there too.
	WorkDir string
	// Levels are the cuts to read, as the Nether's levels are configured.
	Levels  []int
	Store   *FloorStore
	Timeout time.Duration
	Logger  *slog.Logger
}

func (e *FloorExtractor) file(cut int) string {
	return filepath.Join(e.WorkDir, "floors-"+strconv.Itoa(cut)+".bin")
}

func (e *FloorExtractor) sorted() []int {
	levels := slices.Clone(e.Levels)
	slices.Sort(levels)
	slices.Reverse(levels)
	return slices.Compact(levels)
}

// Load brings back the reading an earlier run left on the volume, if there
// is a whole one of the levels configured now and of one snapshot. A reading
// of other levels, or with a level missing, is not used: the world is read
// again at the first cycle.
func (e *FloorExtractor) Load() {
	levels := e.sorted()
	if len(levels) == 0 {
		return
	}
	out := &Floors{levels: levels, maps: map[int]*heights.Map{}}
	for i, cut := range levels {
		m, err := heights.Load(e.file(cut))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return
		case err != nil:
			e.Logger.Warn("saved floors not used", "level", cut, "error", err)
			return
		}
		if i == 0 {
			out.SnapshotAt, out.CensusChunks = m.SnapshotAt, m.CensusChunks
		} else if !m.SnapshotAt.Equal(out.SnapshotAt) || m.CensusChunks != out.CensusChunks {
			e.Logger.Warn("saved floors not used: the levels are not of one snapshot")
			return
		}
		out.maps[cut] = m
	}
	e.Store.Set(out)
	export(out)
}

func export(f *Floors) {
	for _, cut := range f.levels {
		metricFloorChunks.WithLabelValues(strconv.Itoa(cut)).Set(float64(f.maps[cut].Chunks(chunks.Nether)))
	}
	metricFloorBytes.Set(float64(f.Bytes()))
}

// Extract reads the floors of the database at dbDir, a snapshot taken at at
// in which the chunk count found censusChunks chunks, or -1 if it could not
// count. A reading that fails leaves the one before in place.
func (e *FloorExtractor) Extract(ctx context.Context, dbDir string, at time.Time, censusChunks int) (FloorStats, error) {
	levels := e.sorted()
	if len(levels) == 0 {
		return FloorStats{Unchanged: true}, nil
	}
	if held := e.Store.Floors(); held != nil && slices.Equal(held.levels, levels) && censusChunks >= 0 &&
		held.CensusChunks == censusChunks && at.Sub(held.SnapshotAt) < maxStale && !at.Before(held.SnapshotAt) {
		metricFloorReads.WithLabelValues("unchanged").Inc()
		return FloorStats{Chunks: held.Level(levels[0]).Chunks(chunks.Nether), Unchanged: true}, nil
	}
	started := time.Now()
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	db, done, err := chunks.OpenView(dbDir, e.WorkDir)
	if err != nil {
		metricFloorReads.WithLabelValues("failed").Inc()
		return FloorStats{}, err
	}
	defer done()
	reader := &Reader{DB: db, Registry: NewRegistry()}
	maps, err := FloorMaps(ctx, reader, chunks.Nether, levels, at)
	if err != nil {
		metricFloorReads.WithLabelValues("failed").Inc()
		return FloorStats{}, err
	}
	out := &Floors{SnapshotAt: at, CensusChunks: censusChunks, levels: levels, maps: map[int]*heights.Map{}}
	for i, cut := range levels {
		maps[i].CensusChunks = censusChunks
		out.maps[cut] = maps[i]
	}
	e.Store.Set(out)
	export(out)
	metricFloorReads.WithLabelValues("read").Inc()
	metricFloorSeconds.Set(time.Since(started).Seconds())
	for _, cut := range levels {
		if err := out.maps[cut].Save(e.file(cut)); err != nil {
			metricFloorSaveFailures.Inc()
			e.Logger.Warn("floors not saved to the volume; a restart would start without them", "level", cut, "error", fmt.Sprint(err))
		}
	}
	return FloorStats{Chunks: out.maps[levels[0]].Chunks(chunks.Nether)}, nil
}
