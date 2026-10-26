package replay

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Memory is the store for local runs, tests, and for the stretch before the
// database answers. It forgets everything on exit.
type Memory struct {
	mu      sync.Mutex
	nextID  int64
	samples []Sample
}

var _ Store = (*Memory)(nil)

func NewMemory() *Memory { return &Memory{} }

func (m *Memory) Append(_ context.Context, s []Sample) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range s {
		m.nextID++
		x.ID = m.nextID
		m.samples = append(m.samples, x)
	}
	return nil
}

func (m *Memory) Query(_ context.Context, q Query) (Page, error) {
	limit := q.limit()
	m.mu.Lock()
	scan := make([]Sample, 0, len(m.samples))
	for _, s := range m.samples {
		if q.Unit != "" && s.Unit != q.Unit {
			continue
		}
		if !q.Since.IsZero() && s.At.Before(q.Since) || !q.Until.IsZero() && s.At.After(q.Until) {
			continue
		}
		if q.paged() && !after(q.Cursor, s) {
			continue
		}
		scan = append(scan, s)
	}
	m.mu.Unlock()
	slices.SortFunc(scan, func(a, b Sample) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	if len(scan) <= limit {
		return Page{Samples: scan}, nil
	}
	last := scan[limit-1]
	return Page{Samples: scan[:limit], Next: &Cursor{At: last.At, ID: last.ID}}, nil
}

// after is the strict (At, ID) comparison a keyset cursor walks.
func after(c Cursor, s Sample) bool {
	if !s.At.Equal(c.At) {
		return s.At.After(c.At)
	}
	return s.ID > c.ID
}

func (m *Memory) Prune(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.samples[:0]
	for _, s := range m.samples {
		if !s.At.Before(before) {
			kept = append(kept, s)
		}
	}
	n := len(m.samples) - len(kept)
	clear(m.samples[len(kept):])
	m.samples = kept
	return n, nil
}

// trim drops the oldest samples beyond max and reports how many went.
func (m *Memory) trim(max int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	over := len(m.samples) - max
	if over <= 0 {
		return 0
	}
	slices.SortStableFunc(m.samples, func(a, b Sample) int { return a.At.Compare(b.At) })
	m.samples = append([]Sample(nil), m.samples[over:]...)
	return over
}
