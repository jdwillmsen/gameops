package main

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
)

// Trails come from the live layer. Turned off, or with no live layer to
// read, nothing is started and there is no recorder for a route.
func TestTrailsStartOnlyWithTheLiveLayer(t *testing.T) {
	var wg sync.WaitGroup
	on := config.Config{Trails: true, TrailMaxAge: time.Hour, TrailMaxPoints: 100}
	if r := startTrails(t.Context(), on, nil, &wg); r != nil {
		t.Error("a recorder was started with no live layer")
	}
	layer := live.New(10*time.Second, 100, slog.New(slog.DiscardHandler))
	if r := startTrails(t.Context(), config.Config{Trails: false, TrailMaxAge: time.Hour, TrailMaxPoints: 100}, layer, &wg); r != nil {
		t.Error("a recorder was started with trails off")
	}
	wg.Wait()

	ctx, cancel := context.WithCancel(t.Context())
	r := startTrails(ctx, on, layer, &wg)
	if r == nil || r.MaxAge != time.Hour || r.MaxPoints != 100 {
		t.Fatalf("recorder = %+v", r)
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorder did not stop with the service")
	}
}
