package main

import (
	"context"
	"fmt"
	"math"
)

const (
	// phantomAbove and phantomSide place the phantom clear of the warrior's
	// reach, so a hunt has distance to close.
	phantomAbove = 6
	phantomSide  = 10
	// minSamples is how many positions a verdict that the phantom idled needs.
	minSamples = 3
)

// checkPhantom summons a phantom near the warrior and watches it. A hunting
// phantom closes on the warrior or sweeps about; one that hangs where it was
// put has no AI the scenario book can rely on.
func checkPhantom(ctx context.Context, e *Env) Result {
	const check = "phantom"
	unit := e.Opts.Units[0]
	if err := e.guard(ctx); err != nil {
		return e.cut(check, "starting", err)
	}
	w, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position first: "+err.Error(), ev{"unit": unit})
	}
	at := spotAt(w.X+phantomSide, w.Y+phantomAbove, w.Z)
	if err := e.summon(ctx, "phantom", "", at); err != nil {
		return e.cut(check, "summoning the phantom", err)
	}
	// A phantom burns in daylight and a warrior may kill it; neither says
	// anything about whether it hunts. Weakness keeps it from hurting.
	e.effect("phantom", "fire_resistance", 300, 0)
	e.effect("phantom", "weakness", 300, 255)

	var (
		samples  int
		first    pose
		last     pose
		travel   float64
		dist0    float64
		minDist  float64
		vanished bool
		start    = e.Clock.Now()
	)
	for e.Clock.Now().Sub(start) < e.Opts.Window {
		if err := e.pause(ctx, e.Opts.Poll); err != nil {
			return e.cut(check, "watching the phantom", err)
		}
		mobs, err := e.mobPoses("phantom")
		if err != nil {
			return e.cut(check, "reading the phantom's position", err)
		}
		if len(mobs) == 0 {
			vanished = true
			break
		}
		warrior, err := e.playerPose(unit)
		if err != nil {
			return e.cut(check, "reading the warrior's position", err)
		}
		p := mobs[0]
		d := p.dist(warrior)
		if samples == 0 {
			first, dist0, minDist = p, d, d
		} else {
			travel += p.dist(last)
			minDist = min(minDist, d)
		}
		last = p
		samples++
	}

	evidence := ev{
		"unit": unit, "samples": samples, "path_blocks": round1(travel),
		"distance_start": round1(dist0), "distance_min": round1(minDist),
		"closed_blocks": round1(dist0 - minDist), "vanished": vanished,
		"watched_seconds": e.Clock.Now().Sub(start).Seconds(),
	}
	if samples > 0 {
		evidence["start"] = first
		evidence["end"] = last
	}
	closing := samples > 0 && dist0-minDist >= e.Opts.PhantomClose
	moved := travel >= e.Opts.PhantomMove
	switch {
	case closing:
		return pass(check, fmt.Sprintf("the phantom closed %.1f blocks on %s", dist0-minDist, unit), evidence)
	case moved:
		return pass(check, fmt.Sprintf("the phantom flew %.1f blocks", travel), evidence)
	case samples < minSamples:
		why := "the watch ended"
		if vanished {
			why = "the phantom was gone"
		}
		return inconclusive(check, fmt.Sprintf("%s after %d reading(s), too few to tell a hunt from a hover", why, samples), evidence)
	}
	return fail(check, fmt.Sprintf("the phantom idled: %.1f blocks flown and %.1f closed in %d readings", travel, dist0-minDist, samples), evidence)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
