package worlddamage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

var t0 = time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)

func damaged(at time.Time, lost int) mapclient.World {
	return mapclient.World{
		Checked: true, CheckedAt: at, LostTotal: lost,
		Lost:       map[string]int{"overworld": lost},
		LostSample: []mapclient.Chunk{{Dimension: "overworld", X: 2560, Z: -16}},
	}
}

// fakeMap stands in for the map service, with the one rule the real one has
// that matters here: an acknowledgement clears the loss only while it names
// the count currently held.
type fakeMap struct {
	mu       sync.Mutex
	world    mapclient.World
	worldErr error
	ackErr   error
	acked    []time.Time
	reads    int
}

func (f *fakeMap) World(context.Context) (mapclient.World, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.world, f.worldErr
}

func (f *fakeMap) AcknowledgeWorld(_ context.Context, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, at)
	if f.ackErr != nil {
		return f.ackErr
	}
	if !at.Equal(f.world.CheckedAt) {
		return mapclient.ErrCountReplaced
	}
	f.world.LostTotal, f.world.Lost, f.world.LostSample = 0, nil, nil
	return nil
}

func (f *fakeMap) set(w mapclient.World) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.world = w
}

func newWatch(src Source) *Watch { return New(src, logging.New("error")) }

func TestCurrent_ReportsTheLossTheMapHolds(t *testing.T) {
	w := newWatch(&fakeMap{world: damaged(t0, 6460)})
	if err := w.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok := w.Current(context.Background())
	if !ok || got.LostTotal != 6460 || got.Lost["overworld"] != 6460 || len(got.LostSample) != 1 {
		t.Fatalf("Current = %+v, %v", got, ok)
	}
}

func TestCurrent_ACleanWorldIsNoCondition(t *testing.T) {
	for name, world := range map[string]mapclient.World{
		"never counted": {},
		"counted clean": {Checked: true, CheckedAt: t0},
	} {
		w := newWatch(&fakeMap{world: world})
		if err := w.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, ok := w.Current(context.Background()); ok {
			t.Errorf("%s: Current = %+v, want no condition", name, got)
		}
	}
}

// A join straight after a restart must not slip through the gap before the
// first poll: the process holds nothing of its own, and the map's ledger is
// the only record, so the first question is asked when it is first needed.
func TestCurrent_ARestartedAgentReadsTheConditionBeforeAnyPoll(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	first := newWatch(src)
	if err := first.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	restarted := newWatch(src)
	got, ok := restarted.Current(context.Background())
	if !ok || got.LostTotal != 12 {
		t.Fatalf("Current after restart = %+v, %v; want the condition the map still holds", got, ok)
	}
}

func TestCurrent_ARestartedAgentStaysClearedAfterAnAcknowledgement(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	w := newWatch(src)
	if _, err := w.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, ok := newWatch(src).Current(context.Background()); ok {
		t.Fatalf("Current after restart = %+v, want the cleared condition to stay cleared", got)
	}
}

// Nothing says the map is reachable when an agent starts, and a join in that
// window still has to be told. What the agent last saw is the best answer it
// has until the map answers again.
func TestPoll_AnUnreachableMapKeepsTheLastKnownCondition(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	w := newWatch(src)
	if err := w.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.worldErr = errors.New("connection refused")
	src.mu.Unlock()

	if err := w.Poll(context.Background()); err == nil {
		t.Fatal("Poll err = nil, want the failure reported to the caller")
	}
	if got, ok := w.Current(context.Background()); !ok || got.LostTotal != 12 {
		t.Fatalf("Current = %+v, %v; an unreachable map must not read as a repaired world", got, ok)
	}
}

func TestCurrent_NeverReadAndUnreachableIsNoCondition(t *testing.T) {
	w := newWatch(&fakeMap{worldErr: errors.New("connection refused")})
	if got, ok := w.Current(context.Background()); ok {
		t.Fatalf("Current = %+v, want nothing to claim", got)
	}
}

func TestPoll_ANewCountReplacesTheHeldOne(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	w := newWatch(src)
	w.Poll(context.Background())
	src.set(damaged(t0.Add(15*time.Minute), 30))
	w.Poll(context.Background())
	if got, _ := w.Current(context.Background()); got.LostTotal != 30 {
		t.Fatalf("LostTotal = %d, want 30", got.LostTotal)
	}
}

func TestClear_AcknowledgesTheCountTheNoticeQuoted(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 6460)}
	w := newWatch(src)
	w.Poll(context.Background())

	cleared, err := w.Clear(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cleared.LostTotal != 6460 {
		t.Errorf("cleared = %+v, want the condition that was cleared", cleared)
	}
	if len(src.acked) != 1 || !src.acked[0].Equal(t0) {
		t.Errorf("acknowledged %v, want exactly the held count %v", src.acked, t0)
	}
	if got, ok := w.Current(context.Background()); ok {
		t.Errorf("Current = %+v, want the notice to stop at once, without waiting for the next poll", got)
	}
}

// A read that was already in flight when a newer count was taken can answer
// after it. Its older count must not replace what the newer read cached.
func TestPoll_AnOlderCountArrivingLateDoesNotReplaceANewerOne(t *testing.T) {
	src := &fakeMap{world: damaged(t0.Add(15*time.Minute), 12)}
	w := newWatch(src)
	w.Poll(context.Background())

	src.set(damaged(t0, 6460))
	w.Poll(context.Background())

	got, ok := w.Current(context.Background())
	if !ok || got.LostTotal != 12 || !got.CheckedAt.Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("Current = %+v, %v; want the newer count of 12 kept", got, ok)
	}
}

// ackThenCount is a map on which a newer count lands, and is polled, after
// the acknowledgement is accepted but before Clear hears so.
type ackThenCount struct {
	*fakeMap
	after func()
}

func (a ackThenCount) AcknowledgeWorld(ctx context.Context, at time.Time) error {
	err := a.fakeMap.AcknowledgeWorld(ctx, at)
	if err == nil {
		a.after()
	}
	return err
}

// The acknowledgement names one count. A later one that a poll cached while
// the acknowledgement was in flight was never accepted, so clearing must not
// wipe it and leave joining players unwarned until the next poll.
func TestClear_KeepsANewerLossCachedWhileAcknowledging(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 6460)}
	var w *Watch
	w = newWatch(ackThenCount{fakeMap: src, after: func() {
		src.set(damaged(t0.Add(15*time.Minute), 12))
		w.Poll(context.Background())
	}})
	w.Poll(context.Background())

	if _, err := w.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok := w.Current(context.Background())
	if !ok || got.LostTotal != 12 {
		t.Fatalf("Current = %+v, %v; want the newer, unacknowledged loss of 12 still held", got, ok)
	}
}

// The operator read one count and a newer one has landed since: clearing the
// newer one would forget losses nobody looked at. The refusal is reported,
// and what the agent holds is brought up to date so the next try names the
// count the operator can now see.
func TestClear_ANewerCountIsRefusedAndRefreshed(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	w := newWatch(src)
	w.Poll(context.Background())
	src.set(damaged(t0.Add(15*time.Minute), 30))

	_, err := w.Clear(context.Background())
	if !errors.Is(err, mapclient.ErrCountReplaced) {
		t.Fatalf("err = %v, want ErrCountReplaced", err)
	}
	got, ok := w.Current(context.Background())
	if !ok || got.LostTotal != 30 {
		t.Fatalf("Current = %+v, %v; want the newer count so the retry names it", got, ok)
	}
	if _, err := w.Clear(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestClear_WithNothingRecordedSaysSoWithoutCallingTheMap(t *testing.T) {
	src := &fakeMap{world: mapclient.World{Checked: true, CheckedAt: t0}}
	w := newWatch(src)
	w.Poll(context.Background())
	if _, err := w.Clear(context.Background()); !errors.Is(err, ErrNothingToClear) {
		t.Fatalf("err = %v, want ErrNothingToClear", err)
	}
	if len(src.acked) != 0 {
		t.Errorf("acknowledged %v, want no call: there is nothing to accept", src.acked)
	}
}

func TestClear_AFailedAcknowledgementLeavesTheNoticeOn(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12), ackErr: errors.New("map: acknowledge world: status 500")}
	w := newWatch(src)
	w.Poll(context.Background())
	if _, err := w.Clear(context.Background()); err == nil {
		t.Fatal("err = nil, want the failure")
	}
	if _, ok := w.Current(context.Background()); !ok {
		t.Fatal("condition cleared though the map never accepted the acknowledgement")
	}
}

// A poll that was already reading when the acknowledgement landed returns the
// count as it was before. Believing it would switch the notice back on for a
// world the operator just accepted.
func TestPoll_AReadStartedBeforeAnAcknowledgementDoesNotRevive(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	w := newWatch(src)
	w.Poll(context.Background())
	if _, err := w.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}

	src.set(damaged(t0, 12))
	w.Poll(context.Background())
	if got, ok := w.Current(context.Background()); ok {
		t.Fatalf("Current = %+v, want the acknowledged count to stay cleared", got)
	}

	src.set(damaged(t0.Add(15*time.Minute), 3))
	w.Poll(context.Background())
	if got, ok := w.Current(context.Background()); !ok || got.LostTotal != 3 {
		t.Fatalf("Current = %+v, %v; a later count with new loss must raise the notice again", got, ok)
	}
}

func TestRun_PollsUntilCancelled(t *testing.T) {
	src := &fakeMap{world: damaged(t0, 12)}
	w := newWatch(src)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx, time.Millisecond); close(done) }()

	deadline := time.After(5 * time.Second)
	for {
		src.mu.Lock()
		reads := src.reads
		src.mu.Unlock()
		if reads >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d reads", reads)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
