package chunks

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricPresent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_world_chunks",
		Help: "Chunks in the world at the last count, by dimension.",
	}, []string{"dimension"})
	metricMissing = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_world_chunks_missing",
		Help: "Chunks seen before and absent from the last count, by dimension.",
	}, []string{"dimension"})
	metricLost = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_world_chunks_lost",
		Help: "Chunks found missing since an operator last acknowledged the world, by dimension, including any generated again since. Only an acknowledgement brings it back to zero.",
	}, []string{"dimension"})
	metricCensusAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_world_census_last_success_timestamp_seconds",
		Help: "When the world's chunks were last counted.",
	})
	metricCensusSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_world_census_duration_seconds",
		Help: "How long the last successful count took.",
	})
	metricCensusFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_world_census_failures_total",
		Help: "Counts that could not read the world or record the result.",
	})
)

// Census counts a world's chunks and compares them with the ledger.
type Census struct {
	// WorkDir is where a scan builds its view of the world; on the same
	// filesystem as the mirror, so the view is links rather than a copy.
	WorkDir string
	Ledger  *Ledger

	// Serialises a ledger change with the export of its result, so an
	// acknowledgement cannot be overwritten by a count that read the ledger
	// just before it.
	mu sync.Mutex
}

// Publish exports what the ledger already holds. Called at startup, so the
// gauges carry a remembered loss before the first count of this process.
func (c *Census) Publish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.Ledger.Last(); ok {
		export(r)
	}
}

// Take counts the chunks in the LevelDB at dbDir as of at.
func (c *Census) Take(ctx context.Context, dbDir string, at time.Time) (Report, error) {
	started := time.Now()
	found, err := Scan(ctx, dbDir, c.WorkDir)
	if err != nil {
		metricCensusFailures.Inc()
		return Report{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.Ledger.Observe(found, at)
	if err != nil {
		metricCensusFailures.Inc()
		return Report{}, err
	}
	export(r)
	metricCensusAt.Set(float64(at.Unix()))
	metricCensusSeconds.Set(time.Since(started).Seconds())
	return r, nil
}

// Acknowledge accepts the world as the count taken at checkedAt found it.
func (c *Census) Acknowledge(checkedAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.Ledger.Acknowledge(checkedAt); err != nil {
		return err
	}
	if r, ok := c.Ledger.Last(); ok {
		export(r)
	}
	return nil
}

// Last is the most recent report, if there has been a count.
func (c *Census) Last() (Report, bool) { return c.Ledger.Last() }

func export(r Report) {
	for _, d := range Dimensions {
		metricPresent.WithLabelValues(d.Name()).Set(float64(r.Present[d]))
		metricMissing.WithLabelValues(d.Name()).Set(float64(r.Missing[d]))
		metricLost.WithLabelValues(d.Name()).Set(float64(r.Lost[d]))
	}
}
