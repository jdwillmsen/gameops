package life

import (
	"context"
	"errors"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// A dropped item despawns after 6000 ticks, so a unit that has not set out by
// then has nothing to walk back for.
const recoverWindow = 6000

// lastDeath is where the unit last died. It is written by the read loop when
// the death is announced and read by the stepping goroutine, under Life.mu;
// it is kept in memory only, so a restart forgets it.
type lastDeath struct {
	pos mgl32.Vec3
	dim int32
	// lost is how many items the unit held, or wore, at its last tick alive.
	lost  int
	known bool
	// fresh is true until the first tick of the next life stamps at, since the
	// read loop that sees the death has no tick clock.
	fresh bool
	at    uint64
	// attempted makes a death worth one trip, however it ends.
	attempted bool
}

type recoverState struct {
	visit skill.Skill
	// resume says a fight interrupted the walk, which then plans afresh.
	resume bool
}

// noteDeathLocked remembers the death. Called with mu held, where the feet are
// the last pose the stepping goroutine reported.
func (l *Life) noteDeathLocked() {
	if !l.feetKnown {
		l.death = lastDeath{}
		return
	}
	l.death = lastDeath{pos: l.feet, dim: l.dim, lost: l.carried, known: true, fresh: true}
}

// noteLife keeps what a death will need to know of the living unit, and stamps
// a death the unit has just come back from.
func (l *Life) noteLife(s belief.Snapshot) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.death.fresh {
		l.death.fresh, l.death.at = false, l.tick
		// A walk begun for the earlier death means nothing for this one.
		l.rec.visit = nil
	}
	if s.Spawned && !s.Dead {
		l.dim, l.carried = s.Dimension, held(s)
	}
}

// held counts every item the unit carries or wears.
func held(s belief.Snapshot) int {
	n := len(s.Armour)
	for _, it := range s.Inventory {
		n += it.Count
	}
	return n
}

// pendingDeath returns the death waiting for its trip, if any.
func (l *Life) pendingDeath() (lastDeath, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.death
	return d, d.known && !d.fresh && !d.attempted
}

func (l *Life) markAttempted() {
	l.mu.Lock()
	l.death.attempted = true
	l.mu.Unlock()
}

// wantsRecovery says whether the unit is on its way back to a death, and
// starts the trip when a death is waiting for one and the way is fit. It is
// asked from Tending, which then hands the tick to Tend. The trip is made
// ahead of a queued task: the drops last five minutes and the task that was
// running died with the unit.
func (l *Life) wantsRecovery(s belief.Snapshot) bool {
	if l.rec.visit != nil {
		return true
	}
	if !l.cfg.Recover || !s.Spawned || s.Dead || !s.PositionKnown {
		return false
	}
	d, ok := l.pendingDeath()
	if !ok {
		return false
	}
	skip := func(reason string) bool {
		l.markAttempted()
		l.logEvent("recover_skipped", map[string]any{"reason": reason})
		return false
	}
	switch {
	case d.lost == 0:
		return skip("nothing_lost")
	case l.tick-d.at > recoverWindow:
		return skip("too_late")
	}
	v := l.newRecover(d)
	if err := v.Ready(s); err != nil {
		return skip(recoverReason(err))
	}
	l.markAttempted()
	l.rec.visit, l.rec.resume = v, false
	l.logEvent("recover_started", map[string]any{"lost": d.lost})
	return true
}

func recoverReason(err error) string {
	switch {
	case errors.Is(err, skill.ErrRecoverTooFar):
		return "too_far"
	case errors.Is(err, skill.ErrRecoverNotLoaded):
		return "chunk_not_loaded"
	case errors.Is(err, skill.ErrRecoverHazard):
		return "hazard"
	case errors.Is(err, skill.ErrRecoverDimension):
		return "other_dimension"
	}
	return "not_ready"
}

// tendRecover steps the trip by one tick. Like tendKit it always sends one
// input.
func (l *Life) tendRecover(ctx context.Context, a skill.Actuator) error {
	r := &l.rec
	before := a.Ticks()
	if r.resume {
		r.resume = false
		if rs, ok := r.visit.(skill.Resumer); ok {
			rs.Resume()
		}
	}
	st, err := r.visit.Step(ctx, a)
	if st == skill.Running {
		return nil
	}
	r.visit = nil
	if st == skill.Failed && err == nil {
		err = errors.New("recovery failed")
	}
	if err != nil {
		l.logEvent("recover_failed", map[string]any{"error": err.Error()})
	} else {
		l.logEvent("recover_done", nil)
	}
	if a.Ticks() == before {
		return l.still(a)
	}
	return nil
}

// stopRecover pauses the trip for a fight. The walk keeps its clock, so a long
// fight spends the time the drops have.
func (l *Life) stopRecover() {
	if l.rec.visit != nil {
		l.rec.resume = true
	}
}

func (l *Life) newRecover(d lastDeath) skill.Skill {
	if l.makeRecover != nil {
		return l.makeRecover(d)
	}
	return skill.NewRecover(l.world, l.self, l.ents, d.pos, d.dim, d.lost)
}
