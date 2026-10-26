package alerts

import (
	"context"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// Store keeps the alert list across restarts. It holds a snapshot of the
// engine's list, not a history: Save replaces whatever was stored.
type Store interface {
	Load(ctx context.Context) ([]Alert, error)
	Save(ctx context.Context, alerts []Alert) error
}

const (
	// persistDelay lets a burst of changes (a dozen units dying in one poll)
	// become one write.
	persistDelay   = time.Second
	persistTimeout = 5 * time.Second
)

// Restore merges alerts saved by an earlier run into the engine. An alert the
// engine already holds keeps its own state, because the poll that made it saw
// the world more recently than the snapshot did; only the operator's
// acknowledgement and mute carry over, which nothing else can recreate.
func (e *Engine) Restore(saved []Alert, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range saved {
		a := e.alerts[s.ID]
		if a == nil {
			if s.State == StateResolved && s.ResolvedAt != nil && now.Sub(*s.ResolvedAt) > resolvedKeep {
				continue
			}
			c := s
			a = &c
			e.alerts[s.ID] = a
			e.names[s.Unit] = s.Name
			// The readings these rules count are not saved, so seed them from
			// the alert: otherwise the first poll would find nothing wrong and
			// resolve an alert that is still true.
			if s.State == StateFiring {
				switch s.Rule {
				case RuleStuck:
					e.stuckSince[s.Unit] = s.Since
				case RuleFailures:
					e.fails[s.Unit] = e.cfg.Failures
				}
			}
		} else if a.State == StateFiring && s.State == StateFiring && a.Since.Equal(s.Since) {
			a.Acked = a.Acked || s.Acked
		}
		if s.MutedUntil != nil && s.MutedUntil.After(now) {
			until := *s.MutedUntil
			e.mutes[s.ID] = until
			a.MutedUntil = &until
		}
	}
	e.version++
	e.notifyLocked()
}

// Persist restores the saved alerts, then writes the list back whenever it
// changes, until ctx ends. A store that is down is logged once per outage and
// retried on the next change; alerts keep working from memory regardless.
func Persist(ctx context.Context, e *Engine, s Store, log *logging.Logger) {
	ch, cancel := e.Subscribe()
	defer cancel()

	lctx, lcancel := context.WithTimeout(ctx, persistTimeout)
	saved, err := s.Load(lctx)
	lcancel()
	if err != nil {
		log.Error("alert_restore_failed", logging.Fields{"error": err.Error(), "effect": "acknowledgements and mutes start empty"})
	} else {
		e.Restore(saved, time.Now())
		log.Info("alerts_restored", logging.Fields{"count": len(saved)})
	}

	warned := false
	save := func(ctx context.Context) {
		sctx, scancel := context.WithTimeout(ctx, persistTimeout)
		defer scancel()
		if err := s.Save(sctx, e.List()); err != nil {
			if !warned {
				warned = true
				log.Error("alert_save_failed", logging.Fields{"error": err.Error(), "effect": "alert state is held in memory only"})
			}
			return
		}
		warned = false
	}
	for {
		select {
		case <-ctx.Done():
			save(context.WithoutCancel(ctx))
			return
		case <-ch:
		}
		select {
		case <-ctx.Done():
			save(context.WithoutCancel(ctx))
			return
		case <-time.After(persistDelay):
		}
		save(ctx)
	}
}
