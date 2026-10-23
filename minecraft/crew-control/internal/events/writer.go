package events

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	// writerQueue is one burst's worth of headroom: a unit flapping or a
	// task churning should queue, not drop, and a database that is down for
	// longer than this loses events rather than the console's cadence.
	writerQueue = 1024
	// The writer flushes when this many events are ready, or this often: one
	// tick's events go in one insert, and a quiet stream still lands within a
	// second.
	writerBatch = 64
	writerEvery = time.Second
	// shutdownFlush bounds the last write, made after the run context ended.
	shutdownFlush = 2 * time.Second
)

type queued struct {
	e Event
	h Hint
}

// Writer turns the poller's event stream into store batches without ever
// blocking the poll loop: the poller's cadence is the console's liveness,
// and a slow insert must not slow polling. Events are dropped, not queued
// forever, because they are history, not work: a gap during an outage is
// honest, a backlog that delays the live feed is not.
type Writer struct {
	store *Lazy
	log   *logging.Logger
	ch    chan queued
	col   *Collapser
	every time.Duration
	max   int

	// warnedDrop and warnedWrite are once-per-outage flags, so a database
	// that is down logs its state once, not once per dropped event.
	warnedDrop  atomic.Bool
	warnedWrite atomic.Bool
}

func NewWriter(store *Lazy, log *logging.Logger) *Writer {
	return &Writer{store: store, log: log, ch: make(chan queued, writerQueue), col: NewCollapser(), every: writerEvery, max: writerBatch}
}

// Add offers one event to the writer. It never blocks; a full queue means
// the store is not keeping up, and the event is dropped.
func (w *Writer) Add(e Event, h Hint) {
	// The table refuses an unknown severity, and one bad row would fail the
	// whole batch it travels in.
	if !validSeverity(e.Severity) {
		e.Severity = SevInfo
	}
	select {
	case w.ch <- queued{e, h}:
		w.warnedDrop.Store(false)
	default:
		if w.warnedDrop.CompareAndSwap(false, true) {
			w.log.Warn("event_writer_dropping", logging.Fields{"queue": cap(w.ch)})
		}
	}
}

// Run collapses and flushes queued events until ctx ends. Each flush is one
// Append of at most w.max events: a burst is coalesced, and a steady stream
// costs one insert a second.
func (w *Writer) Run(ctx context.Context) {
	tick := time.NewTicker(w.every)
	defer tick.Stop()
	batch := make([]Event, 0, w.max)
	flush := func(ctx context.Context) {
		w.append(ctx, batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// Whatever is still queued or open is the most recent history;
			// a restart should not be the one gap in it.
			for drained := false; !drained; {
				select {
				case q := <-w.ch:
					batch = append(batch, w.col.Push(q.e, q.h)...)
				default:
					drained = true
				}
			}
			batch = append(batch, w.col.Flush()...)
			last, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownFlush)
			flush(last)
			cancel()
			return
		case q := <-w.ch:
			batch = append(batch, w.col.Push(q.e, q.h)...)
			if len(batch) >= w.max {
				flush(ctx)
			}
		case <-tick.C:
			batch = append(batch, w.col.Expire(time.Now())...)
			flush(ctx)
		}
	}
}

func (w *Writer) append(ctx context.Context, batch []Event) {
	if len(batch) == 0 {
		return
	}
	// Bounded, like every store call: a writer stuck on a hung database
	// would otherwise stop draining and start dropping.
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := w.store.Append(ctx, batch); err != nil {
		if w.warnedWrite.CompareAndSwap(false, true) {
			w.log.Warn("event_append_failed", logging.Fields{"batch": len(batch), "error": err.Error()})
		}
		return
	}
	w.warnedWrite.Store(false)
}
