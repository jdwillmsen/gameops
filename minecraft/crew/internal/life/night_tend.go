package life

import (
	"context"
	"errors"
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// A bed farther than this from the unit is not walked to: the walk leaves
	// the loaded chunks and could only fail, and a shelter dug where the unit
	// stands is the better night.
	nightBedReach = 64
	// A bed visit that ended without the unit asleep, as at dusk when the
	// server still refuses sleep, is repeated this soon.
	nightRetryBackoff = 100
	// The visits per night. A server that keeps refusing sleep sends the unit
	// to dig in instead.
	nightMaxBedVisits = 16
	// A visit that failed is not tried again for a minute.
	nightFailBackoff = 1200
	// A night is about 12000 ticks and a bed visit can hold the unit through
	// all of it; a visit longer than this has lost the clock.
	nightVisitLimit = 26000
	// No respawn point set yet is announced with this in every coordinate.
	noSpawn = math.MinInt32
)

type nightKind uint8

const (
	nightBed nightKind = iota
	nightShelter
)

func (k nightKind) String() string {
	if k == nightBed {
		return "bed"
	}
	return "shelter"
}

type nightState struct {
	visit     skill.Skill
	kind      nightKind
	startedAt uint64
	// blockedUntil is the tick before which no visit starts.
	blockedUntil uint64
	// bedVisits counts tonight's bed visits, and bedFailed says one failed, so
	// the bed is not trusted for the rest of the night.
	bedVisits int
	bedFailed bool
	// resume says a fight interrupted the visit, which then replans.
	resume bool
}

// bedFor finds the bed the server remembers as the unit's respawn point: the
// point is where the unit stood when it tapped the bed, so the bed itself is
// looked for around it. A bed that is gone, or too far to walk to, is none.
func (l *Life) bedFor(s belief.Snapshot) (protocol.BlockPos, bool) {
	if !s.SpawnKnown || s.SpawnPoint[1] == noSpawn || !s.PositionKnown {
		return protocol.BlockPos{}, false
	}
	spawn := protocol.BlockPos{s.SpawnPoint[0], s.SpawnPoint[1], s.SpawnPoint[2]}
	if math.Hypot(float64(s.Position[0])-float64(spawn.X()), float64(s.Position[2])-float64(spawn.Z())) > nightBedReach {
		return protocol.BlockPos{}, false
	}
	return skill.FindBed(l.cfg.World, spawn)
}

// wantsNight says whether the unit is spending the night, and starts the visit
// when dusk or night has come and none is under way: to bed when the unit has
// one, else walled in where it stands. It is asked from Tending, which then
// hands the tick to Tend; the dawn ends the visit by itself.
func (l *Life) wantsNight(s belief.Snapshot) bool {
	n := &l.night
	if !l.cfg.NightShelter || l.cfg.World == nil {
		return false
	}
	if n.visit != nil {
		if l.tick-n.startedAt > nightVisitLimit {
			l.stopNight(nil)
			l.endNight(errors.New("visit took too long"))
			return false
		}
		return true
	}
	if s.Env.DayPhase == belief.PhaseDay {
		n.bedVisits, n.bedFailed = 0, false
	}
	if !nightFree(s, s.Env) || l.tick < n.blockedUntil {
		return false
	}
	bed, haveBed := l.bedFor(s)
	haveBed = haveBed && !n.bedFailed && n.bedVisits < nightMaxBedVisits
	var kind nightKind
	switch {
	case ShouldSleep(s, s.Env, haveBed):
		kind = nightBed
	case ShouldShelter(s, s.Env, haveBed, nil):
		kind = nightShelter
	default:
		return false
	}
	v := l.newNight(kind, bed)
	if err := v.Ready(s); err != nil {
		l.logEvent("night_skipped", map[string]any{"kind": kind.String(), "error": err.Error()})
		n.blockedUntil = l.tick + nightFailBackoff
		return false
	}
	n.visit, n.kind, n.startedAt, n.resume = v, kind, l.tick, false
	if kind == nightBed {
		n.bedVisits++
	}
	l.logEvent("night_started", map[string]any{"kind": kind.String(), "phase": s.Env.DayPhase})
	return true
}

// tendNight steps the visit by one tick. Like tendRecover it always sends one
// input.
func (l *Life) tendNight(ctx context.Context, a skill.Actuator) error {
	n := &l.night
	before := a.Ticks()
	if n.resume {
		n.resume = false
		if rs, ok := n.visit.(skill.Resumer); ok {
			rs.Resume()
		}
	}
	st, err := n.visit.Step(ctx, a)
	if st == skill.Running {
		return nil
	}
	if st == skill.Failed && err == nil {
		err = errors.New("night visit failed")
	}
	l.endNight(err)
	if a.Ticks() == before {
		return l.still(a)
	}
	return nil
}

func (l *Life) endNight(err error) {
	n := &l.night
	kind := n.kind
	n.visit = nil
	// A walk home that the night interrupted starts its clock over.
	if l.home.visit != nil {
		l.home.startedAt, l.home.resume = l.tick, true
	}
	if err != nil {
		l.logEvent("night_failed", map[string]any{"kind": kind.String(), "error": err.Error()})
		if kind == nightBed {
			// A bed that cannot be used tonight sends the unit to dig in soon,
			// not a minute from now.
			n.bedFailed, n.blockedUntil = true, l.tick+nightRetryBackoff
			return
		}
		n.blockedUntil = l.tick + nightFailBackoff
		return
	}
	l.logEvent("night_done", map[string]any{"kind": kind.String()})
	n.blockedUntil = l.tick + nightRetryBackoff
}

// stopNight takes the unit off the visit for a fight: out of bed, and out of
// the shelter's room. It reports whether that sent the tick's one input, in
// which case the fight begins on the next tick. A nil actuator drops the
// visit without a word to the server.
func (l *Life) stopNight(a skill.Actuator) (sent bool) {
	n := &l.night
	if n.visit == nil {
		return false
	}
	n.resume = true
	if a == nil {
		return false
	}
	st, ok := n.visit.(skill.Stopper)
	if !ok {
		return false
	}
	before := a.Ticks()
	st.Stop(a)
	return a.Ticks() != before
}

func (l *Life) newNight(kind nightKind, bed protocol.BlockPos) skill.Skill {
	if l.makeNight != nil {
		return l.makeNight(kind, bed)
	}
	var opts []skill.Option
	if l.log != nil {
		opts = append(opts, skill.WithLogger(l.log))
	}
	if kind == nightBed {
		return skill.NewUseBed(l.cfg.World, l.self, bed, opts...)
	}
	return skill.NewShelter(l.cfg.World, l.self, 0, opts...)
}
