// Command crew-control serves the public, view-only console for the Crew
// units and proxies an allow-list of each unit's camera routes.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/proxy"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

// Stamped by the Dockerfile at build time.
var version = "dev"

const shutdownTimeout = 10 * time.Second

func main() {
	log := logging.New(os.Getenv("LOG_LEVEL"))

	units, err := roster.Parse(os.Getenv("CREW_UNITS"))
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8080"
	}

	mux := http.NewServeMux()
	mux.Handle("/units/", proxy.Handler(units, log))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// No WriteTimeout: it would cut the camera websocket and the status
	// stream, which are long-lived by design.
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", logging.Fields{"version": version, "addr": addr, "units": len(units.Units())})

	select {
	case err := <-errc:
		log.Error("serve_failed", logging.Fields{"error": err.Error()})
		os.Exit(1)
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("shutdown_incomplete", logging.Fields{"error": err.Error()})
	}
}
