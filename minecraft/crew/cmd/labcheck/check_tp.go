package main

import (
	"context"
	"fmt"
	"strings"
)

// yawTolerance is how far a unit's facing may be from the teleport's before
// the teleport is said not to have turned it.
const yawTolerance = 10.0

// moveTolerance is how far a unit may have walked between the teleport and
// the reading for its facing to still be the teleport's doing.
const moveTolerance = 0.5

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

	after, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position after the tp: "+err.Error(), evidence)
	}
	if yawDiff(after.Yaw, yaw) <= yawTolerance {
		evidence["yaw_after"] = after.Yaw
		return pass(check, "tp with a facing was accepted and turned "+unit, evidence)
	}
	// The unit may still have been turning on its own; look again once it
	// has had time to settle before calling the facing lost.
	if err := e.pause(ctx, e.Opts.Settle); err != nil {
		return e.cut(check, "waiting for the unit to settle", err)
	}
	later, err := e.playerPose(unit)
	if err != nil {
		return inconclusive(check, "cannot read "+unit+"'s position after settling: "+err.Error(), evidence)
	}
	evidence["yaw_after"], evidence["yaw_settled"] = after.Yaw, later.Yaw
	switch {
	case yawDiff(later.Yaw, yaw) <= yawTolerance:
		return pass(check, "tp with a facing was accepted and turned "+unit+" once it settled", evidence)
	case later.dist(before) > moveTolerance:
		evidence["moved"] = later.dist(before)
		return inconclusive(check, unit+" moved after the tp, so its facing says nothing about the command", evidence)
	}
	return fail(check, fmt.Sprintf("the tp was accepted but %s still faces %.0f, not %.0f", unit, later.Yaw, yaw), evidence)
}
