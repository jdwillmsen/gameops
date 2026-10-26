package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/sessionid"
)

// A session lasting at least this long is treated as healthy and resets the
// reconnect backoff. Without it, a unit that connects and is immediately
// kicked retries as eagerly as one recovering from a momentary blip.
const stableSession = 60 * time.Second

type loopLogger interface {
	Info(event string, fields map[string]any)
	Error(event string, fields map[string]any)
}

type loopOptions struct {
	minDelay, maxDelay time.Duration
	// after is time.After outside tests.
	after func(time.Duration) <-chan time.Time
}

// runConnectLoop keeps crew connected for the life of the process. It
// reconnects rather than exiting, because exiting moves the retry loop into
// Kubernetes: a coarser backoff, a fresh pod, and a token reload on every
// attempt.
func runConnectLoop(ctx context.Context, opt loopOptions, connect func(context.Context) error, onEnd func(), log loopLogger) {
	delay := opt.minDelay
	first := true
	for ctx.Err() == nil {
		if !first {
			wait := jitter(delay)
			log.Info("reconnecting", map[string]any{"in_ms": wait.Milliseconds()})
			select {
			case <-opt.after(wait):
			case <-ctx.Done():
				return
			}
		}
		first = false

		started := time.Now()
		sid := sessionid.Begin()
		err := connect(ctx)
		lasted := time.Since(started)
		onEnd()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("session_error", map[string]any{"error": err.Error(), "session_lasted_ms": lasted.Milliseconds(), "session_id": sid})
		} else {
			log.Info("session_ended", map[string]any{"session_lasted_ms": lasted.Milliseconds(), "session_id": sid})
		}
		delay = backoff(delay, lasted, opt.minDelay, opt.maxDelay)
	}
}

func backoff(current, lasted, min, max time.Duration) time.Duration {
	if lasted >= stableSession {
		return min
	}
	if next := current * 2; next < max {
		return next
	}
	return max
}

// jitter spreads reconnects so a server restart does not bring every unit
// back in lockstep, at the moment the server can least take it.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}
