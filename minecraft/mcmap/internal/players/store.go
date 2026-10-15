package players

import (
	"math"
	"strconv"
	"sync"
	"time"
)

// Store holds the players of the last snapshot read. A snapshot replaces
// all of it at once, so an answer is never made of two.
type Store struct {
	mu sync.RWMutex
	at time.Time
	// byID is keyed by the UniqueID's 64 bits as unsigned, so that both
	// readings of an id are looked up the same way.
	byID    map[uint64]Record
	scanned bool
}

// Set replaces the players with those of the snapshot taken at at.
func (s *Store) Set(at time.Time, byID map[int64]Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keyed := make(map[uint64]Record, len(byID))
	for id, r := range byID {
		keyed[bits(id)] = r
	}
	s.at, s.byID, s.scanned = at, keyed, true
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
	var key uint64
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		key = bits(n)
	} else if u, err := strconv.ParseUint(id, 10, 64); err == nil {
		key = u
	} else {
		return Record{}, time.Time{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byID[key]
	return r, s.at, ok
}

// bits is a signed id's 64 bits as the unsigned number they also read as:
// a negative one is counted back from the top.
func bits(n int64) uint64 {
	if n >= 0 {
		return uint64(n)
	}
	return math.MaxUint64 - uint64(-(n + 1))
}
