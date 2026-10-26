package events

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// fallbackCap bounds the memory held while the database is away. It is a
// little under an hour of a busy fleet after idle polls are rolled up; an
// outage longer than that loses its oldest events, which the log says.
const fallbackCap = 20000

// Fallback keeps the console's history working when the database is not: a
// store that is unset, unreachable or missing its table is replaced by a
// bounded in-memory one, loudly, and its contents are written back once the
// database answers again.
//
// Reads during an outage see only what memory holds, so history looks shorter
// than it is rather than the console going dark.
type Fallback struct {
	log *logging.Logger
	mem *Memory

	// mu orders appends so replay cannot interleave with a live batch.
	mu      sync.Mutex
	primary atomic.Pointer[Store]

	degraded atomic.Bool
	diverted atomic.Int64
	replayed atomic.Int64
	lost     atomic.Int64
}

var _ Store = (*Fallback)(nil)

func NewFallback(log *logging.Logger) *Fallback {
	f := &Fallback{log: log, mem: NewMemory()}
	f.degraded.Store(true)
	log.Error("event_store_degraded", logging.Fields{
		"error":  ErrNoPrimary.Error(),
		"effect": "events are held in memory only and are lost on restart until the database is reached",
	})
	return f
}

// SetPrimary installs the database store. Until then, and whenever it fails,
// the memory store serves.
func (f *Fallback) SetPrimary(s Store) { f.primary.Store(&s) }

// Degraded reports whether the last write went to memory instead of the
// database. It starts true: before the database is first reached, memory is
// what serves.
func (f *Fallback) Degraded() bool { return f.degraded.Load() }

// Diverted counts events written to memory instead of the database.
func (f *Fallback) Diverted() int64 { return f.diverted.Load() }

// Replayed counts diverted events written back to the database afterwards.
func (f *Fallback) Replayed() int64 { return f.replayed.Load() }

// Lost counts diverted events that fell off the bounded memory before the
// database came back.
func (f *Fallback) Lost() int64 { return f.lost.Load() }

func (f *Fallback) store() Store {
	if p := f.primary.Load(); p != nil {
		return *p
	}
	return nil
}

// ErrNoPrimary is why a write went to memory before the database was ever
// reached.
var ErrNoPrimary = errors.New("events: database store not connected")

func (f *Fallback) Append(ctx context.Context, ev []Event) error {
	if len(ev) == 0 {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.store()
	err := ErrNoPrimary
	if p != nil {
		if err = f.replay(ctx, p); err == nil {
			err = p.Append(ctx, ev)
		}
	}
	if err == nil {
		if f.degraded.Swap(false) {
			f.log.Warn("event_store_recovered", logging.Fields{"replayed": f.replayed.Load(), "lost": f.lost.Load()})
		}
		return nil
	}
	// Once per outage: the metric carries the running count.
	if !f.degraded.Swap(true) {
		f.log.Error("event_store_degraded", logging.Fields{
			"error":  err.Error(),
			"effect": "events are held in memory only and are lost on restart",
			"events": len(ev),
		})
	}
	f.divert(ctx, ev)
	return nil
}

func (f *Fallback) divert(ctx context.Context, ev []Event) {
	_ = f.mem.Append(ctx, ev)
	f.diverted.Add(int64(len(ev)))
	f.lost.Add(int64(f.mem.trim(fallbackCap)))
}

// replay writes what memory holds back to the database, oldest first, and
// empties it. A failure leaves memory as it was.
func (f *Fallback) replay(ctx context.Context, p Store) error {
	held := f.mem.drain()
	if len(held) == 0 {
		return nil
	}
	for i := range held {
		// The database assigns its own ids; a memory id would point at
		// nothing there.
		held[i].ID, held[i].CauseRef = 0, nil
	}
	if err := p.Append(ctx, held); err != nil {
		if rejected(err) {
			// A batch the database will never take must not wedge every
			// later one behind it.
			f.lost.Add(int64(len(held)))
			f.log.Error("event_replay_rejected", logging.Fields{"error": err.Error(), "events": len(held)})
			return nil
		}
		_ = f.mem.Append(ctx, held)
		return err
	}
	f.replayed.Add(int64(len(held)))
	return nil
}

func (f *Fallback) Query(ctx context.Context, q Query) (Page, error) {
	if p := f.store(); p != nil {
		page, err := p.Query(ctx, q)
		if err == nil || errors.Is(err, ErrInvalid) {
			return page, err
		}
		f.log.Warn("event_query_fallback", logging.Fields{"error": err.Error()})
	}
	return f.mem.Query(ctx, q)
}

// Prune applies the same cutoff to both stores, so the memory held during an
// outage cannot outlive the retention window either. The database's report is
// the one returned.
func (f *Fallback) Prune(ctx context.Context, before time.Time, sample int, dryRun bool) (PruneReport, error) {
	memRep, memErr := f.mem.Prune(ctx, before, sample, dryRun)
	p := f.store()
	if p == nil {
		return memRep, memErr
	}
	return p.Prune(ctx, before, sample, dryRun)
}

// rejected reports a data error (class 22 or 23): the database is up and
// refuses the rows themselves, which retrying cannot fix.
func rejected(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (strings.HasPrefix(pg.Code, "22") || strings.HasPrefix(pg.Code, "23"))
}

// trim drops the oldest events beyond max and reports how many went.
func (m *Memory) trim(max int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	over := len(m.events) - max
	if over <= 0 {
		return 0
	}
	order(m.events, true)
	clear(m.events[:over])
	m.events = append([]Event(nil), m.events[over:]...)
	return over
}

// drain empties the store and returns what it held, oldest first.
func (m *Memory) drain() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.events
	m.events = nil
	order(out, true)
	return out
}
