package players

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/pkg/playerdata"
)

var (
	metricPlayers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_player_records",
		Help: "Player records read from the world at the last scan.",
	})
	metricFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_player_scan_failures_total",
		Help: "Player scans that could not read the world or ran out of time.",
	})
)

// Extractor reads a freshly mirrored world's player records into a Store.
type Extractor struct {
	// WorkDir is where a scan builds its view of the world, as the other
	// readers' is: on the mirror's filesystem and shared with no one.
	WorkDir string
	Store   *Store
	// Timeout bounds one scan; zero means no bound.
	Timeout time.Duration
}

// Extract reads the records of the snapshot at dbDir, taken at at. A scan
// that fails leaves the store as it was.
func (e *Extractor) Extract(ctx context.Context, dbDir string, at time.Time) (Stats, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	db, done, err := chunks.OpenView(dbDir, e.WorkDir)
	if err != nil {
		metricFailures.Inc()
		return Stats{}, err
	}
	defer done()
	found, stats, err := playerdata.Read(ctx, db)
	if err != nil {
		metricFailures.Inc()
		return Stats{}, err
	}
	e.Store.Set(at, found)
	metricPlayers.Set(float64(stats.Players))
	return stats, nil
}
