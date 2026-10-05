// Package worlddamage keeps the agent's view of whether the world has lost
// chunks it cannot get back.
//
// The record itself is the map service's, on its own disk: a loss stays lost
// until an operator acknowledges the count that found it, because the server
// regenerates a lost chunk as empty terrain when a player walks near, which
// would otherwise make the damage clear itself. The agent holds no copy
// that could outlive or contradict that, so a restart costs it one read.
package worlddamage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// ErrNothingToClear is Clear with no damage recorded.
var ErrNothingToClear = errors.New("worlddamage: no world damage is recorded")

// Source is the map service as the watch uses it.
type Source interface {
	World(ctx context.Context) (mapclient.World, error)
	AcknowledgeWorld(ctx context.Context, checkedAt time.Time) error
}

// Watch holds the last damage the map reported, for every join to read
// without a network call.
type Watch struct {
	src Source
	log *logging.Logger

	mu    sync.Mutex
	known bool
	world mapclient.World
	// ackedAt is the count an operator last accepted. A read that began
	// before the acknowledgement returns that count as it stood, loss and
	// all, and would otherwise switch the notice back on.
	ackedAt time.Time
	// failing is whether the previous poll failed, so an outage is logged
	// when it starts and ends rather than once per poll.
	failing bool
}

func New(src Source, log *logging.Logger) *Watch {
	return &Watch{src: src, log: log}
}

// Run polls until ctx ends, starting immediately so a restarted agent knows
// the condition before the first player joins.
func (w *Watch) Run(ctx context.Context, interval time.Duration) {
	w.Poll(ctx)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.Poll(ctx)
		}
	}
}

// Poll reads the map once. On failure the last known condition is kept: a map
// that cannot be reached is not evidence that the world was repaired.
func (w *Watch) Poll(ctx context.Context) error {
	got, err := w.src.World(ctx)

	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		if !w.failing {
			w.log.Warn("world_damage_poll_failed", logging.Fields{"error": err.Error(), "holding_damage": w.active()})
		}
		w.failing = true
		return err
	}
	if w.failing {
		w.log.Info("world_damage_poll_recovered", logging.Fields{})
	}
	w.failing = false

	if got.LostTotal > 0 && got.CheckedAt.Equal(w.ackedAt) {
		return nil
	}
	// Two reads can be in flight at once, and the one that started first can
	// land last. A count older than the one already held is that late answer,
	// not news: taking it would bring back a loss that has since been
	// replaced or accepted.
	if w.known && got.CheckedAt.Before(w.world.CheckedAt) {
		return nil
	}
	before := w.world
	w.known, w.world = true, got
	switch {
	case got.LostTotal > 0 && got.LostTotal != before.LostTotal:
		w.log.Warn("world_damage_recorded", logging.Fields{"lost": got.LostTotal, "checked_at": got.CheckedAt})
	case got.LostTotal == 0 && before.LostTotal > 0:
		w.log.Info("world_damage_cleared", logging.Fields{"was_lost": before.LostTotal})
	}
	return nil
}

func (w *Watch) active() bool { return w.known && w.world.LostTotal > 0 }

// Current is the damage on record. Read from the map when nothing has been
// read yet, so a join straight after a restart is not told the world is fine
// because the first poll had not finished.
func (w *Watch) Current(ctx context.Context) (mapclient.World, bool) {
	w.mu.Lock()
	known := w.known
	w.mu.Unlock()
	if !known {
		w.Poll(ctx)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.world, w.active()
}

// Clear accepts the world as the held count found it, which stops the notice.
//
// It names the count players were being told about, not whatever the map
// holds now: a newer one may carry losses the operator has not seen, and the
// map refuses to accept it in this one's place. That refusal comes back as
// mapclient.ErrCountReplaced, after the held count has been brought up to
// date so that reading it and clearing again names the right one.
func (w *Watch) Clear(ctx context.Context) (mapclient.World, error) {
	held, ok := w.Current(ctx)
	if !ok {
		return mapclient.World{}, ErrNothingToClear
	}
	switch err := w.src.AcknowledgeWorld(ctx, held.CheckedAt); {
	case errors.Is(err, mapclient.ErrCountReplaced):
		w.Poll(ctx)
		return held, err
	case err != nil:
		return held, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.ackedAt = held.CheckedAt
	// A count that finished while the acknowledgement was in flight is a
	// loss nobody has accepted, and a poll may already have cached it. Only
	// the count that was acknowledged is cleared.
	if w.world.CheckedAt.Equal(held.CheckedAt) {
		w.world = mapclient.World{Checked: true, CheckedAt: held.CheckedAt}
	}
	w.log.Warn("world_damage_acknowledged", logging.Fields{"lost": held.LostTotal, "checked_at": held.CheckedAt})
	return held, nil
}
