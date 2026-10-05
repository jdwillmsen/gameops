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

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/generations"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/mirror"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/server"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/worker"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/web"
)

// A waiting login is a couple of hundred bytes, so this costs a few megabytes
// at worst. It is sized so that pushing a real login out of the table takes
// hundreds of requests a second for as long as the player is typing.
const maxPendingLogins = 10_000

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

	// A ledger that cannot be read stops the service rather than starting
	// over: an empty one would take a damaged world as the new normal.
	ledger, err := chunks.OpenLedger(filepath.Join(cfg.DataDir, "chunks", "seen.bin"))
	if err != nil {
		return err
	}
	census := &chunks.Census{WorkDir: filepath.Join(cfg.DataDir, "chunks"), Ledger: ledger}
	census.Publish()

	// Retained world copies, on the same volume as the mirror so that a
	// generation is links to the mirror's files rather than a second copy
	// of them. A store that cannot be opened stops the service: running on
	// with no restore point is the state this exists to end.
	keeper, err := generations.Open(filepath.Join(cfg.DataDir, "generations"), logger)
	if err != nil {
		return err
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
		Census:        census,
		Keeper:        keeper,
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx, cfg.Refresh)
	}()

	app := &server.Server{
		Renderer:    renderer,
		MapsDir:     mapsDir,
		World:       cfg.Level,
		Status:      status,
		Refresh:     cfg.Refresh,
		Static:      web.FS,
		Log:         logger,
		Chunks:      census,
		Generations: keeper,
		// The agent's token guards the internal API whether or not the page
		// has a login; with none configured those routes are not served.
		InternalToken: cfg.InternalToken,
	}
	if cfg.Login {
		key, err := auth.LoadKey(filepath.Join(cfg.DataDir, "auth", "session.key"))
		if err != nil {
			return err
		}
		revoked, err := auth.LoadRevocations(filepath.Join(cfg.DataDir, "auth", "revoked.json"))
		if err != nil {
			return err
		}
		app.Sessions = &auth.Sessions{Key: key, TTL: cfg.SessionTTL, Now: time.Now, Revoked: revoked}
		app.Codes = &auth.Codes{TTL: 10 * time.Minute, Max: maxPendingLogins, Now: time.Now}
	} else {
		logger.Warn("running with no login: anyone who can reach this port sees the whole map")
	}

	serve := func(addr string, handler http.Handler) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}
	public, internal := serve(cfg.HTTPAddr, app.Handler()), serve(cfg.InternalAddr, app.InternalHandler())
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = public.Shutdown(shutdown)
		_ = internal.Shutdown(shutdown)
	}()

	logger.Info("starting", "http_addr", cfg.HTTPAddr, "internal_addr", cfg.InternalAddr, "login", cfg.Login, "level", cfg.Level, "refresh", cfg.Refresh.String(), "quiet_windows", len(cfg.Quiet))
	errs := make(chan error, 2)
	go func() { errs <- internal.ListenAndServe() }()
	go func() { errs <- public.ListenAndServe() }()
	err = <-errs
	stop()
	wg.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
