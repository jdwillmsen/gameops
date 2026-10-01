// Command mcmap serves a web map of a Bedrock world, kept current from the
// running server.
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

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/mirror"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/server"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/worker"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("exiting", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	mirrorDir := filepath.Join(cfg.DataDir, "mirror")
	mapsDir := filepath.Join(cfg.DataDir, "maps")
	for _, dir := range []string{mirrorDir, mapsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	renderer := &render.Unmined{
		Dir:             filepath.Join(cfg.DataDir, "tools", "unmined"),
		URL:             cfg.UnminedURL,
		SHA256:          cfg.UnminedSHA256,
		HTTP:            &http.Client{Timeout: 10 * time.Minute},
		Logger:          logger,
		ChunkProcessors: cfg.ChunkProcessors,
		NetherTopY:      cfg.NetherTopY,
	}
	status := &worker.Status{}
	for _, dimension := range render.Dimensions {
		if at, ok := renderer.RenderedAt(filepath.Join(mapsDir, dimension)); ok {
			status.MarkRendered(dimension, at)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w := &worker.Worker{
		Syncer: &mirror.Mirror{
			Root:  mirrorDir,
			URL:   cfg.BridgeURL,
			Token: cfg.BridgeToken,
			// Longer than the bridge will ever hold saving for one caller, so
			// the bridge's own limit is the one that ends a stalled copy.
			HTTP:   &http.Client{Timeout: 7 * time.Minute},
			Logger: logger,
		},
		Renderer:  renderer,
		MirrorDir: mirrorDir,
		MapsDir:   mapsDir,
		Level:     cfg.Level,
		Quiet:     cfg.Quiet,
		// The bridge waits up to 30s for the save and then holds for at most
		// five minutes; a snapshot started now is over within this.
		Lead:          6 * time.Minute,
		RenderTimeout: 3 * time.Hour,
		Status:        status,
		Logger:        logger,
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx, cfg.Refresh)
	}()

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: (&server.Server{
			Renderer: renderer,
			MapsDir:  mapsDir,
			World:    cfg.Level,
			Status:   status,
			Refresh:  cfg.Refresh,
			Static:   web.FS,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	logger.Info("starting", "http_addr", cfg.HTTPAddr, "level", cfg.Level, "refresh", cfg.Refresh.String(), "quiet_windows", len(cfg.Quiet))
	err = srv.ListenAndServe()
	stop()
	wg.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
