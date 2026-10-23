package atlas

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const lockName = ".build.lock"

// The pack host rate-limits anonymous API calls by address, and every unit
// leaves the cluster from the same one, so units sharing a cache directory
// take turns: one builds and the rest wait for its result. Timings are
// variables for tests. The staleness limit is several heartbeats, so a
// network filesystem's attribute caching cannot make a live holder look dead.
var (
	lockPoll  = 5 * time.Second
	lockBeat  = 30 * time.Second
	lockStale = 5 * time.Minute
)

var errBuildHeld = errors.New("atlas: another unit is building")

// lockBuild takes the cache's build lock, creating the cache directory first
// so an unwritable one fails here and not after the download. The lock is a
// directory because mkdir is atomic on NFS where an exclusive file create is
// not reliably so. While held it is touched regularly; release stops that and
// removes it.
func lockBuild(cacheDir string) (release func(), err error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	lock := filepath.Join(cacheDir, lockName)
	if err := os.Mkdir(lock, 0o755); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		fi, serr := os.Stat(lock)
		if serr != nil || time.Since(fi.ModTime()) < lockStale {
			return nil, errBuildHeld
		}
		// Two units can both find it stale and both build. That wastes a
		// download but is harmless: the saved files are identical.
		if err := os.Remove(lock); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := os.Mkdir(lock, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return nil, errBuildHeld
			}
			return nil, err
		}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(lockBeat)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				_ = os.Chtimes(lock, now, now)
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
		_ = os.Remove(lock)
	}, nil
}

// waitForBuild pauses between attempts at the lock, ending early with the
// context's error.
func waitForBuild(ctx context.Context) error {
	select {
	case <-time.After(lockPoll):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
