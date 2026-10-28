package life

import (
	"runtime"
	"sync"
	"weak"
)

// unitScratchCap bounds one table. Units number in the dozens, so reaching it
// means a process that churns sessions faster than the collector reclaims
// them; the table then lets go of the dead units' entries first and, failing
// that, of the oldest, which costs one unit one forgotten cooldown and nothing
// more.
const unitScratchCap = 256

// unitScratch is per-unit memory for a tactic. Tactics are registered once
// and shared by every unit in the process, so what one unit remembers cannot
// live in the tactic. The key is weak so a unit that is gone takes its entry
// with it, and the lock makes it safe for units stepping on separate
// goroutines. The zero value is ready to use.
type unitScratch[T any] struct {
	mu sync.Mutex
	by map[weak.Pointer[Life]]*scratchEntry[T]
	// cleaned holds the keys that already have a cleanup registered, so a
	// unit evicted and seen again does not register a second one.
	cleaned map[weak.Pointer[Life]]struct{}
	seq     uint64
	// init prepares a fresh entry; nil leaves it zero.
	init func(*T)
}

type scratchEntry[T any] struct {
	v   *T
	seq uint64
}

// of is the unit's entry, made on first use. The pointer stays valid after
// the entry is evicted, so a tick that holds it finishes undisturbed.
func (s *unitScratch[T]) of(l *Life) *T {
	key := weak.Make(l)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.by[key]; ok {
		return e.v
	}
	if s.by == nil {
		s.by = map[weak.Pointer[Life]]*scratchEntry[T]{}
		s.cleaned = map[weak.Pointer[Life]]struct{}{}
	}
	if len(s.by) >= unitScratchCap {
		s.evict()
	}
	v := new(T)
	if s.init != nil {
		s.init(v)
	}
	s.seq++
	s.by[key] = &scratchEntry[T]{v: v, seq: s.seq}
	if _, ok := s.cleaned[key]; !ok {
		s.cleaned[key] = struct{}{}
		runtime.AddCleanup(l, s.drop, key)
	}
	return v
}

// evict makes room for one entry: the entries of units already collected
// first, else the single oldest. The caller holds the lock.
func (s *unitScratch[T]) evict() {
	for k := range s.by {
		if k.Value() == nil {
			delete(s.by, k)
		}
	}
	if len(s.by) < unitScratchCap {
		return
	}
	var (
		oldest weak.Pointer[Life]
		low    = ^uint64(0)
	)
	for k, e := range s.by {
		if e.seq < low {
			oldest, low = k, e.seq
		}
	}
	delete(s.by, oldest)
}

func (s *unitScratch[T]) drop(key weak.Pointer[Life]) {
	s.mu.Lock()
	delete(s.by, key)
	delete(s.cleaned, key)
	s.mu.Unlock()
}

// has says whether the unit has an entry, without making one.
func (s *unitScratch[T]) has(l *Life) (*T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.by[weak.Make(l)]
	if !ok {
		return nil, false
	}
	return e.v, true
}
