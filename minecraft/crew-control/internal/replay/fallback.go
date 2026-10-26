package replay

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// fallbackCap is about 40 minutes of twenty always-moving units; an outage
// longer than that loses its oldest positions.
const fallbackCap = 30000

// Fallback keeps replay working when the database is not: a store that is
// unset, unreachable or missing its table is replaced by a bounded memory one,
// loudly. Positions are not written back afterwards, since they are
// diagnostic history rather than work, and a gap during an outage is honest.
type Fallback struct {
	log     *logging.Logger
	mem     *Memory
	primary atomic.Pointer[Store]

	writeDown atomic.Bool
	readDown  atomic.Bool
}

var _ Store = (*Fallback)(nil)

func NewFallback(log *logging.Logger) *Fallback {
	return &Fallback{log: log, mem: NewMemory()}
}

// SetPrimary installs the database store.
func (f *Fallback) SetPrimary(s Store) { f.primary.Store(&s) }

// Degraded reports whether the last write went to memory.
func (f *Fallback) Degraded() bool { return f.writeDown.Load() || f.primary.Load() == nil }

func (f *Fallback) store() Store {
	if p := f.primary.Load(); p != nil {
		return *p
	}
	return nil
}

func (f *Fallback) Append(ctx context.Context, s []Sample) error {
	if len(s) == 0 {
		return nil
	}
	err := errNoPrimary
	if p := f.store(); p != nil {
		if err = p.Append(ctx, s); err == nil {
			if f.writeDown.Swap(false) {
				f.log.Warn("replay_store_recovered", nil)
			}
			return nil
		}
	}
	// Once per outage, so a missing table does not log every tick.
	if !f.writeDown.Swap(true) {
		f.log.Error("replay_store_degraded", logging.Fields{
			"error":  err.Error(),
			"effect": "positions are held in memory only and are lost on restart",
		})
	}
	_ = f.mem.Append(ctx, s)
	f.mem.trim(fallbackCap)
	return nil
}

func (f *Fallback) Query(ctx context.Context, q Query) (Page, error) {
	if p := f.store(); p != nil {
		page, err := p.Query(ctx, q)
		if err == nil {
			f.readDown.Store(false)
			return page, nil
		}
		if !f.readDown.Swap(true) {
			f.log.Warn("replay_query_fallback", logging.Fields{"error": err.Error()})
		}
	}
	return f.mem.Query(ctx, q)
}

// Prune applies one cutoff to both stores so the memory held during an outage
// cannot outlive retention either.
func (f *Fallback) Prune(ctx context.Context, before time.Time) (int, error) {
	n, _ := f.mem.Prune(ctx, before)
	if p := f.store(); p != nil {
		return p.Prune(ctx, before)
	}
	return n, nil
}
