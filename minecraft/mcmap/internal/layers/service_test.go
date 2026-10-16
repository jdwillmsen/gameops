package layers

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

func key(v string, x int) Key {
	return Key{Version: v, Spec: Spec{Dim: chunks.Overworld, Cut: 0, Thickness: 16}, X: x}
}

func TestCache_HoldsUpToItsLimitAndLetsGoOfTheLeastRecentlyUsed(t *testing.T) {
	c := NewCache(3 * (100 + entryOverhead))
	pic := make([]byte, 100)
	for i := range 3 {
		c.Put(key("a", i), pic, true)
	}
	// Asking for the first makes the second the oldest.
	if _, _, held := c.Get(key("a", 0)); !held {
		t.Fatal("the first is not held")
	}
	c.Put(key("a", 3), pic, true)
	if _, _, held := c.Get(key("a", 1)); held {
		t.Error("the least recently used was kept")
	}
	for _, x := range []int{0, 2, 3} {
		if _, _, held := c.Get(key("a", x)); !held {
			t.Errorf("tile %d was let go of", x)
		}
	}
	if c.Bytes() > 3*(100+entryOverhead) || c.Len() != 3 {
		t.Errorf("%d bytes in %d entries", c.Bytes(), c.Len())
	}
	// One that does not fit at all is not kept, and costs nothing held.
	c.Put(key("a", 9), make([]byte, 10_000), true)
	if _, _, held := c.Get(key("a", 9)); held || c.Len() != 3 {
		t.Error("a picture larger than the cache was kept")
	}
}

func TestCache_ATileWithNothingInItIsKeptAndCostsSomething(t *testing.T) {
	c := NewCache(10 * entryOverhead)
	for i := range 100 {
		c.Put(key("a", i), nil, false)
	}
	if c.Len() != 10 {
		t.Errorf("%d empty tiles held in room for 10", c.Len())
	}
	data, ok, held := c.Get(key("a", 99))
	if !held || ok || data != nil {
		t.Errorf("the last: held %v, drawn %v", held, ok)
	}
}

func TestCache_ReplacingATileDoesNotCountItTwiceAndDropLeavesOnlyOneVersion(t *testing.T) {
	c := NewCache(1 << 20)
	c.Put(key("a", 1), make([]byte, 500), true)
	c.Put(key("a", 1), make([]byte, 200), true)
	if c.Bytes() != 200+entryOverhead || c.Len() != 1 {
		t.Errorf("%d bytes in %d entries", c.Bytes(), c.Len())
	}
	c.Put(key("b", 1), make([]byte, 50), true)
	c.Put(key("b", 2), make([]byte, 50), true)
	c.Drop("b")
	if c.Len() != 2 || c.Bytes() != 2*(50+entryOverhead) {
		t.Errorf("after the drop: %d bytes in %d entries", c.Bytes(), c.Len())
	}
	if _, _, held := c.Get(key("a", 1)); held {
		t.Error("a tile of an older version survived")
	}
}

// A service over a made-up world: a stone chunk at the origin.
func serviceOf(t *testing.T, max int) (*Service, string) {
	t.Helper()
	f := newFake(t)
	for cx := range 4 {
		f.chunk(chunks.Pos{Dim: chunks.Overworld, X: int32(cx), Z: 0}, 10, upTo(10, "stone"))
	}
	dir := f.closed()
	s := NewService(Config{WorkDir: filepath.Join(t.TempDir(), "work"), CacheBytes: 1 << 20, MaxRenders: max})
	t.Cleanup(s.Close)
	return s, dir
}

var cutSpec = Spec{Dim: chunks.Overworld, Cut: 10, Thickness: 16}

func TestService_DrawsOnceAndThenServesFromTheCache(t *testing.T) {
	s, dir := serviceOf(t, 2)
	if _, err := s.Tile(context.Background(), cutSpec, 0, 0, 0); err != ErrNotReady {
		t.Fatalf("a tile before the world is open: %v", err)
	}
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	first, err := s.Tile(context.Background(), cutSpec, 0, 0, 0)
	if err != nil || !first.Drawn || first.Cached || len(first.Data) == 0 || first.Timings.Chunks == 0 {
		t.Fatalf("the first: %+v, %v", first, err)
	}
	again, err := s.Tile(context.Background(), cutSpec, 0, 0, 0)
	if err != nil || !again.Cached || string(again.Data) != string(first.Data) {
		t.Errorf("the second was drawn again or differs: cached %v, %v", again.Cached, err)
	}
	// Nothing there is also remembered.
	empty, _ := s.Tile(context.Background(), cutSpec, 0, 40, 40)
	emptyAgain, _ := s.Tile(context.Background(), cutSpec, 0, 40, 40)
	if empty.Drawn || empty.Cached || !emptyAgain.Cached || emptyAgain.Drawn {
		t.Errorf("an empty tile: %+v then %+v", empty.Cached, emptyAgain)
	}
}

func TestService_ANewVersionDrawsAgainAndLetsGoOfTheOld(t *testing.T) {
	s, dir := serviceOf(t, 2)
	ctx := context.Background()
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tile(ctx, cutSpec, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(dir, "v2"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Version(), "v2") || s.Cache().Len() != 0 {
		t.Errorf("version %q with %d tiles held", s.Version(), s.Cache().Len())
	}
	if res, err := s.Tile(ctx, cutSpec, 0, 0, 0); err != nil || res.Cached {
		t.Errorf("a tile of the new version: cached %v, %v", res.Cached, err)
	}
	// And once more, which swaps back to the first of the two directories.
	if err := s.Refresh(dir, "v3"); err != nil {
		t.Fatal(err)
	}
	if res, err := s.Tile(ctx, cutSpec, 0, 0, 0); err != nil || res.Cached || !res.Drawn {
		t.Errorf("after the third open: %+v, %v", res.Cached, err)
	}
}

func TestService_TilesAskedForAtOnceAreDrawnOnce(t *testing.T) {
	s, dir := serviceOf(t, 2)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	var draws atomic.Int32
	release := make(chan struct{})
	s.onDraw = func() {
		draws.Add(1)
		<-release
	}
	var wg sync.WaitGroup
	results := make([]Result, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = s.Tile(context.Background(), cutSpec, 0, 0, 0)
		}()
	}
	// Let them all arrive at the one that is drawing.
	deadline := time.Now().Add(5 * time.Second)
	for draws.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if draws.Load() != 1 {
		t.Errorf("%d draws for one tile", draws.Load())
	}
	for i, r := range results {
		if len(r.Data) == 0 || string(r.Data) != string(results[0].Data) {
			t.Errorf("request %d got %d bytes", i, len(r.Data))
		}
	}
}

func TestService_NeverDrawsMoreAtOnceThanItIsGiven(t *testing.T) {
	s, dir := serviceOf(t, 2)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	var now, most atomic.Int32
	s.onDraw = func() {
		n := now.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		now.Add(-1)
	}
	var wg sync.WaitGroup
	for x := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Tile(context.Background(), cutSpec, 0, x, 0); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if most.Load() != 2 {
		t.Errorf("at most %d drawn at once, want 2", most.Load())
	}
}

func TestService_AViewerWhoGivesUpWhileWaitingIsNotDrawnFor(t *testing.T) {
	s, dir := serviceOf(t, 1)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	hold := make(chan struct{})
	started := make(chan struct{}, 1)
	s.onDraw = func() {
		select {
		case started <- struct{}{}:
		default:
		}
		<-hold
	}
	go func() { _, _ = s.Tile(context.Background(), cutSpec, 0, 0, 0) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.Tile(ctx, cutSpec, 0, 1, 0); err == nil {
		t.Error("a request that waited past its deadline was answered")
	}
	close(hold)
	if _, _, held := s.Cache().Get(Key{Version: s.Version(), Spec: cutSpec, X: 1}); held {
		t.Error("a tile nobody waited for was drawn and kept")
	}
}

func TestService_RefusesATileItCannotDraw(t *testing.T) {
	s, dir := serviceOf(t, 1)
	if err := s.Refresh(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tile(context.Background(), cutSpec, 1, 0, 0); err == nil {
		t.Error("a tile finer than the finest was drawn")
	}
	if _, err := s.Tile(context.Background(), cutSpec, MinZoom-1, 0, 0); err == nil {
		t.Error("a tile wider than the widest was drawn")
	}
	if _, err := s.Tile(context.Background(), Spec{Dim: chunks.Overworld, Cut: 999, Thickness: 16}, 0, 0, 0); err == nil {
		t.Error("a cut outside the dimension was drawn")
	}
}
