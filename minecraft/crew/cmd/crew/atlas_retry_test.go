package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
)

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The pack host rate-limits every unit behind one address, so a start that
// finds the limit spent must come back on its own.
func TestAtlasBuildThatFellBackIsRetriedWithoutAReconnect(t *testing.T) {
	built, err := atlas.Build("../../internal/atlas/testdata/pack")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	l := &atlasLoader{dir: t.TempDir(), hub: newHub(), log: logging.New(""), retry: time.Millisecond}
	l.build = func(ctx context.Context, _, _ string) (*atlas.Atlas, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if calls.Add(1) == 1 {
			l.fellBack.Store(true)
		}
		return built, nil
	}
	l.load(context.Background(), "1.2.3.4")
	eventually(t, "the retry", func() bool { return calls.Load() >= 2 })
	eventually(t, "the loader to settle", func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.done })
	n := calls.Load()
	l.load(context.Background(), "1.2.3.4")
	if calls.Load() != n {
		t.Error("a complete atlas was rebuilt")
	}
}

func TestFailedAtlasBuildIsRetriedWithoutAReconnect(t *testing.T) {
	built, err := atlas.Build("../../internal/atlas/testdata/pack")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	l := &atlasLoader{dir: t.TempDir(), hub: newHub(), log: logging.New(""), retry: time.Millisecond}
	l.build = func(ctx context.Context, _, _ string) (*atlas.Atlas, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if calls.Add(1) < 3 {
			return nil, errors.New("403 rate limited")
		}
		return built, nil
	}
	l.load(context.Background(), "1.2.3.4")
	eventually(t, "the third attempt", func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.done })
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

// Each reconnect starts a build of its own; during an outage those must not
// each leave a retry behind, or a unit asks the shared address once per
// reconnect instead of once.
func TestReconnectsDuringAnOutageLeaveOneRetryPending(t *testing.T) {
	var calls atomic.Int32
	l := &atlasLoader{dir: t.TempDir(), hub: newHub(), log: logging.New(""), retry: 20 * time.Millisecond}
	l.build = func(context.Context, string, string) (*atlas.Atlas, error) {
		calls.Add(1)
		return nil, errors.New("403 rate limited")
	}
	for range 10 {
		l.load(context.Background(), "1.2.3.4")
	}
	time.Sleep(200 * time.Millisecond)
	if n := calls.Load(); n != 10 {
		t.Errorf("calls = %d, want the 10 reconnect builds and no retry yet after ten failures in a row", n)
	}
}

func TestRetryLaterBuildsForTheLatestServerVersion(t *testing.T) {
	built, err := atlas.Build("../../internal/atlas/testdata/pack")
	if err != nil {
		t.Fatal(err)
	}
	versions := make(chan string, 4)
	l := &atlasLoader{dir: t.TempDir(), hub: newHub(), log: logging.New(""), retry: 50 * time.Millisecond}
	l.build = func(_ context.Context, v, _ string) (*atlas.Atlas, error) {
		versions <- v
		if v == "1.0" {
			return nil, errors.New("403 rate limited")
		}
		return built, nil
	}
	l.load(context.Background(), "1.0")
	l.mu.Lock()
	l.running = true
	l.mu.Unlock()
	l.load(context.Background(), "2.0")
	l.mu.Lock()
	l.running = false
	l.mu.Unlock()
	<-versions
	select {
	case v := <-versions:
		if v != "2.0" {
			t.Errorf("retry built for %q, want the newer server 2.0", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no retry")
	}
}

// The delay doubles per failure in a row and never exceeds an hour.
func TestAtlasRetryBacksOffToAnHour(t *testing.T) {
	l := &atlasLoader{retry: time.Minute}
	for _, c := range []struct {
		failures int
		lo, hi   time.Duration
	}{
		{1, time.Minute, 2 * time.Minute},
		{2, 2 * time.Minute, 4 * time.Minute},
		{3, 4 * time.Minute, 8 * time.Minute},
		{100, 30 * time.Minute, time.Hour},
	} {
		for range 50 {
			if d := l.retryDelay(c.failures); d < c.lo || d >= c.hi {
				t.Fatalf("retryDelay(%d) = %v, want in [%v, %v)", c.failures, d, c.lo, c.hi)
			}
		}
	}
}
