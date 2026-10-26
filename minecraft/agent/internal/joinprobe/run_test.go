package joinprobe

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

func TestRunProbesImmediatelyAndRepeats(t *testing.T) {
	// A buffered channel, not an atomic counter: the arrival of a probe is
	// both the count and the wake-up, so no poll loop with sleeps is needed.
	probeArrived := make(chan struct{}, 4)
	s := newFakeServer(t, samplePong, func(int32) []packet.Packet {
		select {
		case probeArrived <- struct{}{}:
		default:
		}
		return []packet.Packet{&packet.NetworkSettings{}}
	})

	// Ticks arrive on our say-so. That makes the moment Run's context ends
	// relative to the ticks deterministic: cancel with no tick outstanding
	// and Run cannot run one probe more, publish its failure, and leave that
	// failure as the last thing recorded — what a wall-clock ticker did, on
	// machines slow enough for a probe to outlast its interval.
	ticks := make(chan time.Time)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		probeLoop(ctx, s.addr(), 0, ticks, logging.New("error"))
		close(done)
	}()

	// The first probe must not wait for the first tick: a process that has
	// just started and published nothing looks exactly like one that cannot
	// reach the server. No tick has gone out yet, so receiving one proves it.
	waitFor(t, probeArrived, "the first probe")

	ticks <- time.Time{}
	waitFor(t, probeArrived, "the first repeated probe")

	ticks <- time.Time{}
	waitFor(t, probeArrived, "the second repeated probe")

	cancel()
	waitFor(t, done, "Run to return after cancellation")

	if got := testutil.ToFloat64(joinable); got != 1 {
		t.Errorf("joinable = %v after probing a healthy server, want 1", got)
	}
}

// waitFor reports a hang only. Its timeout is far past what a healthy run
// takes, so it never paces anything — the pacing comes from the ticks.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	s := newFakeServer(t, samplePong, func(int32) []packet.Packet {
		return []packet.Packet{&packet.NetworkSettings{}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, s.addr(), 0, 10*time.Millisecond, logging.New("error"))
		close(done)
	}()
	cancel()
	waitFor(t, done, "Run to return after cancellation")
}
