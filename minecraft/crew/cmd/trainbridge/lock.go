package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// lockPoll is how often a waiting run retries a squad lock another run holds.
const lockPoll = 200 * time.Millisecond

// lockSquads takes the exclusive lock of every squad, in name order so two
// runs wanting the same pair cannot each hold one. A squad has one profile
// and one set of units, so a second trainbridge touching it would overwrite
// the first one's candidate mid-fight and score the wrong profile. It waits
// for the holder until ctx ends, and then fails as a plain fault rather than
// an aborted fight, since nothing was measured and nothing was pushed. The kernel drops a flock when its holder
// dies, so a crashed run leaves nothing stale.
func lockSquads(ctx context.Context, dir string, squads []string) (release func(), err error) {
	squads = slices.Sorted(slices.Values(squads))
	squads = slices.Compact(squads)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var held []*os.File
	release = func() {
		for _, f := range held {
			f.Close()
		}
	}
	for _, sq := range squads {
		if !labSquadID.MatchString(sq) {
			release()
			return nil, fmt.Errorf("trainbridge: squad %q is not a lab squad id", sq)
		}
		f, err := os.OpenFile(filepath.Join(dir, "squad-"+sq+".lock"), os.O_RDWR|os.O_CREATE|noFollow, 0o600)
		if err != nil {
			release()
			return nil, err
		}
		held = append(held, f)
		if err := flockWait(ctx, f); err != nil {
			release()
			return nil, fmt.Errorf("trainbridge: squad %q is in use by another run: %v", sq, err)
		}
	}
	return release, nil
}

func flockWait(ctx context.Context, f *os.File) error {
	for {
		err := tryFlock(f)
		if !errors.Is(err, errWouldBlock) {
			return err
		}
		t := time.NewTimer(lockPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
