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
		Help: "Attempts to fetch the mob icons, marker pictures and names from the published samples, by result. With icons on, none at all means they were read from the volume; with icons off nothing is ever fetched.",
	}, []string{"result"})
	metricMobTypes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_icons_mob_types",
		Help: "Mob types that have an icon. Zero means the page is drawing every mob as a dot.",
	})
	metricPictures = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_icons_marker_pictures",
		Help: "Marker, structure, face and block pictures held. Zero means the page is drawing every marker as a ring and every structure as a letter, as it also does with icons off.",
	})
	metricNames = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_icons_names",
		Help: "Display names read from the language file. Zero means every name served is a tidied id, or that icons are off and none is served.",
	})
	metricFaults = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_icons_faults_total",
		Help: "Faults caught while making a picture from the samples' models. Each cost one picture, or all the made ones, and is in the log; none stopped the map.",
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
