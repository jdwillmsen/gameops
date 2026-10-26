package events

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"
)

// Memory is the store for local runs and tests; it forgets everything on
// exit, like the work queue's.
type Memory struct {
	mu     sync.Mutex
	nextID int64
	events []Event
}

var _ Store = (*Memory)(nil)

func NewMemory() *Memory { return &Memory{} }

func (m *Memory) Append(_ context.Context, ev []Event) error {
	if len(ev) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range ev {
		m.nextID++
		e.ID = m.nextID
		if e.CauseRef == nil {
			e.CauseRef = LinkCause(e, m.events)
		}
		m.events = append(m.events, e)
	}
	return nil
}

// matches reports whether e answers every set filter of q.
func (q Query) matches(e Event) bool {
	if q.Unit != "" && e.Unit != q.Unit {
		return false
	}
	if q.Kind != "" && e.Kind != q.Kind {
		return false
	}
	if q.Severity != "" && e.Severity != q.Severity {
		return false
	}
	if q.ReasonCode != "" && e.ReasonCode != q.ReasonCode {
		return false
	}
	if q.Outcome != "" && e.Outcome != q.Outcome {
		return false
	}
	if q.SessionID != "" && e.SessionID != q.SessionID {
		return false
	}
	if q.CauseRef != 0 && (e.CauseRef == nil || *e.CauseRef != q.CauseRef) {
		return false
	}
	if !q.Since.IsZero() && e.At.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && e.At.After(q.Until) {
		return false
	}
	return true
}

// order sorts a whole scan by (At, ID) in the query's direction. The ID
// tiebreak is what makes keyset pagination exact on shared timestamps: the
// sort is always oldest-first on both columns, so a descending page is the
// exact reverse of an ascending one.
func order(e []Event, asc bool) {
	slices.SortFunc(e, func(a, b Event) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if !asc {
		slices.Reverse(e)
	}
}

func (m *Memory) Query(_ context.Context, q Query) (Page, error) {
	limit := defaultLimit
	if q.Limit > 0 {
		limit = min(q.Limit, maxLimit)
	}
	past := !q.Cursor.At.IsZero() || q.Cursor.ID != 0

	m.mu.Lock()
	scan := make([]Event, 0, len(m.events))
	for _, e := range m.events {
		if q.matches(e) {
			scan = append(scan, e)
		}
	}
	m.mu.Unlock()
	order(scan, q.Asc)

	var out []Event
	for _, e := range scan {
		if past && !q.beyond(q.Cursor, e) {
			continue
		}
		if len(out) == limit {
			// The probe row: it only says a next page exists.
			out = append(out, e)
			break
		}
		out = append(out, e)
	}
	var next *Cursor
	if len(out) > limit {
		last := out[limit-1]
		next = &Cursor{At: last.At, ID: last.ID}
		out = out[:limit]
	}
	return Page{Events: out, Next: next}, nil
}

// beyond reports whether e is past the cursor in the query's direction: a
// strict comparison on (At, ID), so rows that share a timestamp still page.
func (q Query) beyond(c Cursor, e Event) bool {
	if !e.At.Equal(c.At) {
		if q.Asc {
			return e.At.After(c.At)
		}
		return e.At.Before(c.At)
	}
	if q.Asc {
		return e.ID > c.ID
	}
	return e.ID < c.ID
}

func (m *Memory) Prune(_ context.Context, before time.Time, sample int, dryRun bool) (PruneReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rep PruneReport
	// kept aliases m.events' array, so it is only ever written when the
	// prune is real: a dry run that shifted rows down would corrupt the
	// stream it was only meant to read.
	kept := m.events[:0]
	write := !dryRun
	for _, e := range m.events {
		if !e.At.Before(before) {
			if write {
				kept = append(kept, e)
			}
			continue
		}
		rep.Total++
		if dryRun && len(rep.Would) < max(0, sample) {
			rep.Would = append(rep.Would, e)
		}
	}
	if write {
		// The tail that no longer belongs to anyone is cut off for real, or
		// the pruned rows would stay reachable behind the shorter slice.
		clear(m.events[len(kept):])
		m.events = kept
		rep.Pruned = rep.Total
	}
	slices.SortFunc(rep.Would, func(a, b Event) int { return a.At.Compare(b.At) })
	return rep, nil
}
