package explorer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

const (
	maxViews     = 50
	maxViewName  = 60
	maxViewQuery = 1024
)

var (
	ErrViewLimit    = errors.New("explorer: too many saved views")
	ErrViewNotFound = errors.New("explorer: no such view")
)

// View is a named filter set. Query is the canonical URL query of a Params,
// so opening a view is the same as following a deep link.
type View struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Query   string    `json:"query"`
	Created time.Time `json:"created"`
}

// ViewStore keeps saved views per operator. A view belongs to the operator
// who made it; no method crosses operators.
type ViewStore interface {
	List(operator string) ([]View, error)
	// Save adds a view, or replaces the operator's view of the same name.
	Save(operator, name, query string) (View, error)
	Delete(operator, id string) error
}

// MemoryViews forgets views on exit, like the other in-memory stores.
type MemoryViews struct {
	mu  sync.Mutex
	now func() time.Time
	by  map[string][]View
}

var _ ViewStore = (*MemoryViews)(nil)

func NewMemoryViews() *MemoryViews {
	return &MemoryViews{now: time.Now, by: map[string][]View{}}
}

func (m *MemoryViews) List(op string) ([]View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]View(nil), m.by[op]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemoryViews) Save(op, name, query string) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.by[op]
	for i := range l {
		if l[i].Name == name {
			l[i].Query = query
			return l[i], nil
		}
	}
	if len(l) >= maxViews {
		return View{}, ErrViewLimit
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	v := View{ID: hex.EncodeToString(b[:]), Name: name, Query: query, Created: m.now().UTC()}
	m.by[op] = append(l, v)
	return v, nil
}

func (m *MemoryViews) Delete(op, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.by[op]
	for i := range l {
		if l[i].ID == id {
			m.by[op] = append(l[:i:i], l[i+1:]...)
			return nil
		}
	}
	return ErrViewNotFound
}
