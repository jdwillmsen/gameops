package main

import (
	"context"
	"errors"
	"testing"
	"time"

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
	connect := func(context.Context) error {
		calls++
		if calls == 3 {
			cancel()
		}
		return errors.New("kicked")
	}
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() {}, discardLog{})
	if calls != 3 {
		t.Errorf("connect called %d times, want 3", calls)
	}
}

func TestLoopResetsBeliefWhenASessionEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	resets := 0
	connect := func(context.Context) error { cancel(); return nil }
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() { resets++ }, discardLog{})
	if resets != 1 {
		t.Errorf("onEnd called %d times, want 1", resets)
	}
}

func TestLoopReportsASessionErrorToTheController(t *testing.T) {
	var got []unitevent.Event
	unitevent.SetSink(func(e unitevent.Event) { got = append(got, e) })
	t.Cleanup(func() { unitevent.SetSink(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	connect := func(context.Context) error {
		defer cancel()
		return errors.New("kicked")
	}
	// The connect that ends in cancel is not an error to report.
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() {}, discardLog{})
	if len(got) != 0 {
		t.Fatalf("a shutdown reported %+v", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	calls := 0
	connect = func(context.Context) error {
		calls++
		if calls == 2 {
			cancel()
		}
		return errors.New("kicked")
	}
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() {}, discardLog{})
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
