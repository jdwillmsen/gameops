// Package worker runs the map's refresh cycle: mirror the world, then render
// each dimension from the mirror.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/mirror"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/schedule"
)

var (
	metricSnapshots = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_snapshots_total",
		Help: "Refresh cycles, by what happened to the world snapshot.",
	}, []string{"result"})
	metricSnapshotAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_snapshot_last_success_timestamp_seconds",
		Help: "When the mirror last matched the server's saved world.",
	})
	metricSnapshotBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_snapshot_bytes_total",
		Help: "World file bytes fetched from the bridge.",
	})
	metricSnapshotSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_snapshot_duration_seconds",
		Help: "How long the last successful snapshot took end to end.",
	})
	metricRenderAt = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_render_last_success_timestamp_seconds",
		Help: "When a dimension's tiles were last brought up to date.",
	}, []string{"dimension"})
	metricRenderSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_render_duration_seconds",
		Help: "How long a dimension's last successful render took.",
	}, []string{"dimension"})
	metricRenderFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_render_failures_total",
		Help: "Renders that failed, by dimension.",
	}, []string{"dimension"})
)

// Census counts the chunks in a freshly mirrored world.
type Census interface {
	Take(ctx context.Context, dbDir string, at time.Time) (chunks.Report, error)
}

type Syncer interface {
	Sync(ctx context.Context) (mirror.Stats, error)
}

type Worker struct {
	Syncer   Syncer
	Renderer render.Renderer
	// MirrorDir holds the world directories; MapsDir one directory of tiles
	// per dimension.
	MirrorDir string
	MapsDir   string
	Level     string
	Quiet     schedule.Quiet
	// Lead is how long before a quiet window the map already stays away: the
	// longest a snapshot started now could still be holding the world.
	Lead time.Duration
	// RenderTimeout bounds one dimension's render; zero means no bound.
	RenderTimeout time.Duration
	Status        *Status
	Logger        *slog.Logger
	// Census, if set, counts the world's chunks after every snapshot.
	Census Census
}

// Status is what the last cycles achieved, for the web page to report.
type Status struct {
	mu         sync.Mutex
	snapshotAt time.Time
	renderedAt map[string]time.Time
	problem    string
}

type StatusSnapshot struct {
	SnapshotAt time.Time
	RenderedAt map[string]time.Time
	// Problem is which stage the last cycle failed in, "snapshot" or
	// "render", or empty. The detail goes to the log only: it names internal
	// addresses and paths, and this value is served to browsers.
	Problem string
}

func (s *Status) Snapshot() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusSnapshot{SnapshotAt: s.snapshotAt, RenderedAt: maps.Clone(s.renderedAt), Problem: s.problem}
}

// MarkProblem records which stage is failing; empty clears it.
func (s *Status) MarkProblem(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.problem = stage
}

// MarkRendered records a render that finished at t. It is also how the
// service restores what an earlier run left on disk.
func (s *Status) MarkRendered(dimension string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renderedAt == nil {
		s.renderedAt = map[string]time.Time{}
	}
	s.renderedAt[dimension] = t
}

func (s *Status) set(fn func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// Outcome is how a cycle ended, which decides how soon the next one runs.
type Outcome int

const (
	Applied Outcome = iota
	Quiet
	Busy
	Failed
)

func (o Outcome) String() string {
	return [...]string{"applied", "quiet", "busy", "failed"}[o]
}

// retryAfter is how soon a cycle that mirrored nothing is tried again.
const retryAfter = time.Minute

// Run cycles immediately and then every interval until ctx ends, sooner
// after a cycle that got nowhere.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	failures := 0
	for {
		outcome := w.Cycle(ctx, time.Now())
		if outcome == Failed {
			failures++
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(nextDelay(outcome, failures, interval, retryAfter)):
		}
	}
}

// nextDelay is the wait before the next cycle. A refusal is usually over in
// seconds (another copy finishing), so it is retried soon. So is a first
// failure (a bridge still connecting after a restart), but each failure
// after that doubles the wait up to the normal pace: a fault that does not
// clear must not turn into a pause of the live world every minute. A quiet
// window ends on the clock, so it keeps the normal pace.
func nextDelay(outcome Outcome, failures int, interval, retry time.Duration) time.Duration {
	switch outcome {
	case Busy:
		return min(interval, retry)
	case Failed:
		delay := retry
		for i := 1; i < failures && delay < interval; i++ {
			delay *= 2
		}
		return min(interval, delay)
	}
	return interval
}

func (w *Worker) render(ctx context.Context, dimension string) error {
	if w.RenderTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.RenderTimeout)
		defer cancel()
	}
	return w.Renderer.Render(ctx, filepath.Join(w.MirrorDir, w.Level), dimension, filepath.Join(w.MapsDir, dimension))
}

// Cycle runs one refresh. now is passed in so the quiet windows and the
// recorded times come from one reading of the clock.
func (w *Worker) Cycle(ctx context.Context, now time.Time) Outcome {
	if w.Quiet.Contains(now) || w.Quiet.Contains(now.Add(w.Lead)) {
		metricSnapshots.WithLabelValues("quiet").Inc()
		w.Logger.Info("refresh skipped: inside or too close to a quiet window")
		return Quiet
	}

	if err := w.Renderer.Prepare(ctx); err != nil {
		metricSnapshots.WithLabelValues("failed").Inc()
		w.Logger.Error("renderer unavailable; not taking a snapshot it could not use", "error", err)
		w.Status.MarkProblem("render")
		return Failed
	}

	stats, err := w.Syncer.Sync(ctx)
	switch {
	case errors.Is(err, mirror.ErrBusy):
		metricSnapshots.WithLabelValues("busy").Inc()
		w.Logger.Info("refresh skipped: the bridge is busy or saving is paused elsewhere")
		return Busy
	case err != nil:
		metricSnapshots.WithLabelValues("failed").Inc()
		w.Logger.Error("snapshot failed", "error", err)
		w.Status.MarkProblem("snapshot")
		return Failed
	}
	metricSnapshots.WithLabelValues("ok").Inc()
	metricSnapshotAt.Set(float64(now.Unix()))
	metricSnapshotBytes.Add(float64(stats.Bytes))
	metricSnapshotSeconds.Set(stats.Duration.Seconds())
	w.Logger.Info("snapshot applied", "files", stats.Files, "fetched", stats.Fetched, "removed", stats.Removed, "bytes", stats.Bytes, "seconds", stats.Duration.Seconds())
	w.Status.set(func(s *Status) { s.snapshotAt = now })
	w.census(ctx, now)

	problem := ""
	for _, dimension := range render.Dimensions {
		started := time.Now()
		err := w.render(ctx, dimension)
		if err != nil {
			metricRenderFailures.WithLabelValues(dimension).Inc()
			w.Logger.Error("render failed", "dimension", dimension, "error", err)
			problem = "render"
			continue
		}
		took := time.Since(started)
		metricRenderAt.WithLabelValues(dimension).Set(float64(now.Unix()))
		metricRenderSeconds.WithLabelValues(dimension).Set(took.Seconds())
		w.Logger.Info("rendered", "dimension", dimension, "seconds", took.Seconds())
		w.Status.MarkRendered(dimension, now)
	}
	w.Status.MarkProblem(problem)
	return Applied
}

// census runs before the renders, which take minutes: a world losing chunks
// is the one thing this service sees that cannot wait for them. Its failure
// is logged and counted but stops nothing else.
func (w *Worker) census(ctx context.Context, now time.Time) {
	if w.Census == nil {
		return
	}
	r, err := w.Census.Take(ctx, filepath.Join(w.MirrorDir, w.Level, "db"), now)
	if err != nil {
		w.Logger.Error("chunk census failed", "error", err)
		return
	}
	if lost := r.TotalLost(); lost > 0 {
		attrs := []any{"lost", lost, "missing_now", r.TotalMissing()}
		for _, d := range chunks.Dimensions {
			attrs = append(attrs, d.Name(), r.Lost[d])
		}
		// Block coordinates, which is what a player or the map shows.
		if len(r.Sample) > 0 {
			first := r.Sample[0]
			attrs = append(attrs, "first_dimension", first.Dim.Name(), "block_x", first.X*16, "block_z", first.Z*16)
		}
		w.Logger.Error("world has lost chunks", attrs...)
		return
	}
	w.Logger.Info("chunk census", "overworld", r.Present[chunks.Overworld], "nether", r.Present[chunks.Nether], "end", r.Present[chunks.End])
}
