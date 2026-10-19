package work

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Memory is the store for local runs and tests; it forgets everything on exit.
type Memory struct {
	mu        sync.Mutex
	items     []*memItem // insertion order is creation order
	now       func() time.Time
	delivered map[uuid.UUID]time.Time
}

type memItem struct{ Item }

func NewMemory() *Memory {
	return &Memory{now: time.Now, delivered: map[uuid.UUID]time.Time{}}
}

func (m *Memory) Enqueue(_ context.Context, unit, kind string, body json.RawMessage, operator string) (Item, error) {
	if err := validateBody(kind, body); err != nil {
		return Item{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.now()
	it := &memItem{Item{
		ID: uuid.New(), Unit: unit, Kind: kind, Body: slices.Clone(body),
		State: Queued, Operator: operator, Created: t, Updated: t,
	}}
	m.items = append(m.items, it)
	return it.Item, nil
}

func (m *Memory) Next(_ context.Context, unit string, now time.Time) (Item, bool, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var (
		pick *memItem
		due  time.Duration
	)
	for _, it := range m.items {
		if it.Unit != unit {
			continue
		}
		switch it.State {
		case Queued:
		case Delivered:
			if left := RedeliverAfter - now.Sub(m.delivered[it.ID]); left > 0 {
				if due == 0 || left < due {
					due = left
				}
				continue
			}
		default:
			continue
		}
		// Items are in creation order, so the first of a kind is the oldest.
		if pick == nil || (it.Kind == KindCommand && pick.Kind != KindCommand) {
			pick = it
		}
	}
	if pick == nil {
		return Item{}, false, due, nil
	}
	pick.State = Delivered
	pick.Updated = now
	m.delivered[pick.ID] = now
	return pick.Item, true, 0, nil
}

func (m *Memory) Apply(_ context.Context, unit string, id uuid.UUID, state string, progress int, reason string) (Item, error) {
	if err := validateReport(state); err != nil {
		return Item{}, err
	}
	progress, reason = cleanReport(progress, reason)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		if it.Unit != unit {
			return Item{}, ErrWrongUnit
		}
		if terminal(it.State) {
			return it.Item, nil
		}
		it.State, it.Progress, it.Reason, it.Updated = state, progress, reason, m.now()
		return it.Item, nil
	}
	return Item{}, ErrNotFound
}

func (m *Memory) CancelQueued(_ context.Context, unit string, id uuid.UUID) (Item, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		if it.Unit != unit {
			return Item{}, false, ErrWrongUnit
		}
		if it.State != Queued {
			return it.Item, false, nil
		}
		it.State, it.Reason, it.Updated = Cancelled, "cancelled by operator", m.now()
		return it.Item, true, nil
	}
	return Item{}, false, ErrNotFound
}

func (m *Memory) CancelOpen(_ context.Context, unit string) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, it := range m.items {
		if it.Unit == unit && !terminal(it.State) {
			it.State, it.Updated = Cancelled, m.now()
			out = append(out, it.Item)
		}
	}
	return out, nil
}

func (m *Memory) Recent(_ context.Context, unit string, n int) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for i := len(m.items) - 1; i >= 0 && len(out) < n; i-- {
		if m.items[i].Unit == unit {
			out = append(out, m.items[i].Item)
		}
	}
	return out, nil
}

func (m *Memory) Prune(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	m.items = slices.DeleteFunc(m.items, func(it *memItem) bool {
		old := it.Created.Before(before)
		if old {
			delete(m.delivered, it.ID)
			n++
		}
		return old
	})
	return n, nil
}
