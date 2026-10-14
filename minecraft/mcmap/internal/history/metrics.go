package history

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricCaptures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_history_captures_total",
		Help: "Days of the map offered for keeping, by what happened: captured or failed. A day already held is not counted.",
	}, []string{"result"})
	metricCapturedAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_last_success_timestamp_seconds",
		Help: "The snapshot the newest day of map history was drawn from. More than a day and a cycle old means days are being missed.",
	})
	metricCaptureSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_capture_duration_seconds",
		Help: "How long the last capture took.",
	})
	metricCaptureTiles = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_capture_tiles",
		Help: "Tiles in the last day captured, across dimensions and zooms.",
	})
	metricCaptureChanged = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_capture_tiles_changed",
		Help: "Tiles in the last day captured that differ from the day before it.",
	})
	metricCaptureBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_capture_bytes",
		Help: "Bytes the last day captured added to the store: what a day of history costs.",
	})
	metricDays = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_days",
		Help: "Days of the map held.",
	})
	metricBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_history_bytes",
		Help: "Bytes of tiles and markers held for every day together, each distinct one counted once.",
	})
)
