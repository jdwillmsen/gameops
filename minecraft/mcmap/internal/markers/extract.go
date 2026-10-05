package markers

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

var (
	metricMarkers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_markers",
		Help: "Markers read from the world at the last scan and served, by dimension and kind.",
	}, []string{"dimension", "kind"})
	metricLeftOut = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_markers_left_out",
		Help: "Markers the last scan found beyond the limit for their kind, by dimension and kind.",
	}, []string{"dimension", "kind"})
	metricScanAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_markers_last_success_timestamp_seconds",
		Help: "When the world's markers were last read.",
	})
	metricScanSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_markers_duration_seconds",
		Help: "How long the last successful marker scan took.",
	})
	metricScanFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_markers_failures_total",
		Help: "Marker scans that could not read the world or ran out of time.",
	})
)

// Extractor reads a freshly mirrored world's markers into a Store.
type Extractor struct {
	// WorkDir is where a scan builds its view of the world; on the same
	// filesystem as the mirror, so the view is links rather than a copy,
	// and shared with no other reader.
	WorkDir string
	Store   *Store
	// Timeout bounds one scan, which runs between the snapshot and the
	// renders. A scan that takes longer is given up and the markers of
	// the one before stay; zero means no bound.
	Timeout time.Duration
}

// Extract scans the LevelDB at dbDir, a snapshot taken at at. A scan that
// fails leaves the store as it was.
func (e *Extractor) Extract(ctx context.Context, dbDir string, at time.Time) (Stats, error) {
	started := time.Now()
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	world, stats, err := e.scan(ctx, dbDir)
	if err != nil {
		metricScanFailures.Inc()
		return Stats{}, err
	}
	e.Store.Set(at, world)
	for _, d := range chunks.Dimensions {
		l := world[d]
		for kind, n := range map[string][2]int{
			"bed":       {len(l.Beds), l.More.Beds},
			"container": {len(l.Containers), l.More.Containers},
			"mob":       {len(l.Mobs), l.More.Mobs},
		} {
			metricMarkers.WithLabelValues(d.Name(), kind).Set(float64(n[0]))
			metricLeftOut.WithLabelValues(d.Name(), kind).Set(float64(n[1]))
		}
	}
	metricScanAt.Set(float64(at.Unix()))
	metricScanSeconds.Set(time.Since(started).Seconds())
	return stats, nil
}

func (e *Extractor) scan(ctx context.Context, dbDir string) (World, Stats, error) {
	db, done, err := chunks.OpenView(dbDir, e.WorkDir)
	if err != nil {
		return nil, Stats{}, err
	}
	defer done()
	return Scan(ctx, db)
}
