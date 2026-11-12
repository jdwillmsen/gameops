package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
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
	// wakeWindow is how long the server has to take the unit out of bed after
	// it was asked to.
	wakeWindow = 40
	// rescanTicks is how long a visit keeps looking for a bed that is not at
	// the target. After a death the world is sent again, and the bed can read
	// as missing, or sit a block off, until it has loaded.
	rescanTicks = 40
	// bedSearch is how far from the target, horizontally and vertically, a
	// re-scan looks for a bed.
	bedSearchXZ, bedSearchY = 3, 2
	// maxRescans bounds how often a bed that vanishes under the unit sends the
	// visit back to looking.
	maxRescans = 3
	// bedTapRange is how far, horizontally, from the bed's block centre the
	// unit may stand to tap it. The live server answers a tap on a bed only
	// from about 2.6 blocks (a tap from 2.65 gets no reply, not even a
	// refusal), well inside the 4.5 a chest or a placement is good for, so
	// this keeps a margin under it.
	bedTapRange = 2.2
	// bedStandRange bounds the standing spot chosen beside the bed: the eight
	// cells around it, so that arriving within bedArrive of the spot's centre
	// still leaves the unit inside bedTapRange.
	bedStandRange = 1.45
	bedArrive     = 0.4
)

var (
	errNoBed       = errors.New("no bed at the target")
	errBedNotSet   = errors.New("the server did not set the respawn point")
	errBedUnloaded = errors.New("the bed's chunk is not loaded")
	errBedNotWoken = errors.New("the unit did not leave the bed")
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
	waking     bool
	set        bool
	// origin is the target as given; a re-scan searches around it.
	origin  protocol.BlockPos
	rescan  int
	rescans int
	skipped bool
}

// NewUseBed makes the bed at the position the unit's respawn point.
func NewUseBed(world ChestWorld, self func() belief.Snapshot, bed protocol.BlockPos, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &UseBed{world: world, self: self, bed: bed, origin: bed, log: o.log}
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

// Report says why a visit that found no bed ended without setting a respawn
// point.
func (u *UseBed) Report() (string, bool) {
	if u.skipped {
		return "no bed at the target; skipped", true
	}
	return "", false
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
		if found, ok := u.findBed(); ok {
			u.bed, u.rescan = found, 0
		} else {
			if u.rescan++; u.rescan >= rescanTicks {
				return u.skip()
			}
			return u.idle(a)
		}
	}
	u.phase = bdWalk
	return next
}

// findBed looks around the original target for a bed, nearest first, in the
// cells the unit knows.
func (u *UseBed) findBed() (protocol.BlockPos, bool) {
	best, bestD, found := protocol.BlockPos{}, 1<<30, false
	for dx := int32(-bedSearchXZ); dx <= bedSearchXZ; dx++ {
		for dy := int32(-bedSearchY); dy <= bedSearchY; dy++ {
			for dz := int32(-bedSearchXZ); dz <= bedSearchXZ; dz++ {
				c := protocol.BlockPos{u.origin.X() + dx, u.origin.Y() + dy, u.origin.Z() + dz}
				if name, known := u.world.Block(c); !known || !isBed(name) {
					continue
				}
				if d := int(dx*dx + dy*dy + dz*dz); d < bestD {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}

// skip ends a visit that found no bed as done, so a plan that repeats after a
// death goes on to its next step instead of failing on a bed that is gone.
func (u *UseBed) skip() outcome {
	u.skipped = true
	if u.log != nil {
		u.log.Info("bed_skipped", map[string]any{})
	}
	return outcome{st: Done}
}

func (u *UseBed) walk(a Actuator) outcome {
	if u.nav == nil {
		pose := a.Pose()
		if u.inTapRange(pose) {
			u.phase = bdTap
			return next
		}
		stand, ok := u.standBeside(pose)
		if !ok {
			return u.failed(errors.New("no standing spot within reach of the bed"))
		}
		u.nav = newNavigator(u.world, stand, bedArrive)
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

// inTapRange says the unit stands close enough for the server to answer a tap.
func (u *UseBed) inTapRange(p act.Pose) bool {
	c := centre(u.bed)
	return math.Hypot(float64(c.X()-p.Feet.X()), float64(c.Z()-p.Feet.Z())) <= bedTapRange &&
		c.Sub(eyeOf(p.Feet)).Len() <= placeReach-arriveSlack
}

// standBeside finds the standable cell next to the bed, nearest the unit. The
// bed's other half is not one: it is the bed itself.
func (u *UseBed) standBeside(pose act.Pose) (protocol.BlockPos, bool) {
	best, bestD, found := protocol.BlockPos{}, float32(math.MaxFloat32), false
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			for dy := int32(-1); dy <= 1; dy++ {
				if dx == 0 && dz == 0 {
					continue
				}
				c := protocol.BlockPos{u.bed.X() + dx, u.bed.Y() + dy, u.bed.Z() + dz}
				if math.Hypot(float64(dx), float64(dz)) > bedStandRange || !path.Standable(u.world, c) || u.onBed(c) {
					continue
				}
				if centre(u.bed).Sub(standEye(c)).Len() > placeReach-arriveSlack {
					continue
				}
				if d := centre(c).Sub(pose.Feet).LenSqr(); d < bestD {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}

// onBed says the cell is a bed or rests on one.
func (u *UseBed) onBed(c protocol.BlockPos) bool {
	for _, p := range []protocol.BlockPos{c, {c.X(), c.Y() - 1, c.Z()}} {
		if name, known := u.world.Block(p); known && isBed(name) {
			return true
		}
	}
	return false
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
			// The bed went between the walk and the tap: look again.
			if u.rescans++; u.rescans > maxRescans {
				return u.skip()
			}
			u.phase, u.nav = bdStart, nil
			return next
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
		// A tap after a fight may get no second SetSpawnPosition for a bed
		// that is already the respawn point; that is not a failure.
		if u.set {
			u.phase = bdSleep
			return next
		}
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
	case u.slept && u.waking:
		if u.waited++; u.waited >= wakeWindow {
			return u.failed(errBedNotWoken)
		}
	case u.slept:
		if u.waited++; u.waited >= maxSleepTicks {
			// The server never woke the unit; leaving it in bed would hold it
			// there through whatever task comes next.
			if err := a.StopSleeping(); err != nil {
				return u.failed(err)
			}
			u.waking, u.waited = true, 0
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
