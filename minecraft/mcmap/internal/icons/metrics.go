package icons

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The label values of the fetch metric.
const (
	resultOK     = "ok"
	resultFailed = "failed"
)

var (
	metricFetches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_icons_fetches_total",
		Help: "Attempts to fetch the mob icons from the published samples, by result. None at all means they were read from the volume.",
	}, []string{"result"})
	metricMobTypes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_icons_mob_types",
		Help: "Mob types that have an icon. Zero means the page is drawing every mob as a dot.",
	})
	metricHeads = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_icons_player_heads",
		Help: "Online players the agent has reported a head for.",
	})
	metricHeadsRefused = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_icons_player_heads_refused_total",
		Help: "Heads the agent reported that were not a small square PNG and were not kept.",
	})
)
