package life

import (
	"runtime"
	"sync"
	"weak"
)

// unitScratchCap bounds one table. Units number in the dozens, so reaching it
// means a process that churns sessions faster than the collector reclaims
// them; the table then starts over, which costs each live unit one forgotten
// cooldown and nothing more.
const unitScratchCap = 256

// unitScratch is per-unit memory for a tactic. Tactics are registered once
// and shared by every unit in the process, so what one unit remembers cannot
// live in the tactic. The key is weak so a unit that is gone takes its entry
// with it, and the lock makes it safe for units stepping on separate
// goroutines. The zero value is ready to use.
type unitScratch[T any] struct {
	mu sync.Mutex
	by map[weak.Pointer[Life]]*T
	// init prepares a fresh entry; nil leaves it zero.
	init func(*T)
}

// of is the unit's entry, made on first use. The pointer stays valid after
// the table is cleared, so a tick that holds it finishes undisturbed.
func (s *unitScratch[T]) of(l *Life) *T {
	key := weak.Make(l)
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.by[key]; ok {
		return v
	}
	if s.by == nil || len(s.by) >= unitScratchCap {
		s.by = map[weak.Pointer[Life]]*T{}
	}
	v := new(T)
	if s.init != nil {
		s.init(v)
	}
	s.by[key] = v
	runtime.AddCleanup(l, s.drop, key)
	return v
}

func (s *unitScratch[T]) drop(key weak.Pointer[Life]) {
	s.mu.Lock()
	delete(s.by, key)
	s.mu.Unlock()
}

// has says whether the unit has an entry, without making one.
func (s *unitScratch[T]) has(l *Life) (*T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.by[weak.Make(l)]
	return v, ok
}
