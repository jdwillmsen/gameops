package main

import (
	"context"
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
)

const (
	// creeperAhead and zombieAhead put the mob in front of the warrior and
	// well inside the distance entities are announced at. The creeper stands
	// farther: it is frozen, but a charged blast is not to be near the unit.
	creeperAhead = 10
	zombieAhead  = 6
	// A mob is frozen for as long as a check can run.
	freezeSeconds = 300
)

// seenState reads a unit's sightings, or says why the check cannot.
func (e *Env) seenState(ctx context.Context, check, unit string) (*life.Sightings, *Result) {
	st, err := e.Units.State(ctx, unit)
	if err != nil {
		r := e.cut(check, "reading "+unit+"'s state", err)
		return nil, &r
	}
	if st.Seen == nil {
		r := inconclusive(check, unit+" reports no sightings: its build predates them", ev{"unit": unit})
		return nil, &r
	}
	return st.Seen, nil
}

// checkCreeper summons a charged creeper in front of the warrior and reads
// back what the unit logged about its flags.
func checkCreeper(ctx context.Context, e *Env) Result {
	const check = "creeper"
	unit := e.Opts.Units[0]
	if err := e.guard(ctx); err != nil {
		return e.cut(check, "starting", err)
	}
	before, bad := e.seenState(ctx, check, unit)
	if bad != nil {
		return *bad
	}
	known := latest(before)
	w, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position first: "+err.Error(), ev{"unit": unit})
	}
	// The creeper is born charged by the spawn event proven in the mob-buffs
	// research, and frozen so it can never walk up and blow.
	if err := e.summon(ctx, "creeper", "minecraft:charged_creeper", ahead(w, creeperAhead)); err != nil {
		return e.cut(check, "summoning the charged creeper", err)
	}
	e.effect("creeper", "slowness", freezeSeconds, 255)

	start := e.Clock.Now()
	var seen []life.FlagSeen
	for {
		if err := e.pause(ctx, e.Opts.Poll); err != nil {
			return e.cut(check, "waiting for the unit to log the creeper", err)
		}
		s, bad := e.seenState(ctx, check, unit)
		if bad != nil {
			return *bad
		}
		seen = creepers(s)
		for _, f := range seen {
			if f.Charged {
				return pass(check, unit+" saw the creeper's Charged flag", ev{
					"unit": unit, "flags": f, "fresh": f.Seq > known,
				})
			}
		}
		if e.Clock.Now().Sub(start) >= e.Opts.Window {
			break
		}
	}
	evidence := ev{"unit": unit, "creeper_sightings": seen, "watched_seconds": e.Clock.Now().Sub(start).Seconds()}
	for _, f := range seen {
		if f.Seq > known {
			return fail(check, unit+" saw the charged creeper but its Charged flag never showed", evidence)
		}
	}
	return inconclusive(check, unit+" logged no new creeper sighting; it may not have had the creeper in range", evidence)
}

// checkZombie summons one zombie in front of the warrior and reads back
// whether the unit saw its health.
func checkZombie(ctx context.Context, e *Env) Result {
	const check = "zombie"
	unit := e.Opts.Units[0]
	if err := e.guard(ctx); err != nil {
		return e.cut(check, "starting", err)
	}
	before, bad := e.seenState(ctx, check, unit)
	if bad != nil {
		return *bad
	}
	known := latest(before)
	w, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position first: "+err.Error(), ev{"unit": unit})
	}
	if err := e.summon(ctx, "zombie", "", ahead(w, zombieAhead)); err != nil {
		return e.cut(check, "summoning the zombie", err)
	}
	// Weakness keeps the lone zombie from hurting the unit that fights it.
	e.effect("zombie", "weakness", freezeSeconds, 255)

	start := e.Clock.Now()
	for {
		if err := e.pause(ctx, e.Opts.Poll); err != nil {
			return e.cut(check, "waiting for the unit to log the zombie", err)
		}
		s, bad := e.seenState(ctx, check, unit)
		if bad != nil {
			return *bad
		}
		for _, h := range s.Health {
			if h.Entity == "zombie" {
				return pass(check, fmt.Sprintf("%s saw a zombie's health (%.0f)", unit, h.Health), ev{
					"unit": unit, "health_seen": h, "fresh": h.Seq > known,
				})
			}
		}
		if e.Clock.Now().Sub(start) >= e.Opts.Window {
			return fail(check, unit+" never logged a zombie's health: its health packets do not reach the unit, or it did not see the zombie",
				ev{"unit": unit, "health_seen": s.Health, "watched_seconds": e.Clock.Now().Sub(start).Seconds()})
		}
	}
}

// latest is the highest sequence number in a unit's sightings.
func latest(s *life.Sightings) uint64 {
	var seq uint64
	for _, h := range s.Health {
		seq = max(seq, h.Seq)
	}
	for _, f := range s.Flags {
		seq = max(seq, f.Seq)
	}
	return seq
}

func creepers(s *life.Sightings) []life.FlagSeen {
	var out []life.FlagSeen
	for _, f := range s.Flags {
		if f.Type == "creeper" {
			out = append(out, f)
		}
	}
	return out
}
