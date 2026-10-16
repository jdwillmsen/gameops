// Package statusapi serves what crew believes about itself. It is read-only
// and unauthenticated, so it must only be reachable from where crew-control
// and the probes run: config defaults its address to loopback.
package statusapi

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

type Source interface {
	Snapshot() belief.Snapshot
}

type stateBody struct {
	Version string          `json:"version"`
	State   belief.Snapshot `json:"state"`
}

func Handler(src Source, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		s := src.Snapshot()
		if !s.Spawned || s.Dead {
			http.Error(w, "not in the world", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stateBody{Version: version, State: src.Snapshot()})
	})
	// The process-wide registry: packages register their own metrics at init,
	// so this stays the one place metrics are exposed.
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
