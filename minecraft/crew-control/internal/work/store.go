// Package work is the queue crew-control hands to Crew units and the HTTP
// routes a unit polls it through.
package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	KindTask    = "task"
	KindCommand = "command"

	Queued    = "queued"
	Delivered = "delivered"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"

	// A unit that took an item and died never reports on it; offering it
	// again after this long is what keeps that work from being lost.
	RedeliverAfter = 30 * time.Second

	maxReason = 500
)

var (
	ErrNotFound    = errors.New("work: no such item")
	ErrWrongUnit   = errors.New("work: item belongs to another unit")
	ErrInvalid     = errors.New("work: invalid item")
	ErrUnavailable = errors.New("work: store unavailable")
)

type Item struct {
	ID       uuid.UUID
	Unit     string
	Kind     string
	Body     json.RawMessage
	State    string
	Progress int
	Reason   string
	Operator string
	Created  time.Time
	Updated  time.Time
}

type Store interface {
	Enqueue(ctx context.Context, unit, kind string, body json.RawMessage, operator string) (Item, error)
	// Next returns the oldest command, else the oldest task, that is queued
	// or delivered, or running without a report, for more than RedeliverAfter
	// before now, and marks it delivered. A running item that goes quiet means
	// its unit restarted and lost the task.
	// With nothing to hand out, redeliverIn is how long until a delivered item
	// becomes due again (zero when none is), so a poller can wake for it.
	// A database-backed store takes now from the database, not the argument,
	// so every timestamp it writes and compares shares one clock.
	Next(ctx context.Context, unit string, now time.Time) (it Item, ok bool, redeliverIn time.Duration, err error)
	// Apply records a unit's report. An item already in a terminal state is
	// returned unchanged.
	Apply(ctx context.Context, unit string, id uuid.UUID, state string, progress int, reason string) (Item, error)
	// CancelQueued cancels the item only while it is still queued. It reports
	// false, with the item as it now is, when the unit already took it or it
	// finished, so a caller never cancels work a unit may be running.
	CancelQueued(ctx context.Context, unit string, id uuid.UUID) (Item, bool, error)
	StopUnit(ctx context.Context, unit, operator string, onlyIfOpen *uuid.UUID) (cancelled []Item, stop Item, target Item, stopped bool, err error)
	// Recent returns the unit's newest items first.
	Recent(ctx context.Context, unit string, n int) ([]Item, error)
	Prune(ctx context.Context, before time.Time) (int, error)
}

func terminal(state string) bool {
	return state == Done || state == Failed || state == Cancelled
}

// validateBody keeps out anything the unit client would fail to decode: it
// would take the item, report it failed, and the operator would learn nothing.
func validateBody(kind string, body json.RawMessage) error {
	switch kind {
	case KindTask:
		var m map[string]json.RawMessage
		if json.Unmarshal(body, &m) != nil || m == nil {
			return errors.Join(ErrInvalid, errors.New("task body must be a JSON object"))
		}
	case KindCommand:
		var c struct {
			Name string `json:"name"`
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if dec.Decode(&c) != nil || c.Name != "stop" || dec.More() {
			return errors.Join(ErrInvalid, errors.New(`command body must be {"name":"stop"}`))
		}
	default:
		return errors.Join(ErrInvalid, errors.New("unknown kind"))
	}
	return nil
}

func validateReport(state string) error {
	switch state {
	case Running, Done, Failed, Cancelled:
		return nil
	}
	return errors.Join(ErrInvalid, errors.New("unknown state"))
}

func cleanReport(progress int, reason string) (int, string) {
	progress = max(0, min(progress, 100))
	if r := []rune(reason); len(r) > maxReason {
		reason = string(r[:maxReason])
	}
	return progress, reason
}

// Lazy stands in for a store that is not connected yet, so the routes can be
// mounted at start and answer 503 until the database is reachable.
type Lazy struct {
	mu      sync.RWMutex
	s       Store
	onReady func()
}

// OnReady registers f to run each time a store is set. Streams that opened
// while the store was down skipped their history, and this is how they hear
// that it can be read now.
func (l *Lazy) OnReady(f func()) {
	l.mu.Lock()
	l.onReady = f
	l.mu.Unlock()
}

func (l *Lazy) Set(s Store) {
	l.mu.Lock()
	l.s = s
	f := l.onReady
	l.mu.Unlock()
	if f != nil {
		f()
	}
}

func (l *Lazy) get() (Store, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.s == nil {
		return nil, ErrUnavailable
	}
	return l.s, nil
}

func (l *Lazy) Enqueue(ctx context.Context, unit, kind string, body json.RawMessage, operator string) (Item, error) {
	s, err := l.get()
	if err != nil {
		return Item{}, err
	}
	return s.Enqueue(ctx, unit, kind, body, operator)
}

func (l *Lazy) Next(ctx context.Context, unit string, now time.Time) (Item, bool, time.Duration, error) {
	s, err := l.get()
	if err != nil {
		return Item{}, false, 0, err
	}
	return s.Next(ctx, unit, now)
}

func (l *Lazy) Apply(ctx context.Context, unit string, id uuid.UUID, state string, progress int, reason string) (Item, error) {
	s, err := l.get()
	if err != nil {
		return Item{}, err
	}
	return s.Apply(ctx, unit, id, state, progress, reason)
}

func (l *Lazy) CancelQueued(ctx context.Context, unit string, id uuid.UUID) (Item, bool, error) {
	s, err := l.get()
	if err != nil {
		return Item{}, false, err
	}
	return s.CancelQueued(ctx, unit, id)
}

func (l *Lazy) StopUnit(ctx context.Context, unit, operator string, onlyIfOpen *uuid.UUID) ([]Item, Item, Item, bool, error) {
	s, err := l.get()
	if err != nil {
		return nil, Item{}, Item{}, false, err
	}
	return s.StopUnit(ctx, unit, operator, onlyIfOpen)
}

func (l *Lazy) Recent(ctx context.Context, unit string, n int) ([]Item, error) {
	s, err := l.get()
	if err != nil {
		return nil, err
	}
	return s.Recent(ctx, unit, n)
}

func (l *Lazy) Prune(ctx context.Context, before time.Time) (int, error) {
	s, err := l.get()
	if err != nil {
		return 0, err
	}
	return s.Prune(ctx, before)
}
