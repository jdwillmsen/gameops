package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

// WatchEventStore exports the event history's health. They are read when
// scraped, so a store that is down is visible without a writer to push it.
func (f *Fleet) WatchEventStore(s *events.Fallback) {
	f.reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "crew_event_store_degraded",
			Help: "1 while the event history is held in memory because the database is not reachable (or not yet reached), 0 while it is written to Postgres. Alert on 1 for more than a few minutes: events held in memory are lost on restart.",
		}, func() float64 {
			if s.Degraded() {
				return 1
			}
			return 0
		}),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "crew_event_store_diverted_events_total",
			Help: "Events written to memory instead of Postgres because the database failed.",
		}, func() float64 { return float64(s.Diverted()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "crew_event_store_replayed_events_total",
			Help: "Diverted events written back to Postgres after it returned.",
		}, func() float64 { return float64(s.Replayed()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "crew_event_store_lost_events_total",
			Help: "Diverted events that were dropped: they outgrew the memory bound before the database returned, or the database rejected them.",
		}, func() float64 { return float64(s.Lost()) }),
	)
}
