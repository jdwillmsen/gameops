package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
)

func liveConfig(t *testing.T, enabled bool) (config.Config, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/script" && r.Header.Get("Authorization") == "Bearer bridge-token" {
			asked.Add(1)
		}
		<-r.Context().Done()
	}))
	t.Cleanup(bridge.Close)
	return config.Config{
		BridgeURL: bridge.URL, BridgeToken: "bridge-token",
		Live: enabled, LivePollWait: 2 * time.Second, LiveTTL: 10 * time.Second, LiveMaxEntities: 1000,
	}, &asked
}

// The switch is the way to take the live layer out of a running system, so
// off has to mean nothing is left running: no request to the bridge, no
// goroutine, and no layer for the server to build a route on.
func TestLiveDisabledStartsNothing(t *testing.T) {
	cfg, asked := liveConfig(t, false)
	var wg sync.WaitGroup
	if layer := startLive(t.Context(), cfg, slog.New(slog.DiscardHandler), &wg); layer != nil {
		t.Fatal("a layer was built with the live layer off")
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("something was started with the live layer off")
	}
	time.Sleep(200 * time.Millisecond)
	if n := asked.Load(); n != 0 {
		t.Errorf("the bridge was asked for records %d times with the live layer off", n)
	}
}

func TestLiveEnabledPollsTheBridgeUntilShutdown(t *testing.T) {
	cfg, asked := liveConfig(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	if layer := startLive(ctx, cfg, slog.New(slog.DiscardHandler), &wg); layer == nil {
		t.Fatal("no layer with the live layer on")
	}
	for deadline := time.Now().Add(5 * time.Second); asked.Load() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bridge was never asked for records")
		}
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the source outlived the context it was started with")
	}
}
