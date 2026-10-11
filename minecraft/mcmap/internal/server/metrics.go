package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

// exchangeMalformed is the one outcome the exchange itself never judges: a
// request turned away for how it was made.
const exchangeMalformed = "malformed"

// What became of a request made with a service session.
const (
	serviceAllowed   = "allowed"
	serviceForbidden = "forbidden"
)

var (
	metricServiceExchanges = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_service_exchanges_total",
		Help: "Attempts to exchange the checks' secret for a service session, by result (ok, denied for a wrong secret, limited, locked after repeated wrong ones, malformed) and by where the request came from (private for an address inside the cluster, loopback for a port-forward, other for anything else).",
	}, []string{"result", "source"})
	metricServiceRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_service_requests_total",
		Help: "Requests made with a service session, by whether the route is open to one: allowed, or forbidden for anything that is one player's own or is not a read.",
	}, []string{"result"})
)

// A counter that first appears already above zero shows no increase, so an
// alert on the first wrong secret would never fire. Every series an alert
// reads is therefore at zero from the moment the exchange is served.
func declareServiceMetrics() {
	for _, source := range []string{sourcePrivate, sourceLoopback, sourceOther} {
		for _, result := range []string{auth.ExchangeOK, auth.ExchangeDenied, auth.ExchangeLimited, auth.ExchangeLocked, exchangeMalformed} {
			metricServiceExchanges.WithLabelValues(result, source)
		}
	}
	metricServiceRequests.WithLabelValues(serviceAllowed)
	metricServiceRequests.WithLabelValues(serviceForbidden)
}

// CountServiceRequest is what Sessions.ServiceSeen is set to.
func CountServiceRequest(allowed bool) {
	if allowed {
		metricServiceRequests.WithLabelValues(serviceAllowed).Inc()
		return
	}
	metricServiceRequests.WithLabelValues(serviceForbidden).Inc()
}
