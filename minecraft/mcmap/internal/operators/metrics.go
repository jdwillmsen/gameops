package operators

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricOperators = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_admin_operators",
		Help: "How many operators the map knows of: from the live list, or from the last-known-good one while it is under a day old. A count, never who.",
	})
	metricLookups = newLookups(promauto.With(prometheus.DefaultRegisterer))
)

// newLookups is a constructor so that a test can declare onto a vector of
// its own, where a missing series cannot be hidden by another test's.
func newLookups(f promauto.Factory) *prometheus.CounterVec {
	return f.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_admin_operator_lookups_total",
		Help: "Lookups of the operator list, by where the answer came from: live, last_known_good while the bridge could not be read, or none.",
	}, []string{"result"})
}

func declareLookups(v *prometheus.CounterVec) {
	for _, s := range []Source{Live, LastKnownGood, None} {
		v.WithLabelValues(string(s))
	}
}

func init() { declareLookups(metricLookups) }
