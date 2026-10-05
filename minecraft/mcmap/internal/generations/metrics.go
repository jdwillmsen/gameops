package generations

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// failedLabel counts a capture that could not be completed. It is not an
// Outcome: nothing was retained, so there is nothing for a caller to act on
// beyond the error it already has.
const failedLabel = "failed"

var (
	metricCaptures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_generation_captures_total",
		Help: "Snapshots offered for retention, by what was done with them: promoted to the restore point, quarantined for having lost chunks, skipped because a damaged snapshot is already quarantined, or failed.",
	}, []string{"outcome"})
	metricRetained = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_generations",
		Help: "Complete world generations held, 0 to 2. Below 1 there is no near-current restore point.",
	})
	metricDamaged = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_generation_damaged",
		Help: "1 while a snapshot that lost chunks is quarantined, which is also while no snapshot is being promoted.",
	})
	metricCurrentAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_generation_current_timestamp_seconds",
		Help: "When the snapshot now serving as the restore point was taken. The distance from now is how far a restore from it would roll the world back.",
	})
	metricBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_generation_bytes",
		Help: "World bytes a retained copy names. Generations are hard links, so copies share every file neither the server nor compaction has changed and this is not the space the copy adds.",
	}, []string{"generation"})
)
