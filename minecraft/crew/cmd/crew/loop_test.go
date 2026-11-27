package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/internal/presenceapi"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/presence"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/unitevent"
)

// instant makes every wait elapse at once, so the loop's control flow is
// tested without real sleeps.
func instant(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- time.Time{}
	return c
}

func TestBackoffDoublesToTheCeilingAndResetsAfterAStableSession(t *testing.T) {
	min, max := time.Second, 8*time.Second
	if got := backoff(min, time.Second, min, max); got != 2*time.Second {
		t.Errorf("short session: %v, want 2s", got)
	}
	if got := backoff(8*time.Second, time.Second, min, max); got != max {
		t.Errorf("at ceiling: %v, want %v", got, max)
	}
	if got := backoff(8*time.Second, stableSession, min, max); got != min {
		t.Errorf("stable session: %v, want reset to %v", got, min)
	}
}

func TestJitterStaysWithinHalfToFull(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if j := jitter(10 * time.Second); j < 5*time.Second || j > 10*time.Second {
			t.Fatalf("jitter = %v, want within [5s, 10s]", j)
		}
	}
}

func TestLoopReconnectsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	connect := func(context.Context, func()) error {
		calls++
		if calls == 3 {
			cancel()
		}
		return errors.New("kicked")
	}
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, presence.AlwaysPresent{}, connect, func() {}, discardLog{})
	if calls != 3 {
		t.Errorf("connect called %d times, want 3", calls)
	}
}

func TestLoopResetsBeliefWhenASessionEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	resets := 0
	connect := func(context.Context, func()) error { cancel(); return nil }
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, presence.AlwaysPresent{}, connect, func() { resets++ }, discardLog{})
	if resets != 1 {
		t.Errorf("onEnd called %d times, want 1", resets)
	}
}

func TestLoopReportsASessionErrorToTheController(t *testing.T) {
	var got []unitevent.Event
	unitevent.SetSink(func(e unitevent.Event) { got = append(got, e) })
	t.Cleanup(func() { unitevent.SetSink(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	connect := func(context.Context, func()) error {
		defer cancel()
		return errors.New("kicked")
	}
	// The connect that ends in cancel is not an error to report.
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, presence.AlwaysPresent{}, connect, func() {}, discardLog{})
	if len(got) != 0 {
		t.Fatalf("a shutdown reported %+v", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	calls := 0
	connect = func(context.Context, func()) error {
		calls++
		if calls == 2 {
			cancel()
		}
		return errors.New("kicked")
	}
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, presence.AlwaysPresent{}, connect, func() {}, discardLog{})
	if len(got) != 1 {
		t.Fatalf("reported %d session errors, want 1", len(got))
	}
	if e := got[0]; e.Kind != "session_error" || e.ReasonCode != "session_lost" || e.Reason != "kicked" || e.SessionID == "" {
		t.Errorf("event = %+v", e)
	}
}

type discardLog struct{}

func (discardLog) Info(string, map[string]any)  {}
func (discardLog) Error(string, map[string]any) {}

// fakeGate admits once per value sent on allow, and parks the current
// session when park is called.
type fakeGate struct {
	allow     chan struct{}
	mu        sync.Mutex
	cancel    context.CancelFunc
	connected []bool
}

func newFakeGate() *fakeGate { return &fakeGate{allow: make(chan struct{})} }

func (g *fakeGate) Admit(ctx context.Context) (context.Context, context.CancelFunc, error) {
	select {
	case <-g.allow:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	sess, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()
	return sess, cancel, nil
}

func (g *fakeGate) Connected(c bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.connected = append(g.connected, c)
}

func (g *fakeGate) park() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancel()
}

// waits records every wait the loop asks for and lets each elapse at once.
type waits struct {
	mu  sync.Mutex
	got []time.Duration
}

func (w *waits) after(d time.Duration) <-chan time.Time {
	w.mu.Lock()
	w.got = append(w.got, d)
	w.mu.Unlock()
	return instant(d)
}

func (w *waits) list() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.got...)
}

// With the gate that always admits, the loop is the loop it was: connect at
// once, and back off between failed sessions.
func TestLoopWithoutPresenceBacksOffAsBefore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &waits{}
	calls := 0
	runConnectLoop(ctx, loopOptions{minDelay: time.Second, maxDelay: 8 * time.Second, after: w.after}, presence.AlwaysPresent{}, func(sess context.Context, _ func()) error {
		if sess.Err() != nil {
			t.Error("session started with a cancelled context")
		}
		calls++
		if calls == 3 {
			cancel()
		}
		return errors.New("dial: connection refused")
	}, func() {}, discardLog{})

	got := w.list()
	if len(got) != 2 {
		t.Fatalf("waits = %v, want one before each reconnect", got)
	}
	// Each failure doubles the delay before the jitter halves at most.
	if got[0] < time.Second || got[0] > 2*time.Second || got[1] < 2*time.Second || got[1] > 4*time.Second {
		t.Errorf("waits = %v, want the backoff to double from 1s", got)
	}
}

func TestLoopParkEndsTheSessionAndResumesWithoutBackoff(t *testing.T) {
	gate := newFakeGate()
	w := &waits{}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan int, 4)
	done := make(chan struct{})
	ends := 0

	n := 0
	go func() {
		defer close(done)
		runConnectLoop(ctx, loopOptions{minDelay: 10 * time.Second, maxDelay: 20 * time.Second, after: w.after}, gate, func(sess context.Context, spawned func()) error {
			n++
			started <- n
			spawned()
			<-sess.Done()
			return nil
		}, func() { ends++ }, discardLog{})
	}()

	gate.allow <- struct{}{}
	<-started
	gate.park()

	select {
	case <-started:
		t.Fatal("reconnected while parked")
	case <-time.After(50 * time.Millisecond):
	}

	gate.allow <- struct{}{}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no session after resume")
	}

	cancel()
	<-done
	if got := w.list(); len(got) != 0 {
		t.Errorf("waited %v around a park, want no backoff", got)
	}
	if ends != 2 {
		t.Errorf("onEnd called %d times, want once per session", ends)
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if want := []bool{true, false, true, false}; !reflect.DeepEqual(gate.connected, want) {
		t.Errorf("Connected calls = %v, want %v", gate.connected, want)
	}
}

// A park between two failures leaves the backoff where a fresh start would:
// the park is not a failure to count.
func TestLoopParkDoesNotGrowTheBackoff(t *testing.T) {
	gate := newFakeGate()
	w := &waits{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	n := 0
	go func() {
		defer close(done)
		runConnectLoop(ctx, loopOptions{minDelay: time.Second, maxDelay: 64 * time.Second, after: w.after}, gate, func(sess context.Context, _ func()) error {
			n++
			switch n {
			case 2:
				gate.park()
				<-sess.Done()
				return nil
			case 4:
				cancel()
			}
			return errors.New("kicked")
		}, func() {}, discardLog{})
	}()
	for i := 0; i < 4; i++ {
		select {
		case gate.allow <- struct{}{}:
		case <-done:
		}
	}
	<-done

	got := w.list()
	// Session 1 fails and waits; 2 is parked and does not; 3 fails after
	// the park and waits as a first failure does, not as a third.
	if len(got) != 2 {
		t.Fatalf("waits = %v, want 2", got)
	}
	if got[1] > 2*time.Second {
		t.Errorf("wait after the park = %v, want a first failure's (at most 2s)", got[1])
	}
}

func TestLoopExitsWhileParked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runConnectLoop(ctx, loopOptions{minDelay: time.Second, maxDelay: time.Second, after: instant}, newFakeGate(), func(context.Context, func()) error {
			t.Error("connected while parked")
			return nil
		}, func() {}, discardLog{})
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not exit on shutdown while parked")
	}
}

type recordingLog struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingLog) add(event string, f map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf("%s %v", event, f))
}
func (r *recordingLog) Info(e string, f map[string]any)  { r.add(e, f) }
func (r *recordingLog) Warn(e string, f map[string]any)  { r.add(e, f) }
func (r *recordingLog) Error(e string, f map[string]any) { r.add(e, f) }

func TestPresenceGateOffIsAlwaysPresent(t *testing.T) {
	log := &recordingLog{}
	gate, run := presenceGate(config.Config{}, log)
	if _, ok := gate.(presence.AlwaysPresent); !ok {
		t.Errorf("gate = %T with presence off, want presence.AlwaysPresent", gate)
	}
	if run != nil {
		t.Error("a poller was returned with presence off")
	}
	if len(log.lines) != 0 {
		t.Errorf("logged %v with presence off, want nothing", log.lines)
	}
}

func TestPresenceGateLogsNeitherTheTokenNorAURLPassword(t *testing.T) {
	log := &recordingLog{}
	cfg := config.Config{Presence: config.Presence{
		URL: "http://bot:YYYYYYYYYYYYYYYYYYYY@fwb-server-agent:8080", Token: "ZZZZZZZZZZZZZZZZZZZZ", ActorID: "afk-bot-1",
		Default: presenceapi.StateParked, PollMs: 10000,
	}}
	gate, run := presenceGate(cfg, log)
	if _, ok := gate.(*presence.Reconciler); !ok || run == nil {
		t.Fatalf("gate = %T, run nil = %v; want a reconciler and its poller", gate, run == nil)
	}
	if len(log.lines) != 1 || !strings.HasPrefix(log.lines[0], "presence_enabled ") {
		t.Fatalf("lines = %v, want one presence_enabled", log.lines)
	}
	if strings.Contains(log.lines[0], "ZZZZZZZZZZZZZZZZZZZZ") || strings.Contains(log.lines[0], "YYYYYYYYYYYYYYYYYYYY") {
		t.Errorf("presence_enabled carries a secret: %s", log.lines[0])
	}
}
