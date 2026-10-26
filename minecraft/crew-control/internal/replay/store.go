// Package replay records where units stood and serves it back over a time
// range, so an operator can scrub through a past incident. It sits beside the
// event store rather than inside it: a position every couple of seconds per
// unit would outnumber every other event kind and bury the explorer's reads.
package replay

import (
	"context"
	"errors"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

// A page is bounded whatever the caller asks, like the event history's.
const (
	DefaultLimit = 1000
	MaxLimit     = 5000
)

// Sample is one unit's block position at one instant. Only the overworld is
// recorded, as in the heatmap: coordinates in another dimension would land on
// unrelated lab cells.
type Sample struct {
	ID   int64
	Unit string
	At   time.Time
	X    float64
	Y    float64
	Z    float64
}

// Cursor is the last sample of the page before.
type Cursor struct {
	At time.Time
	ID int64
}

// Query reads oldest first, always: replay only runs forward.
type Query struct {
	Unit  string
	Since time.Time
	Until time.Time
	Limit int
	// Cursor continues strictly after (At, ID).
	Cursor Cursor
}

// Page is one answer; Next is absent when the range ended.
type Page struct {
	Samples []Sample
	Next    *Cursor
}

var ErrUnavailable = work.ErrUnavailable

type Store interface {
	Append(ctx context.Context, s []Sample) error
	Query(ctx context.Context, q Query) (Page, error)
	// Prune drops samples older than before and reports how many went.
	Prune(ctx context.Context, before time.Time) (int, error)
}

func (q Query) limit() int {
	if q.Limit <= 0 {
		return DefaultLimit
	}
	return min(q.Limit, MaxLimit)
}

func (q Query) paged() bool { return !q.Cursor.At.IsZero() || q.Cursor.ID != 0 }

var errNoPrimary = errors.New("replay: database store not connected")
