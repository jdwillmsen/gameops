package events

// Snapshotter is an optional fast path: a store that already holds every
// event in memory hands them over in one copy instead of being paged. The
// bool is false when the store cannot, so callers fall back to Query.
type Snapshotter interface {
	Snapshot() ([]Event, bool)
}

var (
	_ Snapshotter = (*Memory)(nil)
	_ Snapshotter = (*Lazy)(nil)
)

func (m *Memory) Snapshot() ([]Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.events...), true
}

func (l *Lazy) Snapshot() ([]Event, bool) {
	s, err := l.get()
	if err != nil {
		return nil, false
	}
	if sn, ok := s.(Snapshotter); ok {
		return sn.Snapshot()
	}
	return nil, false
}
