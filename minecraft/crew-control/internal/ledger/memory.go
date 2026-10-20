package ledger

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
)

// Memory is the ledger for local runs and tests; it forgets everything on exit.
type Memory struct {
	mu     sync.Mutex
	places map[string]Place
	stock  map[string]map[string]Stock
}

var _ Ledger = (*Memory)(nil)

func NewMemory() *Memory {
	return &Memory{places: map[string]Place{}, stock: map[string]map[string]Stock{}}
}

func clonePlace(p Place) Place {
	p.Items = slices.Clone(p.Items)
	return p
}

func (m *Memory) sorted() []Place {
	out := make([]Place, 0, len(m.places))
	for _, p := range m.places {
		out = append(out, clonePlace(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Memory) Places(context.Context) ([]Place, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sorted(), nil
}

func (m *Memory) Place(_ context.Context, id string) (Place, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.places[id]
	return clonePlace(p), ok, nil
}

func (m *Memory) PutPlace(_ context.Context, p Place) error {
	if err := Validate(p); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.places[p.ID]
	if ok && old.Seeded {
		return ErrSeeded
	}
	if err := checkAutoID(p, old, ok); err != nil {
		return err
	}
	if p.Kind == KindChest {
		for _, o := range m.places {
			if o.ID != p.ID && o.Kind == KindChest && o.Box.Min == p.Box.Min {
				return ErrConflict
			}
		}
	}
	p.Seeded = false
	m.places[p.ID] = clonePlace(p)
	return nil
}

func (m *Memory) DeletePlace(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.places[id]
	if !ok {
		return ErrNotFound
	}
	if p.Seeded {
		return ErrSeeded
	}
	delete(m.places, id)
	delete(m.stock, id)
	return nil
}

func (m *Memory) Seed(_ context.Context, ps []Place) error {
	keep := map[string]bool{}
	for _, p := range ps {
		if err := validSeed(p); err != nil {
			return err
		}
		keep[p.ID] = true
	}
	if err := positionsFree(ps); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.places {
		if p.Seeded && !keep[id] {
			delete(m.places, id)
			delete(m.stock, id)
		}
	}
	for _, p := range ps {
		p.Seeded = true
		m.places[p.ID] = clonePlace(p)
		if p.Kind == KindChest {
			m.foldLocked(p)
		}
	}
	return nil
}

func newest(rows map[string]Stock) (t time.Time) {
	for _, s := range rows {
		if s.SeenAt.After(t) {
			t = s.SeenAt
		}
	}
	return t
}

// foldLocked merges unseeded chests at a seeded chest's position into it.
func (m *Memory) foldLocked(seeded Place) {
	for id, o := range m.places {
		if id == seeded.ID || o.Seeded || o.Kind != KindChest || o.Box.Min != seeded.Box.Min {
			continue
		}
		if len(m.stock[id]) > 0 && newest(m.stock[id]).After(newest(m.stock[seeded.ID])) {
			m.stock[seeded.ID] = m.stock[id]
		}
		delete(m.places, id)
		delete(m.stock, id)
	}
}

func (m *Memory) chestAt(pos Pos) (Place, bool) {
	var found *Place
	for _, p := range m.places {
		if p.Kind == KindChest && p.Box.Min == pos &&
			(found == nil || (p.Seeded && !found.Seeded) || (p.Seeded == found.Seeded && p.ID < found.ID)) {
			p := p
			found = &p
		}
	}
	if found == nil {
		return Place{}, false
	}
	return clonePlace(*found), true
}

func (m *Memory) ChestAt(_ context.Context, pos Pos) (Place, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.chestAt(pos)
	return p, ok, nil
}

func (m *Memory) Observe(_ context.Context, unit string, at time.Time, chest Pos, items map[string]int, partial bool) error {
	if !validPos(chest) {
		return invalid("chest is outside the world")
	}
	items = cleanObservation(items)
	if now := time.Now(); at.After(now) {
		at = now
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.chestAt(chest)
	if !ok {
		p = autoChest(chest)
		m.places[p.ID] = p
	}
	rows := m.stock[p.ID]
	for _, s := range rows {
		if s.SeenAt.After(at) {
			return nil
		}
	}
	if rows == nil {
		rows = map[string]Stock{}
		m.stock[p.ID] = rows
	}
	if !partial {
		for item := range rows {
			if _, still := items[item]; !still {
				items[item] = 0
			}
		}
	}
	for item, n := range items {
		rows[item] = Stock{Item: item, Count: n, SeenAt: at, Unit: unit}
	}
	return nil
}

func (m *Memory) Stock(_ context.Context, placeID string) ([]Stock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Stock, 0, len(m.stock[placeID]))
	for _, s := range m.stock[placeID] {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Item < out[j].Item })
	return out, nil
}
