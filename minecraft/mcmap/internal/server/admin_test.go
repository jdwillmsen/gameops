package server

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
)

// fakeOperators is the operator list as a set, always read live.
type fakeOperators map[string]bool

func (f fakeOperators) IsOperator(_ context.Context, xuid string) (bool, operators.Source) {
	return f[xuid], operators.Live
}
func (f fakeOperators) Source() operators.Source { return operators.Live }

func withAdmin(t *testing.T, ops fakeOperators) *Server {
	t.Helper()
	s := withLogin(t)
	s.Admin, s.Operators = true, ops
	return s
}

// withPasskeys switches passkeys on for s, against the test origin, and
// says where the store is kept.
func withPasskeys(t *testing.T, s *Server) (*auth.Passkeys, *auth.Ceremonies, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passkeys.json")
	keys, err := auth.LoadPasskeys(path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	cer, err := auth.NewCeremonies("https://map.example", "map.example", s.Sessions.Key, keys, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	s.Passkeys, s.Ceremonies = keys, cer
	s.Sessions.Passkeys, s.Sessions.PasskeyTTL = keys, 12*time.Hour
	return keys, cer, path
}

func TestAdmin_MeSaysSoForAnOperatorOnly(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	h := s.Handler()
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); !strings.Contains(rec.Body.String(), `"admin":true`) {
		t.Errorf("an operator's /api/me = %s", rec.Body)
	}
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{session(s, alex)}); strings.Contains(rec.Body.String(), `admin`) {
		t.Errorf("a member's /api/me = %s", rec.Body)
	}
	s.Admin = false
	if rec := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); strings.Contains(rec.Body.String(), `admin`) {
		t.Errorf("with admin off, /api/me = %s", rec.Body)
	}
}

func TestAdmin_TheGate(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	reached := 0
	gate := s.Sessions.Require(s.requireAdmin("GET /test", func(http.ResponseWriter, *http.Request) { reached++ }))
	if rec := do(gate, "GET", "/test", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d", rec.Code)
	}
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, alex)}); rec.Code != http.StatusForbidden {
		t.Errorf("a member = %d", rec.Code)
	}
	before := testutil.ToFloat64(metricAdminRequests.WithLabelValues("GET /test", "true"))
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK || reached != 1 {
		t.Errorf("an operator = %d, reached %d", rec.Code, reached)
	}
	if testutil.ToFloat64(metricAdminRequests.WithLabelValues("GET /test", "true")) != before+1 {
		t.Error("an admin request was not counted")
	}
	// Demoted: the same cookie, the next lookup.
	s.Operators = fakeOperators{}
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusForbidden {
		t.Errorf("a demoted operator's cookie = %d", rec.Code)
	}
}

// With no operator list to read, nobody is an admin and nothing errors.
func TestAdmin_NoListMeansNobodyAndNoError(t *testing.T) {
	s := withAdmin(t, nil)
	s.Operators = nil
	if rec := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "admin") {
		t.Errorf("/api/me with no list = %d %s", rec.Code, rec.Body)
	}
}

func TestConfig_SaysWhetherThereIsAPasskeyLogin(t *testing.T) {
	s := withAdmin(t, fakeOperators{})
	if rec := do(s.Handler(), "GET", "/api/config", "", nil); rec.Body.String() != "{\"login\":true}\n" {
		t.Errorf("/api/config without passkeys = %q, want it unchanged", rec.Body)
	}
	withPasskeys(t, s)
	if rec := do(s.Handler(), "GET", "/api/config", "", nil); !strings.Contains(rec.Body.String(), `"passkeys":true`) {
		t.Errorf("/api/config with passkeys = %s", rec.Body)
	}
}

// On vectors nobody else touches, so that a value left out shows as a
// missing series instead of being hidden by another test's.
func TestAdminMetrics_EveryLabelIsDeclaredAtZero(t *testing.T) {
	m := newAdminMetrics(promauto.With(nil))
	m.declare()
	for name, want := range map[*prometheus.CounterVec]int{m.registered: len(registrationResults), m.logins: len(loginResults), m.removed: 2} {
		if got := testutil.CollectAndCount(name); got != want {
			t.Errorf("%d series declared, want %d", got, want)
		}
	}
	if got := testutil.CollectAndCount(m.passkeys); got != 2 {
		t.Errorf("%d passkeys gauge series, want pending and active", got)
	}
	if testutil.ToFloat64(m.registered.WithLabelValues("confirmed")) != 0 {
		t.Error("a declared series is not at zero")
	}
}

// Startup declares them: building the handler with admin on, passkeys off,
// as production runs before passkeys are switched on.
func TestAdminMetrics_StartupDeclaresThemWithAdminOnAlone(t *testing.T) {
	s := withAdmin(t, fakeOperators{})
	m := newAdminMetrics(promauto.With(nil))
	s.adminMetrics = &m
	s.Handler()
	if got := testutil.CollectAndCount(m.registered); got != len(registrationResults) {
		t.Errorf("%d registration series after startup, want %d", got, len(registrationResults))
	}
	if got := testutil.CollectAndCount(m.logins); got != len(loginResults) {
		t.Errorf("%d login series after startup, want %d", got, len(loginResults))
	}
	s.Admin = false
	off := newAdminMetrics(promauto.With(nil))
	s.adminMetrics = &off
	s.Handler()
	if got := testutil.CollectAndCount(off.registered); got != 0 {
		t.Errorf("with admin off, %d series were declared", got)
	}
}

// The bridge down, a real operator list, no last-known-good: nobody is an
// admin, and nothing answers 5xx for it.
func TestAdmin_ABridgeThatCannotBeReadMakesNobodyAdminAndNothingFail(t *testing.T) {
	s := withLogin(t)
	list, err := operators.Open(filepath.Join(t.TempDir(), "operators.json"), operators.NewBridge("http://127.0.0.1:1", "tok"), time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	s.Admin, s.Operators = true, list
	reached := false
	gate := s.Sessions.Require(s.requireAdmin("GET /test", func(http.ResponseWriter, *http.Request) { reached = true }))
	if rec := do(gate, "GET", "/test", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusForbidden || reached {
		t.Errorf("the gate with the bridge down = %d", rec.Code)
	}
	if rec := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "admin") {
		t.Errorf("/api/me with the bridge down = %d %s", rec.Code, rec.Body)
	}
}
