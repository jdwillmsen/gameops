package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/backfill"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// runBackfill is the backfill subcommand: it keeps a day of history for
// each archive of the world that has none, and gives a day held without
// its markers those of its archive. It reads its own few settings
// and not the service's, which would ask it for a bridge and a login it
// has no use for.
//
// The renderer, its digest and NETHER_TOP_Y have to be the map's own: they
// are recorded with each day, and a day that says it was drawn differently
// from the next is marked as one whose differences are not all the world's.
func runBackfill(getenv func(string) string, logger *slog.Logger) error {
	or := func(name, fallback string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return fallback
	}
	var errs []error
	dir := func(name string) string {
		v := getenv(name)
		if !filepath.IsAbs(v) {
			errs = append(errs, fmt.Errorf("%s must be an absolute path, got %q", name, v))
		}
		return v
	}
	historyDir, archives, work := dir("HISTORY_DIR"), dir("BACKFILL_ARCHIVES"), dir("BACKFILL_WORK")
	level := getenv("LEVEL_NAME")
	if level == "" || strings.ContainsAny(level, `/\`) || level == "." || level == ".." {
		errs = append(errs, fmt.Errorf("LEVEL_NAME must be the world's directory name, got %q", level))
	}
	processors, err := strconv.Atoi(or("RENDER_CHUNK_PROCESSORS", "1"))
	if err != nil || processors < 1 || processors > 64 {
		errs = append(errs, errors.New("RENDER_CHUNK_PROCESSORS must be between 1 and 64"))
	}
	netherTopY, err := strconv.Atoi(or("NETHER_TOP_Y", "100"))
	if err != nil || netherTopY < 1 || netherTopY > 127 {
		errs = append(errs, errors.New("NETHER_TOP_Y must be between 1 and 127"))
	}
	marked, err := strconv.ParseBool(or("MARKERS_ENABLED", "true"))
	if err != nil {
		errs = append(errs, errors.New("MARKERS_ENABLED must be true or false"))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	sha := strings.ToLower(or("UNMINED_SHA256", config.DefaultUnminedSHA256))
	renderer := &render.Unmined{
		Dir:             filepath.Join(or("DATA_DIR", "/data"), "tools", "unmined"),
		URL:             or("UNMINED_URL", config.DefaultUnminedURL),
		SHA256:          sha,
		HTTP:            &http.Client{Timeout: 10 * time.Minute},
		Logger:          logger,
		ChunkProcessors: processors,
		NetherTopY:      netherTopY,
	}
	store, err := history.Open(historyDir, logger)
	if err != nil {
		return err
	}
	store.Source = renderer
	store.Renderer = history.Stamp{SHA256: sha, NetherTopY: netherTopY}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	job := backfill.Job{Archives: archives, Work: work, Level: level, Store: store, Renderer: renderer, Logger: logger}
	// As the map does: one that reads no markers keeps none with its days.
	if marked {
		job.Markers = scanMarkers
	}
	result, err := job.Run(ctx)
	logger.Info("backfill finished", "kept", result.Kept, "amended", result.Amended, "already_held", result.Held, "failed", result.Failed, "unmarked", result.Unmarked, "days", len(store.Days()))
	return err
}

// scanMarkers reads an unpacked world's markers by the scan the map runs
// after each snapshot, into a store of its own, and returns what that
// store would serve for each dimension: the form a day keeps them in.
func scanMarkers(ctx context.Context, db, work string, at time.Time) (map[string][]byte, error) {
	store := markers.NewStore()
	reader := &markers.Extractor{WorkDir: work, Store: store, Timeout: markerScanTimeout}
	if _, err := reader.Extract(ctx, db, at); err != nil {
		return nil, err
	}
	bodies := map[string][]byte{}
	for _, dimension := range render.Dimensions {
		if body, ok := store.Scanned(dimension); ok {
			bodies[dimension] = body
		}
	}
	return bodies, nil
}
