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

	"github.com/jdwillmsen/minecraft-afk-bot/internal/config"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/logging"
	"github.com/jdwillmsen/minecraft-afk-bot/internal/presence"
	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

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

func quietLog() *logging.Logger { return logging.New("info") }

// With PRESENCE_URL unset the loop must be the loop it was: connect at once,
// and back off between failed sessions.
func TestConnectLoopWithoutPresenceReconnectsAsBefore(t *testing.T) {
	cfg := config.Config{ReconnectMinMs: 200, ReconnectMaxMs: 400}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var starts []time.Time
	began := time.Now()
	runConnectLoop(ctx, cfg, presence.AlwaysPresent{}, func(sess context.Context, spawned func()) error {
		if sess.Err() != nil {
			t.Error("session started with a cancelled context")
		}
		starts = append(starts, time.Now())
		if len(starts) == 2 {
			cancel()
		}
		return errors.New("dial: connection refused")
	}, quietLog())

	if len(starts) != 2 {
		t.Fatalf("sessions started = %d, want 2", len(starts))
	}
	if first := starts[0].Sub(began); first > 50*time.Millisecond {
		t.Errorf("first session waited %v, want it immediate", first)
	}
	if gap := starts[1].Sub(starts[0]); gap < 100*time.Millisecond {
		t.Errorf("reconnected after %v, want the backoff (at least half of 200ms)", gap)
	}
}

func TestConnectLoopParkEndsSessionAndResumesWithoutBackoff(t *testing.T) {
	// A 10s floor makes any backoff after the resume impossible to miss.
	cfg := config.Config{ReconnectMinMs: 10_000, ReconnectMaxMs: 20_000}
	gate := newFakeGate()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan int, 4)
	done := make(chan struct{})

	n := 0
	go func() {
		defer close(done)
		runConnectLoop(ctx, cfg, gate, func(sess context.Context, spawned func()) error {
			n++
			started <- n
			spawned()
			<-sess.Done()
			return nil
		}, quietLog())
	}()

	gate.allow <- struct{}{}
	<-started
	gate.park()

	select {
	case <-started:
		t.Fatal("reconnected while parked")
	case <-time.After(100 * time.Millisecond):
	}

	resumed := time.Now()
	gate.allow <- struct{}{}
	select {
	case <-started:
		if took := time.Since(resumed); took > time.Second {
			t.Errorf("resume took %v, want it immediate", took)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no session after resume")
	}

	cancel()
	<-done
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if want := []bool{true, false, true, false}; !reflect.DeepEqual(gate.connected, want) {
		t.Errorf("Connected calls = %v, want %v", gate.connected, want)
	}
}

func TestConnectLoopExitsWhileParked(t *testing.T) {
	cfg := config.Config{ReconnectMinMs: 10_000, ReconnectMaxMs: 20_000}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runConnectLoop(ctx, cfg, newFakeGate(), func(context.Context, func()) error {
			t.Error("connected while parked")
			return nil
		}, quietLog())
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

func (r *recordingLog) add(event string, f logging.Fields) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf("%s %v", event, f))
}
func (r *recordingLog) Info(e string, f logging.Fields)  { r.add(e, f) }
func (r *recordingLog) Warn(e string, f logging.Fields)  { r.add(e, f) }
func (r *recordingLog) Error(e string, f logging.Fields) { r.add(e, f) }

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

func TestPresenceGateLogsNoToken(t *testing.T) {
	log := &recordingLog{}
	cfg := config.Config{Presence: config.Presence{
		URL: "http://fwb-server-agent:8080", Token: "ZZZZZZZZZZZZZZZZZZZZ", ActorID: "afk-bot-1",
		Default: presenceapi.StateParked, PollMs: 10000,
	}}
	gate, run := presenceGate(cfg, log)

	if _, ok := gate.(*presence.Reconciler); !ok || run == nil {
		t.Fatalf("gate = %T, run nil = %v; want a reconciler and its poller", gate, run == nil)
	}
	if len(log.lines) != 1 || !strings.HasPrefix(log.lines[0], "presence_enabled ") {
		t.Fatalf("lines = %v, want one presence_enabled", log.lines)
	}
	if strings.Contains(log.lines[0], "ZZZZZZZZZZZZZZZZZZZZ") {
		t.Errorf("presence_enabled carries the token: %s", log.lines[0])
	}
}
