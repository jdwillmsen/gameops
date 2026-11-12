package life

import (
	"context"
	"errors"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// A unit farther than this from home, on the ground plane, is sent back; a
	// unit already within it counts as home, since the work plan starts from
	// wherever its base is.
	homeArrive = 4.0
	// The walk is done this close to the home cell.
	homeWithin = 2.0
	// A trip that has not arrived by now is given up: five minutes covers a
	// few hundred blocks with time for chunks to load.
	homeLimit = 6000
	// A failed trip is retried after this, as chunks keep arriving.
	homeRetryBackoff = 200
	// Trips that fail in a row before the unit stays where it is until its
	// next death or join.
	homeMaxFails = 4
)

type homeState struct {
	// pending is set from a join or a death until the unit has been home or
	// has given up on getting there. Work that takes the unit away from home on
	// purpose never sets it.
	pending      bool
	visit        skill.Skill
	startedAt    uint64
	blockedUntil uint64
	fails        int
	resume       bool
}

// newHomeState is the state of a fresh life: the unit has just come back at
// the world spawn or a join, wherever that is, and walks home if it has one.
func newHomeState(cfg Config) homeState { return homeState{pending: cfg.Home != nil} }

// wantsHome says whether the unit is walking home, and starts the trip when it
// has come back to a life away from home. It is asked from Tending, which then
// hands the tick to Tend.
func (l *Life) wantsHome(s belief.Snapshot) bool {
	h := &l.home
	if l.cfg.Home == nil {
		return false
	}
	if h.visit != nil {
		if l.tick-h.startedAt > homeLimit {
			h.visit, h.pending = nil, false
			l.logEvent("home_failed", map[string]any{"error": "ran out of time"})
			return false
		}
		return true
	}
	if h.pending && s.Spawned && !s.Dead && (s.Dimension != 0 || s.Creative) {
		// Home is in the overworld, and a creative unit is not sent anywhere:
		// the trip is not owed later, when the unit might be mid-task.
		h.pending = false
	}
	if !h.pending || !s.Spawned || s.Dead || !s.PositionKnown || l.tick < h.blockedUntil {
		return false
	}
	if skill.AtHome(feetOf(s), *l.cfg.Home, homeArrive) {
		h.pending = false
		return false
	}
	v := l.newHome()
	if err := v.Ready(s); err != nil {
		l.logEvent("home_skipped", map[string]any{"error": err.Error()})
		h.blockedUntil = l.tick + homeRetryBackoff
		return false
	}
	h.visit, h.startedAt, h.resume = v, l.tick, false
	l.logEvent("home_started", nil)
	return true
}

// feetOf is where the unit's feet are, from the eye position belief keeps.
func feetOf(s belief.Snapshot) [3]float32 {
	return [3]float32{s.Position[0], s.Position[1] - eyeToFeet, s.Position[2]}
}

// tendHome steps the trip by one tick. Like tendRecover it always sends one
// input.
func (l *Life) tendHome(ctx context.Context, a skill.Actuator) error {
	h := &l.home
	before := a.Ticks()
	if h.resume {
		h.resume = false
		if rs, ok := h.visit.(skill.Resumer); ok {
			rs.Resume()
		}
	}
	st, err := h.visit.Step(ctx, a)
	if st == skill.Running {
		return nil
	}
	h.visit = nil
	if st == skill.Failed && err == nil {
		err = errors.New("walk home failed")
	}
	if err != nil {
		h.fails++
		l.logEvent("home_failed", map[string]any{"error": err.Error()})
		if h.fails >= homeMaxFails {
			h.pending = false
		} else {
			h.blockedUntil = l.tick + homeRetryBackoff
		}
	} else {
		h.pending, h.fails = false, 0
		l.logEvent("home_done", nil)
	}
	if a.Ticks() == before {
		return l.still(a)
	}
	return nil
}

// stopHome pauses the trip for a fight; the walk plans afresh afterwards.
func (l *Life) stopHome() {
	if l.home.visit != nil {
		l.home.resume = true
	}
}

func (l *Life) newHome() skill.Skill {
	if l.makeHome != nil {
		return l.makeHome()
	}
	return skill.NewGoHome(l.world, l.self, *l.cfg.Home, homeWithin)
}
