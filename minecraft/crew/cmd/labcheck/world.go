package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

const (
	// labTag and labName mark everything a check summons. The tag is what the
	// cleanup selects by; the name is a second handle for a mob that took the
	// name but missed the tag, so nothing a check made can be left behind.
	labTag  = "labcheck"
	labName = "labcheck"

	// cleanupTries bounds the kill-and-recount loop. A just-killed mob can
	// still match a selector for about a second, so one recount may be early.
	cleanupTries = 4
	cleanupWait  = time.Second

	// tagRadius is how far from the summon point the new mob is looked for.
	tagRadius = 4
)

// pose is where an entity stands and which way it faces, as querytarget
// reports them. Yaw is in degrees.
type pose struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Z   float64 `json:"z"`
	Yaw float64 `json:"yaw"`
}

func (p pose) dist(q pose) float64 {
	return math.Sqrt((p.X-q.X)*(p.X-q.X) + (p.Y-q.Y)*(p.Y-q.Y) + (p.Z-q.Z)*(p.Z-q.Z))
}

// errNoTargets is a selector that matched nothing.
type errNoTargets struct{ out string }

func (e errNoTargets) Error() string { return "no entity matched: " + strings.TrimSpace(e.out) }

// parsePoses reads every entity of a querytarget reply. The reply may carry a
// log prefix with brackets of its own, so each opening bracket is tried.
func parsePoses(out string) ([]pose, error) {
	if strings.Contains(out, "No targets matched") {
		return nil, errNoTargets{out}
	}
	for i := 0; i < len(out); i++ {
		if out[i] != '[' {
			continue
		}
		var targets []struct {
			Position arena.TargetPosition `json:"position"`
			YRot     float64              `json:"yRot"`
		}
		if err := json.NewDecoder(strings.NewReader(out[i:])).Decode(&targets); err != nil {
			continue
		}
		poses := make([]pose, len(targets))
		for j, t := range targets {
			poses[j] = pose{t.Position.X, t.Position.Y, t.Position.Z, t.YRot}
		}
		return poses, nil
	}
	return nil, fmt.Errorf("no querytarget JSON in %q", strings.TrimSpace(out))
}

// playerPose reads one named player's pose.
func (e *Env) playerPose(name string) (pose, error) {
	out, err := e.Console.Exec(fmt.Sprintf("querytarget @a[name=%q]", name))
	if err != nil {
		return pose{}, err
	}
	poses, err := parsePoses(out)
	switch {
	case err != nil:
		return pose{}, fmt.Errorf("%s: %w", name, err)
	case len(poses) == 0:
		return pose{}, fmt.Errorf("%s: querytarget returned no entity: %w", name, errNoTargets{out})
	}
	return poses[0], nil
}

// mobPoses reads the poses of the check's own mobs of one type. None alive is
// an empty list, not an error: a mob that died is a finding, not a fault.
func (e *Env) mobPoses(typ string) ([]pose, error) {
	out, err := e.Console.Exec(fmt.Sprintf("querytarget @e[tag=%s,type=%s]", labTag, typ))
	if err != nil {
		return nil, err
	}
	poses, err := parsePoses(out)
	if _, none := err.(errNoTargets); none {
		return nil, nil
	}
	return poses, err
}

// refusal says a console reply is the server turning a command down.
func refusal(reply string) bool {
	low := strings.ToLower(reply)
	for _, m := range []string{"syntax error", "unknown command", "no targets matched", "unexpected", "unable to", "cannot", "invalid", "error", "failed"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// spot is a whole-block point a mob is summoned at.
type spot struct{ X, Y, Z int }

func spotAt(x, y, z float64) spot {
	return spot{int(math.Round(x)), int(math.Floor(y)), int(math.Round(z))}
}

// ahead is the spot dist blocks in front of a pose, level with it. Bedrock's
// yaw 0 looks along +z and 90 along -x.
func ahead(p pose, dist float64) spot {
	r := p.Yaw * math.Pi / 180
	return spotAt(p.X-math.Sin(r)*dist, p.Y, p.Z+math.Cos(r)*dist)
}

// summon places one named, tagged mob and returns the console's reply. A
// summon the server did not take, or one that cannot be found to tag, is an
// error, and says so with the reply.
func (e *Env) summon(ctx context.Context, typ, event string, at spot) error {
	cmd := fmt.Sprintf("summon %s %q %d %d %d", typ, labName, at.X, at.Y, at.Z)
	if event != "" {
		// A spawn event wants the rotation before it, and the name last.
		cmd = fmt.Sprintf("summon %s %d %d %d 0 0 %s %q", typ, at.X, at.Y, at.Z, event, labName)
	}
	out, err := e.Console.Exec(cmd)
	if err != nil {
		return err
	}
	if refusal(out) {
		return fmt.Errorf("the server refused %q: %s", cmd, strings.TrimSpace(out))
	}
	tag := fmt.Sprintf("execute positioned %d %d %d run tag @e[type=%s,tag=!%s,c=1,r=%d] add %s", at.X, at.Y, at.Z, typ, labTag, tagRadius, labTag)
	// The mob may need a tick to exist, so one retry before giving up.
	for try := 0; ; try++ {
		out, err = e.Console.Exec(tag)
		if err != nil {
			return err
		}
		if !strings.Contains(out, "No targets matched") {
			return nil
		}
		if try == 1 {
			return fmt.Errorf("summoned a %s but found none to tag: %s", typ, strings.TrimSpace(out))
		}
		if err := e.Clock.Sleep(ctx, cleanupWait); err != nil {
			return err
		}
	}
}

// effect gives the check's own mobs of a type an effect; a miss is only
// logged, because a mob is still observable without it.
func (e *Env) effect(typ, effect string, seconds, amp int) {
	cmd := fmt.Sprintf("effect @e[tag=%s,type=%s] %s %d %d true", labTag, typ, effect, seconds, amp)
	if out, err := e.Console.Exec(cmd); err != nil || refusal(out) {
		e.logf("labcheck: %s: %v %s", cmd, err, strings.TrimSpace(out))
	}
}

// Cleanup is what is left of the check's mobs once the run is over.
type Cleanup struct {
	OK        bool `json:"ok"`
	Remaining int  `json:"remaining"`
	// Unknown is set when the survivor count could not be read.
	Unknown bool `json:"unknown,omitempty"`
}

var cleanupSelectors = []string{
	"@e[tag=" + labTag + ",type=!player]",
	"@e[name=" + labName + ",type=!player]",
}

// cleanup kills everything the checks summoned and counts what survives. It
// runs on every exit path, so it ignores the run's context and its stop
// conditions: being told to stop is the reason it matters.
func (e *Env) cleanup() Cleanup {
	var c Cleanup
	for try := 0; try < cleanupTries; try++ {
		for _, sel := range cleanupSelectors {
			_, _ = e.Console.Exec("kill " + sel)
		}
		c = Cleanup{}
		for _, sel := range cleanupSelectors {
			out, err := e.Console.Exec("testfor " + sel)
			n, perr := arena.ParseCount(out)
			if err != nil || perr != nil {
				c.Unknown = true
				continue
			}
			c.Remaining += n
		}
		if c.Remaining == 0 && !c.Unknown {
			c.OK = true
			return c
		}
		_ = e.Clock.Sleep(context.Background(), cleanupWait)
	}
	return c
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// wrapYaw keeps an angle in [-180, 180).
func wrapYaw(y float64) float64 {
	y = math.Mod(y+180, 360)
	if y < 0 {
		y += 360
	}
	return y - 180
}

// yawDiff is the smaller angle between two headings, in [0, 180].
func yawDiff(a, b float64) float64 {
	return math.Abs(wrapYaw(a - b))
}
