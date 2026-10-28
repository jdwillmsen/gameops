package life

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestUnitScratchKeepsOneEntryPerUnit(t *testing.T) {
	var s unitScratch[int]
	a, b := &Life{}, &Life{}
	*s.of(a) = 7
	if *s.of(a) != 7 {
		t.Error("a unit lost its entry between ticks")
	}
	if *s.of(b) != 0 {
		t.Error("two units shared an entry")
	}
	if _, ok := s.has(&Life{}); ok {
		t.Error("has made an entry for a unit that never asked")
	}
}

func TestUnitScratchRunsInitOnFirstUseOnly(t *testing.T) {
	calls := 0
	s := unitScratch[int]{init: func(v *int) { calls++; *v = 3 }}
	l := &Life{}
	*s.of(l)++
	if got := *s.of(l); got != 4 || calls != 1 {
		t.Errorf("entry = %d after %d inits, want 4 after 1", got, calls)
	}
}

func TestUnitScratchIsSafeForConcurrentUnits(t *testing.T) {
	var s unitScratch[int]
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		l := &Life{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				*s.of(l)++
			}
		}()
	}
	wg.Wait()
}

func TestUnitScratchIsBounded(t *testing.T) {
	var s unitScratch[int]
	keep := make([]*Life, 0, 3*unitScratchCap)
	for range 3 * unitScratchCap {
		l := &Life{}
		keep = append(keep, l)
		s.of(l)
	}
	s.mu.Lock()
	n := len(s.by)
	s.mu.Unlock()
	if n > unitScratchCap {
		t.Errorf("%d entries held, want at most %d", n, unitScratchCap)
	}
	runtime.KeepAlive(keep)
}

func TestUnitScratchDoesNotKeepAGoneUnitAlive(t *testing.T) {
	var s unitScratch[int]
	func() { s.of(&Life{}) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		s.mu.Lock()
		n := len(s.by)
		s.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the entry of a collected unit was never dropped")
}

func TestUnitScratchFullTableForgetsOnlyTheOldestLiveUnit(t *testing.T) {
	var s unitScratch[int]
	keep := make([]*Life, 0, unitScratchCap+1)
	for i := range unitScratchCap {
		l := &Life{}
		keep = append(keep, l)
		*s.of(l) = i + 1
	}
	extra := &Life{}
	keep = append(keep, extra)
	s.of(extra)

	if _, ok := s.has(keep[0]); ok {
		t.Error("the oldest unit kept its entry past the cap")
	}
	for i := 1; i < unitScratchCap; i++ {
		if v, ok := s.has(keep[i]); !ok || *v != i+1 {
			t.Fatalf("live unit %d lost its state when the table filled", i)
		}
	}
	s.mu.Lock()
	n := len(s.by)
	s.mu.Unlock()
	if n != unitScratchCap {
		t.Errorf("%d entries held, want exactly %d", n, unitScratchCap)
	}
	runtime.KeepAlive(keep)
}

func TestUnitScratchRegistersOneCleanupPerUnit(t *testing.T) {
	var s unitScratch[int]
	keep := make([]*Life, 0, unitScratchCap+1)
	for range unitScratchCap + 1 {
		l := &Life{}
		keep = append(keep, l)
		s.of(l)
	}
	for range 5 {
		s.of(keep[0])
		s.of(keep[1])
		s.of(keep[2])
	}
	s.mu.Lock()
	n := len(s.cleaned)
	s.mu.Unlock()
	if n != len(keep) {
		t.Errorf("%d cleanups tracked for %d units", n, len(keep))
	}
	runtime.KeepAlive(keep)
}
