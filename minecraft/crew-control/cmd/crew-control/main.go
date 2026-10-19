// Command crew-control serves the public, view-only console for the Crew
// units and proxies an allow-list of each unit's camera routes.
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/proxy"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

// Stamped by the Dockerfile at build time.
var version = "dev"

const (
	shutdownTimeout = 10 * time.Second
	pollEvery       = time.Second
)

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

	poller := telemetry.NewPoller(units, &http.Client{}, pollEvery)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/units", poller.ListHandler)
	mux.HandleFunc("GET /v1/units/stream", poller.StreamHandler)
	mux.Handle("/units/", proxy.Handler(units, log))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// No WriteTimeout: it would cut the camera websocket and the status
	// stream, which are long-lived by design.
	// Shutdown waits for active requests, and an SSE stream never ends on its
	// own; cancelling the base context when shutdown starts ends them at once.
	streamCtx, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return streamCtx },
	}
	srv.RegisterOnShutdown(endStreams)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pollDone := make(chan struct{})
	go func() { poller.Run(ctx); close(pollDone) }()

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
	<-pollDone
}
