package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// yawTolerance is how far a unit's facing may be from the teleport's before
// the teleport is said not to have turned it.
const yawTolerance = 10.0

// moveTolerance is how far a unit may have walked between the teleport and
// the reading for its facing to still be the teleport's doing.
const moveTolerance = 0.5

// tpSamples readings, tpSampleEvery apart, watch for the teleport's facing.
const (
	tpSamples     = 8
	tpSampleEvery = 200 * time.Millisecond
)

// checkTP asks whether `tp "Name" x y z yaw pitch`, the form the duel runner
// places units with, is accepted by Bedrock and turns the unit. The unit is
// teleported to where it already stands, so the only thing that can change is
// its facing.
func checkTP(ctx context.Context, e *Env) Result {
	const check = "tp"
	unit := e.Opts.Units[0]
	if err := e.guard(ctx); err != nil {
		return e.cut(check, "starting", err)
	}
	before, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position first: "+err.Error(), ev{"unit": unit})
	}
	yaw := wrapYaw(before.Yaw + 90)
	cmd := fmt.Sprintf("tp %q %s %s %s %s 0", unit, num(before.X), num(before.Y), num(before.Z), num(yaw))
	reply, err := e.Console.Exec(cmd)
	if err != nil {
		return e.cut(check, "sending the teleport", err)
	}
	evidence := ev{"unit": unit, "command": cmd, "reply": strings.TrimSpace(reply), "yaw_before": before.Yaw, "yaw_wanted": yaw}
	if !strings.Contains(reply, "Teleported") && refusal(reply) {
		return fail(check, "the server refused the tp with a facing: "+strings.TrimSpace(reply), evidence)
	}

	// A live warrior re-aims on its own within half a second (its defend
	// logic turns it toward whatever it watches), so the teleport's facing is
	// only visible in the first readings. Read quickly and pass on any hit;
	// one late reading says what the unit did next, not what the command did.
	var seen []float64
	var last pose
	for i := 0; i < tpSamples; i++ {
		p, err := e.playerPose(unit)
		if err != nil {
			return inconclusive(check, "cannot read "+unit+"'s position after the tp: "+err.Error(), evidence)
		}
		last = p
		seen = append(seen, p.Yaw)
		if p.dist(before) > moveTolerance {
			evidence["moved"], evidence["yaw_samples"] = p.dist(before), seen
			return inconclusive(check, unit+" moved after the tp, so its facing says nothing about the command", evidence)
		}
		if yawDiff(p.Yaw, yaw) <= yawTolerance {
			evidence["yaw_after"], evidence["yaw_samples"] = p.Yaw, seen
			return pass(check, "tp with a facing was accepted and turned "+unit, evidence)
		}
		if err := e.pause(ctx, tpSampleEvery); err != nil {
			return e.cut(check, "reading the unit's facing", err)
		}
	}
	evidence["yaw_after"], evidence["yaw_samples"] = seen[0], seen
	return fail(check, fmt.Sprintf("the tp was accepted but %s never faced %.0f in %d readings (last %.0f)", unit, yaw, tpSamples, last.Yaw), evidence)
}
