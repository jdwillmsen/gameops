// Package events is the persistent crew unit event stream: what a unit did
// and what happened to it, kept in Postgres for a console to filter, sort
// and replay, and the HTTP routes that fill and read it.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

// Severity is the console's whole vocabulary for colouring and toasts; the
// schema's CHECK constraint carries the same list.
const (
	SevOK   = "ok"
	SevInfo = "info"
	SevWarn = "warn"
	SevCrit = "crit"
)

// A page is bounded whatever the caller asks: an unbounded query is a way to
// ask a console to render the whole retention window at once.
const (
	defaultLimit = 50
	maxLimit     = 200
)

func validSeverity(s string) bool {
	switch s {
	case SevOK, SevInfo, SevWarn, SevCrit:
		return true
	}
	return false
}

// Pos is where a unit stood when the event happened, block coordinates.
// Nil means the event happened nowhere in particular (offline, respawn).
type Pos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Event is one unit event as it is stored. ID is assigned by the store, and
// is part of the pagination cursor, so it is stable only within one store.
type Event struct {
	ID       int64
	Unit     string
	At       time.Time
	Kind     string
	Severity string
	// TaskID and JobID name the work a task event is about, when they are
	// known. They are attributes, not references: pruning work must not
	// take the event history with it.
	TaskID  *uuid.UUID
	JobID   *uuid.UUID
	Pos     *Pos
	Payload json.RawMessage

	// The analytic columns. All are optional: an event from an older unit,
	// or one of a kind they do not describe, leaves them zero. None holds a
	// coordinate, so a reader of these is not a reader of positions.
	ReasonCode  string
	DurationMS  *int
	Attempt     *int
	SessionID   string
	CrewVersion string
	// CauseRef is the id of the earlier event on the same unit that most
	// likely explains this one; see LinkCause.
	CauseRef *int64
	// Outcome is ok, noop or failed; see Outcome.
	Outcome string
}

var (
	ErrNotFound    = errors.New("events: no such event")
	ErrInvalid     = errors.New("events: invalid event")
	ErrUnavailable = work.ErrUnavailable
)

// Query is what a console asks for. The zero value is "everything, newest
// first, one page"; each filter is optional and exact.
type Query struct {
	Unit     string
	Kind     string
	Severity string
	// ReasonCode, Outcome and SessionID are exact filters on the analytic
	// columns; CauseRef selects the events one earlier event explains.
	ReasonCode string
	Outcome    string
	SessionID  string
	CauseRef   int64
	// JobID selects the events that name one job.
	JobID *uuid.UUID
	Since time.Time
	Until time.Time
	// Limit bounds the page; a store may lower it, never raise it.
	Limit int
	// Cursor continues after (At, ID) in the query's own direction.
	Cursor Cursor
	// Asc flips the order from newest-first to oldest-first, so history can
	// be replayed forward.
	Asc bool
}

// Cursor is a page boundary: the last event of the page before. Its zero
// value starts at the beginning of the stream.
type Cursor struct {
	At time.Time
	ID int64
}

// Page is one query answer, ordered by the query's direction on (At, ID).
// Next is the cursor for the following page, absent when the stream ended.
type Page struct {
	Events []Event
	Next   *Cursor
}

// PruneReport says what a prune pass found. Total is every row that went (or,
// in a dry run, would go). Would is a dry run's sample of them, oldest first,
// bounded by the limit the caller chose; a real pass leaves it empty.
type PruneReport struct {
	Pruned int
	Total  int
	Would  []Event
}

type Store interface {
	// Append writes a batch in one insert. At is the event's own time, not
	// the insert's: a unit may post an event late, and the stream must
	// still read in the order things happened.
	Append(ctx context.Context, ev []Event) error
	// Query returns one page. It never errors on an empty result; the page
	// is simply over.
	Query(ctx context.Context, q Query) (Page, error)
	// Prune drops, or in a dry run only lists, events older than before.
	Prune(ctx context.Context, before time.Time, sample int, dryRun bool) (PruneReport, error)
}

// Lazy stands in for a store that is not connected yet, so the routes can be
// mounted at start and answer 503 until the database is reachable, the same
// arrangement the work queue has.
type Lazy struct {
	mu sync.RWMutex
	s  Store
}

func (l *Lazy) Set(s Store) {
	l.mu.Lock()
	l.s = s
	l.mu.Unlock()
}

// Connect installs the database store behind a Fallback already in place, or
// as the whole store when there is none.
func (l *Lazy) Connect(s Store) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fb, ok := l.s.(*Fallback); ok {
		fb.SetPrimary(s)
		return
	}
	l.s = s
}

func (l *Lazy) get() (Store, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.s == nil {
		return nil, ErrUnavailable
	}
	return l.s, nil
}

func (l *Lazy) Append(ctx context.Context, ev []Event) error {
	s, err := l.get()
	if err != nil {
		return err
	}
	return s.Append(ctx, ev)
}

func (l *Lazy) Query(ctx context.Context, q Query) (Page, error) {
	s, err := l.get()
	if err != nil {
		return Page{}, err
	}
	return s.Query(ctx, q)
}

func (l *Lazy) Prune(ctx context.Context, before time.Time, sample int, dryRun bool) (PruneReport, error) {
	s, err := l.get()
	if err != nil {
		return PruneReport{}, err
	}
	return s.Prune(ctx, before, sample, dryRun)
}
