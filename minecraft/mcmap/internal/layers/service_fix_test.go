package layers

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A refresh that fails must not move the next one onto the directory of the
// view in use: opening a view clears what is in its directory.
func TestService_AFailedRefreshDoesNotSendTheNextOneToTheLiveViewsDirectory(t *testing.T) {
	s, dir := serviceOf(t, 1)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	live := s.cur.dir
	if err := s.Refresh(filepath.Join(t.TempDir(), "nothing"), "v2"); err == nil {
		t.Fatal("a world that is not there was opened")
	}
	if s.cur.dir != live || !strings.HasPrefix(s.Version(), "v1") {
		t.Fatalf("a failed refresh changed the view in use: %q", s.Version())
	}
	if err := s.Refresh(dir, "v3"); err != nil {
		t.Fatal(err)
	}
	if s.cur.dir == live {
		t.Errorf("the refresh after a failed one opened %d, the directory the live view was in", s.cur.dir)
	}
	if res, err := s.Tile(context.Background(), cutSpec, 0, 0, 0); err != nil || !res.Drawn {
		t.Errorf("a tile after the refreshes: %v, %v", res.Drawn, err)
	}
}

// Leaflet gives up on every tile it is panned away from; a tile that was
// drawn is kept all the same, since nobody else need draw it.
func TestService_ATileDrawnForAViewerWhoLeftIsKept(t *testing.T) {
	s, dir := serviceOf(t, 1)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.onRender = cancel
	_, _ = s.Tile(ctx, cutSpec, 0, 0, 0)
	if _, _, held := s.Cache().Get(Key{Version: s.Version(), Spec: cutSpec}); !held {
		t.Error("a tile drawn for a viewer who left was thrown away")
	}
	s.onRender = nil
	if res, err := s.Tile(context.Background(), cutSpec, 0, 0, 0); err != nil || !res.Cached {
		t.Errorf("the next viewer: cached %v, %v", res.Cached, err)
	}
}

// A panic in a render must not leave the world held for reading, which
// would stop every later refresh, or the tile marked as being drawn.
func TestService_APanicInARenderIsAnErrorAndLeavesNothingHeld(t *testing.T) {
	s, dir := serviceOf(t, 1)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	s.onRender = func() { panic("index out of range") }
	if _, err := s.Tile(context.Background(), cutSpec, 0, 0, 0); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("a panic was %v", err)
	}
	s.flightMu.Lock()
	left := len(s.flights)
	s.flightMu.Unlock()
	if left != 0 {
		t.Errorf("%d tiles are still marked as being drawn", left)
	}
	refreshed := make(chan error, 1)
	go func() { refreshed <- s.Refresh(dir, "v2") }()
	select {
	case err := <-refreshed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a refresh after a panic in a render never finished")
	}
	s.onRender = nil
	if res, err := s.Tile(context.Background(), cutSpec, 0, 0, 0); err != nil || !res.Drawn {
		t.Errorf("a tile after the panic: %v, %v", res.Drawn, err)
	}
}

// Two openings at the same second are two worlds to the cache.
func TestService_EveryRefreshIsAVersionOfItsOwn(t *testing.T) {
	s, dir := serviceOf(t, 1)
	seen := map[string]bool{}
	for range 4 {
		if err := s.Refresh(dir, "same"); err != nil {
			t.Fatal(err)
		}
		if seen[s.Version()] {
			t.Fatalf("version %q was given twice", s.Version())
		}
		seen[s.Version()] = true
	}
}
