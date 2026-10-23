package detail

import (
	"context"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/pkg/playerdata"
	"github.com/jdwillmsen/gameops/minecraft/pkg/worldview"
)

// Save reads the player records out of a world's save directory, and keeps
// them for a few seconds so a detail view costs the save one read and not
// one a request. It is the file source behind Source; a deployment wires
// it to the lab world's db directory.
//
// The world is opened on links under WorkDir (see worldview.Open), so a
// read writes nothing where the world lies — not even the LOCK file a
// read-only LevelDB open creates. WorkDir must therefore be somewhere
// writable and is best on the same volume as the world, where links are
// cheap; elsewhere every read copies the save's files.
type Save struct {
	// WorldDir is the world's db directory, the one holding the LevelDB.
	WorldDir string
	// WorkDir is the scratch a read builds its view of the world in.
	WorkDir string
	// Every is the least time between reads of the save; zero is the
	// default of a few seconds.
	Every time.Duration
	// Timeout bounds one read; zero means no bound.
	Timeout time.Duration
	Log     *logging.Logger

	mu       sync.Mutex
	store    playerdata.Store
	lastRead time.Time
	warned   bool
}

func (s *Save) every() time.Duration {
	if s.Every > 0 {
		return s.Every
	}
	return DefaultEvery
}

// Lookup returns the player's record, reading the save first when the held
// one is stale. A read that fails leaves the last good records where they
// are, so a world that cannot be read costs the answer its freshness and
// not what it held.
func (s *Save) Lookup(playerID string) (playerdata.Record, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastRead.IsZero() || time.Since(s.lastRead) >= s.every() {
		s.readLocked()
	}
	return s.store.Lookup(playerID)
}

// Scanned reports when the save was last read, without reading it.
func (s *Save) Scanned() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Scanned()
}

func (s *Save) readLocked() {
	ctx := context.Background()
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	db, done, err := worldview.Open(s.WorldDir, s.WorkDir)
	if err != nil {
		s.failLocked(err)
		return
	}
	defer done()
	found, _, err := playerdata.Read(ctx, db)
	if err != nil {
		s.failLocked(err)
		return
	}
	s.warned = false
	now := time.Now()
	s.lastRead = now
	s.store.Set(now, found)
}

func (s *Save) failLocked(err error) {
	// A failed read waits out Every like a good one: otherwise a world that
	// cannot be read would be linked or copied afresh by every request.
	s.lastRead = time.Now()
	// One warning until a read succeeds again: without it, a detail view
	// left open beside an unreadable world would say so every few seconds.
	if !s.warned && s.Log != nil {
		s.warned = true
		s.Log.Error("world_read_failed", logging.Fields{"error": err.Error()})
	}
}
