package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	metricCommandsRun = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mc_console_bridge_commands_run_total",
		Help: "Console commands accepted and sent to the server.",
	})
	metricCommandsRefused = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mc_console_bridge_commands_refused_total",
		Help: "Console commands refused by the allowlist.",
	})
	metricCommandsFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mc_console_bridge_commands_failed_total",
		Help: "Allowlisted console commands that could not be delivered to the server.",
	})
	metricConsoleConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mc_console_bridge_console_connected",
		Help: "Whether the websocket console connection is currently established (1) or not (0).",
	})
	metricConsoleReconnects = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mc_console_bridge_console_reconnects_total",
		Help: "Console connection attempts that ended (successfully established or not) since startup.",
	})
)

func init() {
	prometheus.MustRegister(
		metricCommandsRun, metricCommandsRefused, metricCommandsFailed,
		metricConsoleConnected, metricConsoleReconnects,
	)
}

type server struct {
	cfg     Config
	console *Console
	logger  *slog.Logger
}

func newMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", promhttp.Handler())

	mux.Handle("POST /command", s.authed(s.handleCommand))
	mux.Handle("GET /permissions", s.authed(s.handlePermissions))
	mux.Handle("GET /allowlist", s.authed(s.handleAllowlist))
	mux.Handle("GET /events", s.authed(s.handleEvents))

	return mux
}

func (s *server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, prefix)
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.BridgeToken)) != 1 {
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		h(w, r)
	})
}

// handleHealthz is pure liveness: the process is up and serving. It stays
// green while the console is down, because restarting the bridge cannot fix a
// server that has not opened its console yet.
func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// handleReadyz reports whether the bridge can actually do its job. Without a
// console connection every POST /command fails, so readiness must follow the
// websocket rather than the process.
func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.console.Connected() {
		http.Error(w, "console not connected", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// maxCommandBodyBytes caps POST /command request bodies. The longest
// allowlisted command is a tellraw payload, orders of magnitude under this.
const maxCommandBodyBytes = 16 << 10

type commandRequest struct {
	Command string `json:"command"`
}

type commandResponse struct {
	Rule   string `json:"rule"`
	Output string `json:"output"`
}

func (s *server) handleCommand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCommandBodyBytes)

	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	rule, err := CheckAllowlist(req.Command, s.cfg.Kickable)
	if err != nil {
		metricCommandsRefused.Inc()
		s.logger.Info("command refused", "command", req.Command, "error", err)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	out, err := s.console.SendCommand(r.Context(), req.Command)
	if err != nil {
		metricCommandsFailed.Inc()
		s.logger.Warn("command send failed", "command", req.Command, "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	metricCommandsRun.Inc()
	writeJSONResponse(w, s.logger, commandResponse{Rule: rule, Output: out})
}

func (s *server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(filepath.Join(s.cfg.DataDir, "permissions.json"))
	if err != nil {
		s.handleDataFileOpenError(w, "permissions.json", err)
		return
	}
	defer f.Close()

	perms, err := ParsePermissions(f)
	if err != nil {
		s.logger.Error("permissions.json unparseable", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, s.logger, perms)
}

func (s *server) handleAllowlist(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(filepath.Join(s.cfg.DataDir, "allowlist.json"))
	if err != nil {
		s.handleDataFileOpenError(w, "allowlist.json", err)
		return
	}
	defer f.Close()

	entries, err := ParseAllowlist(f)
	if err != nil {
		s.logger.Error("allowlist.json unparseable", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, s.logger, entries)
}

// handleDataFileOpenError distinguishes "the file genuinely isn't there yet"
// (404, logged at info — expected before the server has written it, or
// during a restore) from any other open failure such as a permissions
// problem on the mounted volume (500, logged at error — worth alerting on).
func (s *server) handleDataFileOpenError(w http.ResponseWriter, name string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		s.logger.Info(name+" not present yet", "error", err)
		http.Error(w, name+" not found", http.StatusNotFound)
		return
	}
	s.logger.Error(name+" unavailable", "error", err)
	http.Error(w, name+" unavailable", http.StatusInternalServerError)
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "since must be an integer event ID", http.StatusBadRequest)
			return
		}
		since = parsed
	}
	writeJSONResponse(w, s.logger, s.console.Events.Since(since))
}

func writeJSONResponse(w http.ResponseWriter, logger *slog.Logger, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already sent, so this can't become an HTTP error
		// response — logging is the only recourse.
		logger.Warn("failed writing JSON response", "error", err)
	}
}
