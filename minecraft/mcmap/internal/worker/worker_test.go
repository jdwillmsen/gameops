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

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/generations"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/mirror"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/schedule"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

type fakeSyncer struct {
	calls int
	err   error
	// idle is a snapshot that found the world as the last one left it.
	idle bool
}

func (f *fakeSyncer) Sync(context.Context) (mirror.Stats, error) {
	f.calls++
	if f.idle {
		return mirror.Stats{Files: 3}, f.err
	}
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
	order    *[]string
	rendered []string
	worlds   []string
	outs     []string
	topYs    []int
	failOn   string
	// failOnTopY fails the render of a nether cut at that height.
	failOnTopY int
	// hang makes a render of that cut wait for its context to end.
	hangOnTopY int
	prepareErr error
	prepared   int
}

func (f *fakeRenderer) Prepare(context.Context) error {
	f.prepared++
	return f.prepareErr
}

func (f *fakeRenderer) Render(ctx context.Context, world, dimension, out string, topY int) error {
	f.rendered = append(f.rendered, dimension)
	if f.order != nil {
		*f.order = append(*f.order, dimension)
	}
	f.worlds = append(f.worlds, world)
	f.outs = append(f.outs, out)
	f.topYs = append(f.topYs, topY)
	if topY != 0 && topY == f.hangOnTopY {
		<-ctx.Done()
		return ctx.Err()
	}
	if dimension == f.failOn && f.failOnTopY == 0 || topY != 0 && topY == f.failOnTopY {
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

type fakeKeeper struct {
	order   *[]string
	srcs    []string
	ats     []time.Time
	healths []generations.Health
	err     error
	outcome generations.Outcome
}

func (f *fakeKeeper) Capture(_ context.Context, src string, at time.Time, h generations.Health) (generations.Outcome, error) {
	if f.order != nil {
		*f.order = append(*f.order, "keep")
	}
	f.srcs = append(f.srcs, src)
	f.ats = append(f.ats, at)
	f.healths = append(f.healths, h)
	return f.outcome, f.err
}

// The copy is taken from the whole mirror, before the renders, which take
// minutes the next node failure may not leave.
func TestCycle_RetainsTheSnapshotAfterCountingItAndBeforeRendering(t *testing.T) {
	var order []string
	k := &fakeKeeper{order: &order, outcome: generations.Promoted}
	w := newWorker(&fakeSyncer{}, &fakeRenderer{order: &order}, "")
	w.Census, w.Keeper = &fakeCensus{order: &order}, k

	w.Cycle(context.Background(), noon)

	if !reflect.DeepEqual(order, []string{"census", "keep", "overworld", "nether", "end"}) {
		t.Fatalf("order = %v", order)
	}
	if len(k.srcs) != 1 || k.srcs[0] != "/data/mirror" || !k.ats[0].Equal(noon) || k.healths[0] != generations.Whole {
		t.Errorf("captured %v at %v as %v", k.srcs, k.ats, k.healths)
	}
}

// Everything that could make a snapshot the wrong thing to promote. A
// damaged one is still offered, so it can be held apart for whoever
// measures the loss; one nothing counted is not offered at all.
func TestCycle_OnlyACountedWholeSnapshotIsOfferedAsTheRestorePoint(t *testing.T) {
	damaged := chunks.Report{Lost: map[chunks.Dimension]int{chunks.Overworld: 6460}}
	for name, c := range map[string]struct {
		census *fakeCensus
		none   bool
		want   []generations.Health
	}{
		"whole":         {census: &fakeCensus{}, want: []generations.Health{generations.Whole}},
		"lost chunks":   {census: &fakeCensus{report: damaged}, want: []generations.Health{generations.Damaged}},
		"census failed": {census: &fakeCensus{err: errors.New("unreadable")}, want: nil},
		"no census":     {none: true, want: nil},
	} {
		k := &fakeKeeper{outcome: generations.Promoted}
		w := newWorker(&fakeSyncer{}, &fakeRenderer{}, "")
		w.Keeper = k
		if !c.none {
			w.Census = c.census
		}
		w.Cycle(context.Background(), noon)
		if !reflect.DeepEqual(k.healths, c.want) {
			t.Errorf("%s: offered %v, want %v", name, k.healths, c.want)
		}
	}
}

func TestCycle_NothingIsRetainedWithoutAFreshSnapshot(t *testing.T) {
	for name, s := range map[string]*fakeSyncer{
		"busy":   {err: mirror.ErrBusy},
		"failed": {err: errors.New("bridge down")},
	} {
		k := &fakeKeeper{}
		w := newWorker(s, &fakeRenderer{}, "")
		w.Census, w.Keeper = &fakeCensus{}, k
		w.Cycle(context.Background(), noon)
		if len(k.srcs) != 0 {
			t.Errorf("%s: retained a mirror that did not change", name)
		}
	}
}

// A volume that cannot take another copy costs the map its newest restore
// point, not its tiles and not the copies it already holds.
func TestCycle_AFailedCaptureIsLoggedAndStopsNothing(t *testing.T) {
	var logged bytes.Buffer
	k := &fakeKeeper{err: errors.New("no space left on device")}
	w := newWorker(&fakeSyncer{}, &fakeRenderer{}, "")
	w.Census, w.Keeper = &fakeCensus{}, k
	w.Logger = slog.New(slog.NewJSONHandler(&logged, nil))

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v", got)
	}
	if !strings.Contains(logged.String(), `"msg":"snapshot not retained"`) {
		t.Errorf("log = %s", logged.String())
	}
}

type fakeSurveyor struct {
	order    *[]string
	dirs     []string
	err      error
	deadline bool
	ran      bool
	// after, if set, is called when a survey has been taken.
	after func()
	// panics, if set, is what a survey panics with, once.
	panics any
}

func (f *fakeSurveyor) Take(ctx context.Context, worldDir string, _ time.Time) (structures.Survey, error) {
	f.dirs = append(f.dirs, worldDir)
	_, f.deadline = ctx.Deadline()
	f.ran = true
	if f.after != nil {
		defer f.after()
	}
	if f.order != nil {
		*f.order = append(*f.order, "survey")
	}
	if p := f.panics; p != nil {
		f.panics = nil
		panic(p)
	}
	return structures.Survey{}, f.err
}

// A survey parses records a game wrote. One that panics over a record is
// that survey's loss: the cycle is still applied, the service is still up
// for the next one, and the panic is logged with its stack and counted.
func TestCycle_ASurveyThatPanicsCostsNothingElse(t *testing.T) {
	var order []string
	var log bytes.Buffer
	w := newWorker(&fakeSyncer{}, &fakeRenderer{order: &order}, "")
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	survey := &fakeSurveyor{order: &order, panics: "slice bounds out of range"}
	w.Structures = survey
	before := testutil.ToFloat64(metricSurveyPanics)

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v, want the cycle to count as applied", got)
	}
	if want := []string{"overworld", "nether", "end", "survey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if !strings.Contains(log.String(), "slice bounds out of range") || !strings.Contains(log.String(), "stack=") {
		t.Errorf("the panic and its stack are not in the log: %s", log.String())
	}
	if got := testutil.ToFloat64(metricSurveyPanics) - before; got != 1 {
		t.Errorf("panics counted = %v, want 1", got)
	}
	if w.Status.Snapshot().Problem != "" {
		t.Errorf("the page is told of a problem: %q", w.Status.Snapshot().Problem)
	}
	// And the next cycle surveys again.
	if got := w.Cycle(context.Background(), noon.Add(15*time.Minute)); got != Applied || len(survey.dirs) != 2 {
		t.Errorf("after the panic: outcome %v and %d surveys, want another survey", got, len(survey.dirs))
	}
}

// Structures change only as chunks are generated; the count, the retained
// copy and the tiles must not wait for them.
func TestCycle_SurveysStructuresAfterEverythingElse(t *testing.T) {
	var order []string
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	w.Census = &fakeCensus{order: &order}
	survey := &fakeSurveyor{order: &order}
	w.Structures = survey
	w.SurveyTimeout = time.Minute

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v", got)
	}
	if want := []string{"census", "overworld", "nether", "end", "survey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if want := []string{filepath.Join("/data/mirror", "FWB")}; !reflect.DeepEqual(survey.dirs, want) {
		t.Errorf("surveyed %v, want %v", survey.dirs, want)
	}
	if !survey.deadline {
		t.Error("the survey ran with no time limit, so a stuck one would hold every later cycle")
	}
}

func TestCycle_AFailedSurveyCostsNothingElse(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	var log bytes.Buffer
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	w.Structures = &fakeSurveyor{err: errors.New("world unreadable")}

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Errorf("outcome = %v, want applied", got)
	}
	if snap := w.Status.Snapshot(); snap.Problem != "" || !snap.RenderedAt["end"].Equal(noon) {
		t.Errorf("status = %+v", snap)
	}
	if !strings.Contains(log.String(), "structure survey failed") {
		t.Errorf("the failure was not logged:\n%s", log.String())
	}
}

// A snapshot that did not happen leaves nothing new to survey.
func TestCycle_NoSurveyWithoutASnapshot(t *testing.T) {
	s, r := &fakeSyncer{err: mirror.ErrBusy}, &fakeRenderer{}
	w := newWorker(s, r, "")
	survey := &fakeSurveyor{}
	w.Structures = survey
	w.Cycle(context.Background(), noon)
	if len(survey.dirs) != 0 {
		t.Errorf("surveyed %v after a refused snapshot", survey.dirs)
	}
}

var levels = []render.NetherLevel{{TopY: 66, Label: "Mid"}, {TopY: 34, Label: "Low"}}

func TestCycle_NoExtraLevelsRendersExactlyTheThreeDimensions(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")

	w.Cycle(context.Background(), noon)

	if !reflect.DeepEqual(r.outs, []string{"/data/maps/overworld", "/data/maps/nether", "/data/maps/end"}) || !reflect.DeepEqual(r.topYs, []int{0, 0, 0}) {
		t.Errorf("outs %v, cuts %v", r.outs, r.topYs)
	}
}

func TestCycle_ExtraLevelsAreRenderedLastEachInADirectoryOfItsOwn(t *testing.T) {
	var order []string
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	w.NetherLevels = levels
	w.Structures = &fakeSurveyor{order: &order}

	w.Cycle(context.Background(), noon)

	if want := []string{"overworld", "nether", "end", "survey", "nether", "nether"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if want := []string{"/data/maps/overworld", "/data/maps/nether", "/data/maps/end", "/data/maps/nether-66", "/data/maps/nether-34"}; !reflect.DeepEqual(r.outs, want) {
		t.Errorf("outs = %v, want %v", r.outs, want)
	}
	if want := []int{0, 0, 0, 66, 34}; !reflect.DeepEqual(r.topYs, want) {
		t.Errorf("cuts = %v, want %v", r.topYs, want)
	}
	snap := w.Status.Snapshot()
	if !snap.RenderedAt["nether-66"].Equal(noon) || !snap.RenderedAt["nether-34"].Equal(noon) || snap.Problem != "" {
		t.Errorf("status = %+v", snap)
	}
}

// A level that fails is counted under its own name and leaves the cycle
// and the other levels as they were.
func TestCycle_AFailingExtraLevelCostsNothingElse(t *testing.T) {
	var order []string
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order, failOnTopY: 66}
	w := newWorker(s, r, "")
	w.NetherLevels = levels
	survey := &fakeSurveyor{order: &order}
	w.Structures = survey
	w.History = &fakeHistorian{order: &order}
	var log bytes.Buffer
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	before := testutil.ToFloat64(metricRenderFailures.WithLabelValues("nether-66"))
	untouched := testutil.ToFloat64(metricRenderFailures.WithLabelValues("nether"))

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v, want applied", got)
	}

	if got := testutil.ToFloat64(metricRenderFailures.WithLabelValues("nether-66")) - before; got != 1 {
		t.Errorf("failures counted for the level = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metricRenderFailures.WithLabelValues("nether")); got != untouched {
		t.Errorf("the default nether's failures moved from %v to %v", untouched, got)
	}
	snap := w.Status.Snapshot()
	if snap.Problem != "" || !snap.RenderedAt["end"].Equal(noon) || !snap.RenderedAt["nether-34"].Equal(noon) {
		t.Errorf("status = %+v", snap)
	}
	if _, ok := snap.RenderedAt["nether-66"]; ok {
		t.Error("the failed level has a render time")
	}
	if !survey.ran || !strings.Contains(log.String(), "render failed") {
		t.Errorf("survey ran = %v; log:\n%s", survey.ran, log.String())
	}
	if !contains(order, "history") {
		t.Errorf("the day was not offered to the history: %v", order)
	}
}

// The levels have one budget between them, so a level that hangs is cut
// off when it is spent and the one after it is not started: what the levels
// can hold the next cycle back by is the budget, however many there are.
// Both are failures, neither is the page's problem, and both are tried
// again the next cycle.
func TestCycle_ExtraLevelsShareOneBudgetAndAreTriedAgainNextCycle(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{hangOnTopY: 66}
	w := newWorker(s, r, "")
	w.NetherLevels = levels
	w.LevelBudget = 20 * time.Millisecond
	var log bytes.Buffer
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	failures := func(name string) float64 { return testutil.ToFloat64(metricRenderFailures.WithLabelValues(name)) }
	cutOff, notStarted := failures("nether-66"), failures("nether-34")

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Fatalf("outcome = %v, want applied", got)
	}

	if got := failures("nether-66") - cutOff; got != 1 {
		t.Errorf("failures counted for the level cut off = %v, want 1", got)
	}
	if got := failures("nether-34") - notStarted; got != 1 {
		t.Errorf("failures counted for the level not started = %v, want 1", got)
	}
	if want := []int{0, 0, 0, 66}; !reflect.DeepEqual(r.topYs, want) {
		t.Errorf("cuts rendered = %v, want %v: a level was started with no time left", r.topYs, want)
	}
	snap := w.Status.Snapshot()
	if snap.Problem != "" || !snap.RenderedAt["end"].Equal(noon) {
		t.Errorf("status = %+v", snap)
	}
	for _, name := range []string{"nether-66", "nether-34"} {
		if _, ok := snap.RenderedAt[name]; ok {
			t.Errorf("%s has a render time", name)
		}
	}
	if got := strings.Count(log.String(), "render failed"); got != 2 {
		t.Errorf("failures logged = %d, want 2:\n%s", got, log.String())
	}

	r.hangOnTopY = 0
	later := noon.Add(15 * time.Minute)
	if got := w.Cycle(context.Background(), later); got != Applied {
		t.Fatalf("outcome = %v, want applied", got)
	}
	snap = w.Status.Snapshot()
	if !snap.RenderedAt["nether-66"].Equal(later) || !snap.RenderedAt["nether-34"].Equal(later) {
		t.Errorf("the levels were not rendered the cycle after: %+v", snap.RenderedAt)
	}
}

// A budget that is not spent holds nothing back.
func TestCycle_ExtraLevelsWithinTheirBudgetAreAllRendered(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	w.NetherLevels = levels
	w.LevelBudget = time.Minute

	w.Cycle(context.Background(), noon)

	snap := w.Status.Snapshot()
	if !snap.RenderedAt["nether-66"].Equal(noon) || !snap.RenderedAt["nether-34"].Equal(noon) {
		t.Errorf("status = %+v", snap)
	}
}

func TestCycle_NoExtraLevelIsRenderedOnceTheCycleIsCancelled(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	w.NetherLevels = levels
	ctx, cancel := context.WithCancel(context.Background())
	w.Structures = &fakeSurveyor{after: cancel}

	w.Cycle(ctx, noon)

	if len(r.topYs) != 3 {
		t.Errorf("renders after the cancel: %v", r.topYs)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
