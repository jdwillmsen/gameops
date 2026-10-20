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
	l.build = func(context.Context, string, string) (*atlas.Atlas, error) {
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
	l.build = func(context.Context, string, string) (*atlas.Atlas, error) {
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
