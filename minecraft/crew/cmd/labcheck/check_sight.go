package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	// creeperClearance is how near another crew unit may stand to the creeper's
	// summon point: slowness stops its walking but not its fuse, and a charged
	// blast reaches well past the usual three blocks.
	creeperClearance = 8

	staleSighting = "already seen this session, restart the unit or use a fresh mob type"
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
	// A unit logs a creeper in one flag state once per session, so a charged
	// one it has already logged can never show up fresh.
	if f, ok := chargedCreeper(before); ok {
		return inconclusive(check, unit+" has a charged creeper sighting: "+staleSighting, ev{"unit": unit, "flags": f})
	}
	w, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position first: "+err.Error(), ev{"unit": unit})
	}
	at := ahead(w, creeperAhead)
	if near, err := e.crowded(at, unit); err != nil {
		return inconclusive(check, "cannot tell whether another unit stands near the creeper: "+err.Error(), ev{"unit": unit})
	} else if near != "" {
		return inconclusive(check, fmt.Sprintf("%s is within %d blocks of where the charged creeper would stand", near, creeperClearance), ev{"unit": unit, "near": near})
	}
	// The creeper is born charged by the spawn event proven in the mob-buffs
	// research, and frozen so it can never walk up and blow.
	if err := e.summon(ctx, "creeper", "minecraft:charged_creeper", at); err != nil {
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
			if f.Charged && f.Seq > known {
				return pass(check, unit+" saw the creeper's Charged flag", ev{
					"unit": unit, "flags": f, "fresh": true,
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
	// A unit logs a mob type's health once per session, so a zombie it has
	// already logged can never show up fresh.
	for _, h := range before.Health {
		if h.Entity == "zombie" {
			return inconclusive(check, unit+" has a zombie health sighting: "+staleSighting, ev{"unit": unit, "health_seen": h})
		}
	}
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
			if h.Entity == "zombie" && h.Seq > known {
				return pass(check, fmt.Sprintf("%s saw a zombie's health (%.0f)", unit, h.Health), ev{
					"unit": unit, "health_seen": h, "fresh": true,
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

func chargedCreeper(s *life.Sightings) (life.FlagSeen, bool) {
	for _, f := range creepers(s) {
		if f.Charged {
			return f, true
		}
	}
	return life.FlagSeen{}, false
}

// crowded names a crew unit other than the subject standing within
// creeperClearance of the spot, or "" when there is none. A unit that is not
// in the world is not near anything.
func (e *Env) crowded(at spot, subject string) (string, error) {
	where := pose{X: float64(at.X), Y: float64(at.Y), Z: float64(at.Z)}
	seen := map[string]bool{subject: true}
	for _, name := range slices.Concat(e.Opts.Units, e.Opts.CrewNames) {
		if seen[name] {
			continue
		}
		seen[name] = true
		p, err := e.playerPose(name)
		var none errNoTargets
		if errors.As(err, &none) {
			continue
		}
		if err != nil {
			return "", err
		}
		if p.dist(where) <= creeperClearance {
			return name, nil
		}
	}
	return "", nil
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
