package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	console := NewConsole(cfg.ConsoleAddr, cfg.ConsolePassword, cfg.CommandTimeout, logger)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		console.Run(ctx)
	}()

	srv := &server{cfg: cfg, console: console, logger: logger}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newMux(srv),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      responseWriteTimeout(cfg.CommandTimeout),
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("starting", "http_addr", cfg.HTTPAddr, "console_addr", cfg.ConsoleAddr)
	err = httpServer.ListenAndServe()
	stop() // unblocks console.Run's ctx.Done() promptly if ListenAndServe returned on its own
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
