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
	return m.enqueue(unit, kind, body, operator, nil, nil)
}

func (m *Memory) EnqueueStep(_ context.Context, unit string, body json.RawMessage, operator string, job, step uuid.UUID) (Item, error) {
	return m.enqueue(unit, KindTask, body, operator, &job, &step)
}

func (m *Memory) enqueue(unit, kind string, body json.RawMessage, operator string, job, step *uuid.UUID) (Item, error) {
	if err := validateBody(kind, body); err != nil {
		return Item{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.now()
	it := &memItem{Item{
		ID: uuid.New(), Unit: unit, Kind: kind, Body: slices.Clone(body),
		State: Queued, Operator: operator, Created: t, Updated: t, JobID: job, StepID: step,
	}}
	m.items = append(m.items, it)
	return it.Item, nil
}

func (m *Memory) Next(_ context.Context, unit, kinds string, now time.Time) (Item, bool, time.Duration, error) {
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
		if kinds == "command" && it.Kind != KindCommand {
			continue
		}
		switch it.State {
		case Queued:
		case Delivered, Running:
			// A running item is silent only if its unit died; a live unit
			// keeps refreshing Updated.
			since := m.delivered[it.ID]
			if it.State == Running {
				since = it.Updated
			}
			if left := RedeliverAfter - now.Sub(since); left > 0 {
				if due == 0 || left < due {
					due = left
				}
				continue
			}
		default:
			continue
		}
		if it.Kind == KindTask && m.cancelPendingLocked(it) {
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

// cancelPendingLocked reports whether an open cancel_task names the task.
func (m *Memory) cancelPendingLocked(task *memItem) bool {
	for _, c := range m.items {
		if c.Unit != task.Unit || terminal(c.State) {
			continue
		}
		if id, ok := cancelTarget(c.Item); ok && id == task.ID {
			return true
		}
	}
	return false
}

func (m *Memory) Get(_ context.Context, id uuid.UUID) (Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID == id {
			return it.Item, nil
		}
	}
	return Item{}, ErrNotFound
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
		if target, ok := cancelTarget(it.Item); ok && state == Done {
			for _, t := range m.items {
				if t.ID == target && t.Unit == unit && !terminal(t.State) {
					t.State, t.Reason, t.Updated = Cancelled, "cancelled by operator", m.now()
				}
			}
		}
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

// cancelLocked is CancelTask for one item; the caller holds m.mu.
func (m *Memory) cancelLocked(it *memItem, operator string) (Item, *Item, error) {
	switch {
	case terminal(it.State):
		return it.Item, nil, nil
	case it.State == Queued:
		it.State, it.Reason, it.Updated = Cancelled, "cancelled by operator", m.now()
		return it.Item, nil, nil
	case it.Kind != KindTask:
		return Item{}, nil, errNotATask
	}
	now := m.now()
	cmd := &memItem{Item{
		ID: uuid.New(), Unit: it.Unit, Kind: KindCommand, Body: cancelTaskBody(it.ID),
		State: Queued, Operator: operator, Created: now, Updated: now,
	}}
	m.items = append(m.items, cmd)
	return it.Item, &cmd.Item, nil
}

func (m *Memory) CancelTask(_ context.Context, unit string, id uuid.UUID, operator string) (Item, *Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		if it.Unit != unit {
			return Item{}, nil, ErrWrongUnit
		}
		return m.cancelLocked(it, operator)
	}
	return Item{}, nil, ErrNotFound
}

func (m *Memory) CancelJob(_ context.Context, job uuid.UUID, operator string) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	// The slice header is read once, so commands appended below are not visited.
	for _, it := range m.items {
		if it.JobID == nil || *it.JobID != job || terminal(it.State) {
			continue
		}
		got, _, err := m.cancelLocked(it, operator)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

func (m *Memory) StopUnit(_ context.Context, unit, operator string, onlyIfOpen *uuid.UUID) ([]Item, Item, Item, bool, error) {
	commandBody := json.RawMessage(`{"name":"stop"}`)
	if err := validateBody(KindCommand, commandBody); err != nil {
		return nil, Item{}, Item{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var target Item
	if onlyIfOpen != nil {
		found := false
		for _, it := range m.items {
			if it.ID != *onlyIfOpen {
				continue
			}
			if it.Unit != unit {
				return nil, Item{}, Item{}, false, ErrWrongUnit
			}
			target, found = it.Item, true
			if terminal(it.State) {
				return nil, Item{}, target, false, nil
			}
			break
		}
		if !found {
			return nil, Item{}, Item{}, false, ErrNotFound
		}
	}
	var out []Item
	now := m.now()
	for _, it := range m.items {
		if it.Unit == unit && !terminal(it.State) {
			it.State, it.Updated = Cancelled, now
			out = append(out, it.Item)
		}
	}
	stop := Item{ID: uuid.New(), Unit: unit, Kind: KindCommand, Body: commandBody, State: Queued, Operator: operator, Created: now, Updated: now}
	m.items = append(m.items, &memItem{Item: stop})
	if onlyIfOpen != nil {
		for _, it := range out {
			if it.ID == *onlyIfOpen {
				target = it
				break
			}
		}
	}
	return out, stop, target, true, nil
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
