package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

var registrationResults = []string{"pending", "refused", "code_issued", "code_wrong", "abandoned", "cancelled", "expired", "confirmed"}

// The results before the signature verifies are anyone's to make; those
// after it mean a real passkey was turned away, which is what pages.
var loginResults = []string{"ok", "bad_assertion", "unknown_credential", "expired", "pending", "not_operator", "counter", "replayed"}

// adminMetricSet is every D6 series of the server, built by a constructor
// so that a test can hand a server vectors of its own and see exactly what
// it declared.
type adminMetricSet struct {
	registered, removed, logins, requests *prometheus.CounterVec
	passkeys                              *prometheus.GaugeVec
}

func newAdminMetrics(f promauto.Factory) adminMetricSet {
	return adminMetricSet{
		registered: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mcmap_admin_passkeys_registered_total",
			Help: "Steps of adding a passkey, by result: pending, refused, code_issued, code_wrong, abandoned, cancelled, expired, confirmed.",
		}, []string{"result"}),
		removed: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mcmap_admin_passkeys_removed_total",
			Help: "Passkeys removed, by why: player (from the page) or revoked (!map logout or Log out everywhere).",
		}, []string{"why"}),
		logins: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mcmap_admin_logins_total",
			Help: "Passkey logins, by result. bad_assertion, unknown_credential and expired come before the signature is verified; pending, not_operator, counter and replayed after.",
		}, []string{"result"}),
		requests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "mcmap_admin_requests_total",
			Help: "Requests to admin routes, by route pattern and whether the player was an admin.",
		}, []string{"route", "allowed"}),
		passkeys: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mcmap_admin_passkeys",
			Help: "Passkeys held, by state: pending or active. Read back from the volume at start, so a restart does not move it.",
		}, []string{"state"}),
	}
}

var (
	adminMetrics             = newAdminMetrics(promauto.With(prometheus.DefaultRegisterer))
	metricPasskeysRegistered = adminMetrics.registered
	metricAdminLogins        = adminMetrics.logins
	metricPasskeys           = adminMetrics.passkeys
)

// declare puts every series an alert reads at zero before its first event,
// so that the first confirmation on a new pod is an increase.
func (m adminMetricSet) declare() {
	for _, r := range registrationResults {
		m.registered.WithLabelValues(r)
	}
	for _, r := range loginResults {
		m.logins.WithLabelValues(r)
	}
	for _, why := range []string{"player", "revoked"} {
		m.removed.WithLabelValues(why)
	}
	m.passkeys.WithLabelValues("pending")
	m.passkeys.WithLabelValues("active")
}

// declareRoute puts an admin route's two series at zero where it is
// registered.
func (m adminMetricSet) declareRoute(pattern string) {
	m.requests.WithLabelValues(pattern, "true")
	m.requests.WithLabelValues(pattern, "false")
}

// metricSet is the set this server declares into: its own in a test,
// the process's otherwise.
func (s *Server) metricSet() adminMetricSet {
	if s.adminMetrics != nil {
		return *s.adminMetrics
	}
	return adminMetrics
}

func CountPasskeys(pending, active int) {
	metricPasskeys.WithLabelValues("pending").Set(float64(pending))
	metricPasskeys.WithLabelValues("active").Set(float64(active))
}

func CountPasskeyExpired() { metricPasskeysRegistered.WithLabelValues("expired").Inc() }

// StartPasskeyMetrics ties the store to the gauge and the expiry count, and
// reads the gauge from what was loaded, so a restart does not move it.
func StartPasskeyMetrics(keys *auth.Passkeys) {
	keys.Counted, keys.Expired = CountPasskeys, CountPasskeyExpired
	CountPasskeys(keys.Counts())
}
