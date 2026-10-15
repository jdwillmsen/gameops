package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/players"
)

type fakePlayers struct {
	dirs []string
	ats  []time.Time
	err  error
}

func (f *fakePlayers) Extract(_ context.Context, dbDir string, at time.Time) (players.Stats, error) {
	f.dirs = append(f.dirs, dbDir)
	f.ats = append(f.ats, at)
	return players.Stats{}, f.err
}

func TestCycle_ReadsThePlayersOfEachSnapshot(t *testing.T) {
	p := &fakePlayers{}
	w := newWorker(&fakeSyncer{}, &fakeRenderer{}, "")
	w.Players = p
	w.Cycle(context.Background(), noon)
	if len(p.dirs) != 1 || p.dirs[0] != filepath.Join("/data/mirror", "FWB", "db") || !p.ats[0].Equal(noon) {
		t.Errorf("read %v at %v", p.dirs, p.ats)
	}
}

func TestCycle_PlayersThatCannotBeReadStopNothing(t *testing.T) {
	r := &fakeRenderer{}
	w := newWorker(&fakeSyncer{}, r, "")
	w.Players = &fakePlayers{err: errors.New("deadline exceeded")}
	if got := w.Cycle(context.Background(), noon); got != Applied || len(r.rendered) != 3 {
		t.Errorf("outcome %v, rendered %v", got, r.rendered)
	}
}
