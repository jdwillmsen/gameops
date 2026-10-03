package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/mirror"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/schedule"
)

type fakeSyncer struct {
	calls int
	err   error
}

func (f *fakeSyncer) Sync(context.Context) (mirror.Stats, error) {
	f.calls++
	return mirror.Stats{Files: 3, Fetched: 1, Bytes: 42}, f.err
}

type fakeCensus struct {
	dirs   []string
	order  *[]string
	err    error
	report chunks.Report
}

func (f *fakeCensus) Take(_ context.Context, dbDir string, at time.Time) (chunks.Report, error) {
	f.dirs = append(f.dirs, dbDir)
	if f.order != nil {
		*f.order = append(*f.order, "census")
	}
	r := f.report
	r.At = at
	return r, f.err
}

type fakeRenderer struct {
	order      *[]string
	rendered   []string
	worlds     []string
	outs       []string
	failOn     string
	prepareErr error
	prepared   int
}

func (f *fakeRenderer) Prepare(context.Context) error {
	f.prepared++
	return f.prepareErr
}

func (f *fakeRenderer) Render(_ context.Context, world, dimension, out string) error {
	f.rendered = append(f.rendered, dimension)
	if f.order != nil {
		*f.order = append(*f.order, dimension)
	}
	f.worlds = append(f.worlds, world)
	f.outs = append(f.outs, out)
	if dimension == f.failOn {
		return errors.New("render broke")
	}
	return nil
}

func (f *fakeRenderer) Info(string) (render.Info, error)              { return render.Info{}, nil }
func (f *fakeRenderer) TilePath(string, string, int, int, int) string { return "" }

func newWorker(s *fakeSyncer, r *fakeRenderer, quiet string) *Worker {
	q, err := schedule.ParseQuiet(quiet)
	if err != nil {
		panic(err)
	}
	return &Worker{
		Syncer:    s,
		Renderer:  r,
		MirrorDir: "/data/mirror",
		MapsDir:   "/data/maps",
		Level:     "FWB",
		Quiet:     q,
		Status:    &Status{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

var noon = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func TestCycle_SyncsThenRendersEveryDimension(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")

	w.Cycle(context.Background(), noon)

	if s.calls != 1 || !reflect.DeepEqual(r.rendered, []string{"overworld", "nether", "end"}) {
		t.Fatalf("sync calls %d, rendered %v", s.calls, r.rendered)
	}
	if r.worlds[0] != filepath.Join("/data/mirror", "FWB") || r.outs[1] != filepath.Join("/data/maps", "nether") {
		t.Errorf("rendered world %q into %q", r.worlds[0], r.outs[1])
	}
	snap := w.Status.Snapshot()
	if !snap.SnapshotAt.Equal(noon) || !snap.RenderedAt["end"].Equal(noon) || snap.Problem != "" {
		t.Errorf("status = %+v", snap)
	}
}

// Inside a quiet window another job may be holding the world's save state
// through a channel the bridge cannot see, so the bridge is not even asked.
func TestCycle_QuietWindowTouchesNothing(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "11:30-12:30")

	w.Cycle(context.Background(), noon)

	if s.calls != 0 || len(r.rendered) != 0 {
		t.Fatalf("sync calls %d, rendered %v during a quiet window", s.calls, r.rendered)
	}
}

// A refusal means the mirror did not change, so there is nothing new to
// render, and it is not a fault worth reporting.
func TestCycle_BusyBridgeSkipsRenderingWithoutAnError(t *testing.T) {
	s, r := &fakeSyncer{err: mirror.ErrBusy}, &fakeRenderer{}
	w := newWorker(s, r, "")

	w.Cycle(context.Background(), noon)

	if len(r.rendered) != 0 {
		t.Errorf("rendered %v after a refused snapshot", r.rendered)
	}
	if snap := w.Status.Snapshot(); snap.Problem != "" || !snap.SnapshotAt.IsZero() {
		t.Errorf("status = %+v, want no error and no snapshot time", snap)
	}
}

func TestCycle_FailedSyncIsReportedAndNothingRenders(t *testing.T) {
	s, r := &fakeSyncer{err: errors.New("bridge down")}, &fakeRenderer{}
	w := newWorker(s, r, "")

	w.Cycle(context.Background(), noon)

	if len(r.rendered) != 0 {
		t.Errorf("rendered %v from a mirror that failed to sync", r.rendered)
	}
	if snap := w.Status.Snapshot(); snap.Problem != "snapshot" {
		t.Errorf("problem = %q, want snapshot", snap.Problem)
	}
}

func TestCycle_OneDimensionFailingDoesNotStopTheOthers(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{failOn: "nether"}
	w := newWorker(s, r, "")

	w.Cycle(context.Background(), noon)

	if !reflect.DeepEqual(r.rendered, []string{"overworld", "nether", "end"}) {
		t.Fatalf("rendered %v", r.rendered)
	}
	snap := w.Status.Snapshot()
	if _, ok := snap.RenderedAt["nether"]; ok {
		t.Error("the failed dimension has a render time")
	}
	if !snap.RenderedAt["overworld"].Equal(noon) || !snap.RenderedAt["end"].Equal(noon) || snap.Problem != "render" {
		t.Errorf("status = %+v", snap)
	}
}

// A later clean cycle clears the error rather than leaving a stale one up.
func TestCycle_SuccessClearsAnEarlierError(t *testing.T) {
	s, r := &fakeSyncer{err: errors.New("bridge down")}, &fakeRenderer{}
	w := newWorker(s, r, "")
	w.Cycle(context.Background(), noon)
	s.err = nil
	w.Cycle(context.Background(), noon.Add(15*time.Minute))

	if snap := w.Status.Snapshot(); snap.Problem != "" {
		t.Errorf("status still reports %q", snap.Problem)
	}
}

// A cycle that got nowhere should not wait a whole interval to try again:
// after a restart the bridge is often not connected to the console for the
// first few seconds, and a refusal means someone else's copy is nearly done.
// A quiet window is different: it ends on the clock, so normal pacing is right.
func TestCycle_ReportsWhetherToRetrySoon(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		quiet string
		want  Outcome
	}{
		{"applied", nil, "", Applied},
		{"quiet", nil, "11:30-12:30", Quiet},
		{"busy", mirror.ErrBusy, "", Busy},
		{"failed", errors.New("bridge down"), "", Failed},
	}
	for _, c := range cases {
		w := newWorker(&fakeSyncer{err: c.err}, &fakeRenderer{}, c.quiet)
		if got := w.Cycle(context.Background(), noon); got != c.want {
			t.Errorf("%s: outcome = %v, want %v", c.name, got, c.want)
		}
	}
}

// A failure that does not go away must not become a pause of the live world
// every minute: each retry doubles, up to the normal pace.
func TestNextDelay(t *testing.T) {
	const interval, retry = 15 * time.Minute, time.Minute
	cases := []struct {
		outcome  Outcome
		failures int
		want     time.Duration
	}{
		{Applied, 0, interval},
		{Quiet, 0, interval},
		{Busy, 0, retry},
		{Failed, 1, retry},
		{Failed, 2, 2 * retry},
		{Failed, 3, 4 * retry},
		{Failed, 4, 8 * retry},
		{Failed, 5, interval},
		{Failed, 60, interval},
	}
	for _, c := range cases {
		if got := nextDelay(c.outcome, c.failures, interval, retry); got != c.want {
			t.Errorf("after %v (failure %d): next in %s, want %s", c.outcome, c.failures, got, c.want)
		}
	}
	// A retry must never be slower than the normal pace.
	if got := nextDelay(Failed, 1, 30*time.Second, retry); got != 30*time.Second {
		t.Errorf("retry with a 30s interval = %s, want 30s", got)
	}
}

// A snapshot that starts just before a quiet window is still running inside
// it. The window has to be respected for as long as a snapshot can take, not
// only at the instant it begins.
func TestCycle_DoesNotStartASnapshotThatCouldRunIntoAQuietWindow(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "12:05-13:00")
	w.Lead = 6 * time.Minute

	if got := w.Cycle(context.Background(), noon); got != Quiet || s.calls != 0 {
		t.Fatalf("at 12:00 with a window from 12:05 and a 6m lead: outcome %v, %d syncs", got, s.calls)
	}
	if got := w.Cycle(context.Background(), noon.Add(-2*time.Minute)); got != Applied {
		t.Errorf("at 11:58 the window is more than the lead away: outcome %v, want applied", got)
	}
}

// If the renderer cannot run there is nothing a snapshot would be for, so
// the live world's saving is not paused to take one.
func TestCycle_RendererThatCannotRunMeansNoSnapshot(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{prepareErr: errors.New("digest mismatch")}
	w := newWorker(s, r, "")

	if got := w.Cycle(context.Background(), noon); got != Failed {
		t.Fatalf("outcome = %v, want failed", got)
	}
	if s.calls != 0 || len(r.rendered) != 0 {
		t.Errorf("%d syncs and %v rendered with a renderer that cannot run", s.calls, r.rendered)
	}
	if snap := w.Status.Snapshot(); snap.Problem != "render" {
		t.Errorf("problem = %q, want render", snap.Problem)
	}
}

func (f *fakeRenderer) RenderedAt(string) (time.Time, bool) { return time.Time{}, false }

// The chunk count runs on every snapshot that lands and before the renders,
// which take minutes: losing chunks is the one thing here that is urgent.
func TestCycle_CountsChunksBeforeRendering(t *testing.T) {
	var order []string
	s, r, c := &fakeSyncer{}, &fakeRenderer{order: &order}, &fakeCensus{order: &order}
	w := newWorker(s, r, "")
	w.Census = c

	w.Cycle(context.Background(), noon)

	if !reflect.DeepEqual(order, []string{"census", "overworld", "nether", "end"}) {
		t.Fatalf("order = %v", order)
	}
	if c.dirs[0] != filepath.Join("/data/mirror", "FWB", "db") {
		t.Errorf("counted %q", c.dirs[0])
	}
}

func TestCycle_ACensusThatFailsDoesNotStopTheMap(t *testing.T) {
	s, r, c := &fakeSyncer{}, &fakeRenderer{}, &fakeCensus{err: errors.New("unreadable")}
	w := newWorker(s, r, "")
	w.Census = c
	if got := w.Cycle(context.Background(), noon); got != Applied || len(r.rendered) != 3 {
		t.Errorf("outcome %v, rendered %v", got, r.rendered)
	}
}

func TestCycle_NoCensusWithoutAFreshSnapshot(t *testing.T) {
	for name, s := range map[string]*fakeSyncer{
		"busy":   {err: mirror.ErrBusy},
		"failed": {err: errors.New("bridge down")},
	} {
		c := &fakeCensus{}
		w := newWorker(s, &fakeRenderer{}, "")
		w.Census = c
		w.Cycle(context.Background(), noon)
		if len(c.dirs) != 0 {
			t.Errorf("%s: counted a mirror that did not change", name)
		}
	}
}

func TestCycle_LostChunksAreLoggedAsAnError(t *testing.T) {
	var logged bytes.Buffer
	c := &fakeCensus{report: chunks.Report{
		Lost:   map[chunks.Dimension]int{chunks.Overworld: 2},
		Sample: []chunks.Pos{{Dim: chunks.Overworld, X: 10, Z: -3}},
	}}
	w := newWorker(&fakeSyncer{}, &fakeRenderer{}, "")
	w.Census = c
	w.Logger = slog.New(slog.NewJSONHandler(&logged, nil))
	w.Cycle(context.Background(), noon)

	line := logged.String()
	for _, want := range []string{`"level":"ERROR"`, `"msg":"world has lost chunks"`, `"lost":2`, `"block_x":160`, `"block_z":-48`} {
		if !strings.Contains(line, want) {
			t.Errorf("log lacks %s:\n%s", want, line)
		}
	}
}
