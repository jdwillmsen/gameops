package players

import (
	"maps"
	"strconv"
	"sync"
	"time"
)

// Store holds the players of the last snapshot read. A snapshot replaces
// all of it at once, so an answer is never made of two.
type Store struct {
	mu      sync.RWMutex
	at      time.Time
	byID    map[int64]Record
	scanned bool
}

// Set replaces the players with those of the snapshot taken at at.
func (s *Store) Set(at time.Time, byID map[int64]Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at, s.byID, s.scanned = at, maps.Clone(byID), true
}

// Scanned reports whether any snapshot has been read, and when it was
// taken. Until one has, no record means only that none is loaded yet.
func (s *Store) Scanned() (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.at, s.scanned
}

// Lookup finds a player by the id the game's scripts report for them. That
// is the UniqueID written as a signed decimal; it is also taken as the
// unsigned reading of the same 64 bits, which a script runtime that holds
// ids as unsigned numbers would print.
func (s *Store) Lookup(id string) (Record, time.Time, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		u, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return Record{}, time.Time{}, false
		}
		n = int64(u)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[n]
	return r, s.at, ok
}
