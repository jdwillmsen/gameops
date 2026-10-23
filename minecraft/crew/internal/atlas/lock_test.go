package atlas

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fastLock(t *testing.T) {
	t.Helper()
	oldPoll, oldStale, oldBeat := lockPoll, lockStale, lockBeat
	lockPoll, lockStale, lockBeat = 10*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { lockPoll, lockStale, lockBeat = oldPoll, oldStale, oldBeat })
}

// Units that share one cache directory and start together must fetch the pack
// once between them, not once each.
func TestEnsureSharedCacheFetchesOnceForConcurrentUnits(t *testing.T) {
	fastLock(t)
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	f := gh.fetcher()

	const units = 6
	var wg sync.WaitGroup
	errs := make(chan error, units)
	for range units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, Options{TileSize: 4})
			if err == nil && len(a.PNG()) == 0 {
				t.Error("empty atlas")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := gh.treeCalls.Load(); got != 1 {
		t.Errorf("tree listed %d times, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(cache, lockName)); !os.IsNotExist(err) {
		t.Errorf("build lock left behind: %v", err)
	}
}

// A cache that another unit has already filled is served without taking the
// lock, so a read-only mount of it still works.
func TestEnsureServesAFilledCacheWithoutWriting(t *testing.T) {
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	f := gh.fetcher()
	if _, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, Options{TileSize: 4}); err != nil {
		t.Fatal(err)
	}
	gh.Close()
	if err := os.Chmod(cache, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })
	if _, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, Options{TileSize: 4}); err != nil {
		t.Fatalf("read-only cache: %v", err)
	}
}

// A holder that died leaves its lock behind; once it stops being refreshed
// another unit takes over instead of waiting forever.
func TestEnsureTakesOverAStaleBuildLock(t *testing.T) {
	fastLock(t)
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	lock := filepath.Join(cache, lockName)
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(context.Background(), gh.fetcher(), "1.26.52.3", cache, Options{TileSize: 4}); err != nil {
		t.Fatal(err)
	}
	if got := gh.treeCalls.Load(); got != 1 {
		t.Errorf("tree listed %d times, want 1", got)
	}
}

// While a live holder builds, a waiting unit stops when its own deadline
// passes and does not break the lock.
func TestEnsureWaitsForALiveHolderUntilTheContextEnds(t *testing.T) {
	fastLock(t)
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	lock := filepath.Join(cache, lockName)
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				now := time.Now()
				_ = os.Chtimes(lock, now, now)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if _, _, err := Ensure(ctx, gh.fetcher(), "1.26.52.3", cache, Options{TileSize: 4}); err == nil {
		t.Fatal("want the context error while the lock is held")
	}
	if gh.requests.Load() != 0 {
		t.Errorf("a waiting unit made %d requests", gh.requests.Load())
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("a live lock was removed: %v", err)
	}
}

// A cache that cannot be written fails before the download, not after eight
// megabytes of it.
func TestEnsureFailsBeforeDownloadingWhenTheCacheIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	gh := fixtureGitHub(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	_, _, err := Ensure(context.Background(), gh.fetcher(), "1.26.52.3", filepath.Join(parent, "atlas"), Options{TileSize: 4})
	if err == nil {
		t.Fatal("want an error")
	}
	if gh.requests.Load() != 0 {
		t.Errorf("downloaded %d requests before failing", gh.requests.Load())
	}
}
