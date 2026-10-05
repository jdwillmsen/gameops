package live

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// What became of a record. The names are the metric's label values.
const (
	resultApplied     = "applied"
	resultBuffered    = "buffered"
	resultHeartbeat   = "heartbeat"
	resultIncomplete  = "incomplete"
	resultDropped     = "dropped"
	resultUnparseable = "unparseable"
	resultNulStripped = "nul_stripped"
)

var lagBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	metricFrames = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_live_frames_total",
		Help: "Records from the script pack, by what became of them: applied (it completed a list, now drawn), buffered (part of a list still arriving), heartbeat, incomplete (a list abandoned with parts missing, counted once per list), dropped (a repeat of a list or part already held), unparseable. nul_stripped counts records that arrived with a NUL byte and is in addition to their outcome.",
	}, []string{"result"})
	metricLastFrame = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_live_last_frame_timestamp_seconds",
		Help: "When the bridge received the newest record this service has accepted, heartbeats included. Old means the pipeline is broken, not that the server is empty.",
	})
	metricEntities = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_live_entities",
		Help: "Entities in the last complete list, by dimension and kind, after this service's own cap.",
	}, []string{"dimension", "kind"})
	metricFrameInterval = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mcmap_live_frame_interval_seconds",
		Help:    "Time between consecutive heartbeats, as the bridge received them.",
		Buckets: []float64{0.5, 0.9, 1.1, 1.5, 2.5, 5.5, 10, 30},
	})
	metricLogLag = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mcmap_live_log_lag_seconds",
		Help:    "From the pack writing a record to the bridge receiving it, across two clocks. Observed only for records that carry the pack's own time.",
		Buckets: lagBuckets,
	})
	metricIngestLag = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mcmap_live_ingest_lag_seconds",
		Help:    "From the bridge receiving a record to this service reading it.",
		Buckets: lagBuckets,
	})
	metricPackScan = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_live_pack_scan_seconds",
		Help: "How long the pack's last sample took on the game server, by its own report.",
	})
	metricPackInterval = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_live_pack_interval_seconds",
		Help: "How often the pack is sampling, by its own report. Above one second it has slowed itself down to protect the server's tick rate.",
	})
	metricPolls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_live_polls_total",
		Help: "Requests to the bridge for records, by outcome: ok, empty (the wait ran out), gap (records were lost between two polls), busy (the bridge had no free waiter), failed.",
	}, []string{"result"})
	metricSubscribers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_live_subscribers",
		Help: "Browsers with a live stream open.",
	})
	metricStreams = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_live_streams_total",
		Help: "Live streams ended, by why: client (it went away), limit (the stream reached its bound, or its session's expiry, and the browser reconnects), write (the browser stopped reading), shutdown.",
	}, []string{"reason"})
	metricFanout = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "mcmap_live_fanout_seconds",
		Help:    "From a frame being ready to it being written to one browser.",
		Buckets: lagBuckets,
	})
	metricFanoutDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_live_fanout_dropped_total",
		Help: "Frames a browser never got because it had not read the one before. The next frame is a whole picture, so nothing is owed.",
	})
)
