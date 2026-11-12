package skill

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const (
	// sleepWindow is how long the server has to put the unit to sleep after
	// the respawn point is set. By day it never will, and with monsters near
	// at night it refuses: either way the point is already set, so running
	// out of the window is a finished visit, not a failure.
	sleepWindow = 40
	// maxSleepTicks bounds a night in bed: a Bedrock night is about 10000
	// ticks, so only a server that never wakes the unit reaches it.
	maxSleepTicks = 14000
	// redoBedTicks is how long a tap may go unanswered before it is sent
	// once more, as a tap right after a join can be swallowed.
	redoBedTicks = 60
)

var (
	errNoBed       = errors.New("no bed at the target")
	errBedNotSet   = errors.New("the server did not set the respawn point")
	errBedUnloaded = errors.New("the bed's chunk is not loaded")
)

type bedPhase int

const (
	bdStart bedPhase = iota
	bdWalk
	bdTap
	bdSleep
)

// UseBed walks to a bed and taps it, which makes it the unit's respawn point
// whatever the hour; at night the server also puts the unit to sleep, and the
// unit stays in bed until the server wakes it.
type UseBed struct {
	world ChestWorld
	self  func() belief.Snapshot
	bed   protocol.BlockPos
	log   Logger

	phase  bedPhase
	nav    *navigator
	waited int
	taps   int
	// setsBefore is the count of spawn points the server had set when the
	// tap went out, so only a later one answers it.
	setsBefore int
	slept      bool
	set        bool
}

// NewUseBed makes the bed at the position the unit's respawn point.
func NewUseBed(world ChestWorld, self func() belief.Snapshot, bed protocol.BlockPos, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &UseBed{world: world, self: self, bed: bed, log: o.log}
}

func (u *UseBed) Name() string { return "use_bed" }

func (u *UseBed) Ready(s belief.Snapshot) error { return overworld(s) }

// Progress is 1 once the respawn point is set.
func (u *UseBed) Progress() int {
	if u.set {
		return 1
	}
	return 0
}

// Resume starts the visit again after a fight, keeping what was set.
func (u *UseBed) Resume() {
	u.phase, u.nav, u.waited, u.taps = bdStart, nil, 0, 0
}

// Stop gets the unit out of bed when the skill is abandoned, as for a fight:
// a sleeping unit would otherwise lie there while it is attacked.
func (u *UseBed) Stop(a Actuator) {
	if u.self().Sleeping {
		_ = a.StopSleeping()
	}
}

func (u *UseBed) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	snap := u.self()
	if err := overworld(snap); err != nil {
		return Failed, err
	}
	for {
		var o outcome
		switch u.phase {
		case bdStart:
			o = u.start(a)
		case bdWalk:
			o = u.walk(a)
		case bdTap:
			o = u.tap(a, snap)
		case bdSleep:
			o = u.sleep(a, snap)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

func (u *UseBed) failed(err error) outcome { return outcome{st: Failed, err: err} }

func (u *UseBed) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return u.failed(err)
	}
	return outcome{st: Running}
}

func (u *UseBed) start(a Actuator) outcome {
	name, known := u.world.Block(u.bed)
	if !known {
		// Unknown is usually a chunk still arriving, not a missing bed.
		if u.waited++; u.waited >= waitTicks {
			return u.failed(errBedUnloaded)
		}
		return u.idle(a)
	}
	u.waited = 0
	if !isBed(name) {
		return u.failed(errNoBed)
	}
	u.phase = bdWalk
	return next
}

func (u *UseBed) walk(a Actuator) outcome {
	// Walking up to a bed is placing's problem too: eye within block reach.
	if u.nav == nil {
		pose := a.Pose()
		if centre(u.bed).Sub(eyeOf(pose.Feet)).Len() <= placeReach-arriveSlack {
			u.phase = bdTap
			return next
		}
		stand, ok := standNear(u.world, u.bed, pose, 0)
		if !ok {
			return u.failed(errors.New("no standing spot within reach of the bed"))
		}
		u.nav = newNavigator(u.world, stand, arriveSlack)
	}
	switch st, err := u.nav.step(a); st {
	case Running:
		return outcome{st: Running}
	case Done:
		u.nav, u.phase = nil, bdTap
		return next
	default:
		return u.failed(fmt.Errorf("getting to the bed: %w", err))
	}
}

func (u *UseBed) sendTap(a Actuator, snap belief.Snapshot) error {
	hash, known := u.world.BlockHash(u.bed)
	if !known {
		return errNoBed
	}
	held := holdable(snap, "")
	p := a.Pose()
	if err := a.SelectHotbar(held.Slot, held); err != nil {
		return err
	}
	return a.InteractBlock(u.bed, faceToward(u.bed, eyeOf(p.Feet)), held.Slot, held, hash)
}

// tap waits for the server's SetSpawnPosition, the one answer it gives to a
// tap on a bed at any hour.
func (u *UseBed) tap(a Actuator, snap belief.Snapshot) outcome {
	if u.taps == 0 {
		if name, known := u.world.Block(u.bed); !known || !isBed(name) {
			return u.failed(errNoBed)
		}
		u.setsBefore = snap.SpawnSets
		if err := u.sendTap(a, snap); err != nil {
			return u.failed(err)
		}
		u.taps, u.waited = 1, 0
		return u.idle(a)
	}
	if snap.SpawnSets > u.setsBefore || snap.Sleeping {
		u.set, u.waited = true, 0
		if u.log != nil {
			// Whether it slept only: the bed is a lab coordinate.
			u.log.Info("respawn_point_set", map[string]any{"sleeping": snap.Sleeping})
		}
		u.phase = bdSleep
		return next
	}
	if u.waited++; u.waited >= answerTicks {
		return u.failed(errBedNotSet)
	}
	if u.taps < 2 && u.waited == redoBedTicks {
		if err := u.sendTap(a, snap); err != nil {
			return u.failed(err)
		}
		u.taps++
	}
	return u.idle(a)
}

// sleep gives the server its window to start sleeping, then stays in bed until
// the unit is woken.
func (u *UseBed) sleep(a Actuator, snap belief.Snapshot) outcome {
	if snap.Sleeping && !u.slept {
		u.slept, u.waited = true, 0
	}
	switch {
	case u.slept && !snap.Sleeping:
		return outcome{st: Done}
	case u.slept:
		if u.waited++; u.waited >= maxSleepTicks {
			return outcome{st: Done}
		}
	case snap.Env.DayPhase == belief.PhaseDay:
		return outcome{st: Done}
	default:
		if u.waited++; u.waited >= sleepWindow {
			return outcome{st: Done}
		}
	}
	return u.idle(a)
}

// isBed matches every colour: "bed" itself and "red_bed" alike, but not
// "bedrock".
func isBed(name string) bool { return name == "bed" || strings.HasSuffix(name, "_bed") }
