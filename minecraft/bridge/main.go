package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := LoadConfig()
	if err != nil {
		logger.Error("config error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The console outlives the signal: on shutdown a snapshot in flight still
	// has to send its `save resume`, so the websocket is closed last.
	consoleCtx, closeConsole := context.WithCancel(context.Background())
	defer closeConsole()

	console := NewConsole(cfg.ConsoleAddr, cfg.ConsolePassword, cfg.ConsoleOrigin, cfg.CommandTimeout, logger)
	snapshots := newSnapshotter(console, filepath.Join(cfg.DataDir, "worlds"), cfg.SnapshotMaxHold, logger)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		console.Run(consoleCtx)
	}()
	go func() {
		defer wg.Done()
		snapshots.Run(consoleCtx)
	}()

	srv := &server{cfg: cfg, console: console, snapshots: snapshots, logger: logger}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newMux(srv),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      responseWriteTimeout(cfg.CommandTimeout),
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("starting", "http_addr", cfg.HTTPAddr, "console_addr", cfg.ConsoleAddr, "console_origin", cfg.ConsoleOrigin, "kickable_actors", cfg.Kickable.Len())
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()

	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}

	// In this order: end any snapshot and get its resume confirmed, let the
	// other requests finish, and only then drop the console.
	snapshots.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	httpServer.Shutdown(shutdownCtx)
	cancel()
	closeConsole()
	wg.Wait()

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server error", "error", err)
		os.Exit(1)
	}
}

// responseWriteTimeout derives the http.Server write deadline from the
// configured command timeout. POST /command can legitimately occupy
// CommandTimeout on the console write plus the output-collection window, and
// a deadline shorter than that truncates the response of a command the
// console has already run.
func responseWriteTimeout(commandTimeout time.Duration) time.Duration {
	return max(10*time.Second, commandTimeout+commandCollectWindow+5*time.Second)
}
