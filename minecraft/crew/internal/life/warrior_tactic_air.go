package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Air-combat numbers. The glide's own physics are measured in act; these are
// the fight's choices on top of them, and none has been tuned on a live
// opponent yet.
const (
	elytraName = "elytra"
	rocketName = "firework_rocket"

	// airThreatHeight is how far above the leader a threat is still focused
	// by a unit that can fly up to it.
	airThreatHeight = 40.0
	// airGroundProbe is how many cells below a target are looked at for
	// ground: a target with solid floor that close is a ground fight.
	airGroundProbe = 3

	// climbPitch is the look that trades speed for height; rockets keep the
	// speed up while it lasts.
	climbPitch = -35
	// diveHeight is how far above the target the unit must be to dive on it:
	// a dive from less leaves no room to build speed or to pull out.
	diveHeight = 6.0
	// airReach is the distance from the eye to the target's centre inside
	// which the strike swings. A flying pass is a few ticks long, so the
	// swing is taken from further out than a walking one.
	airReach = 4.5
	// strikeBegins is where the dive hands over to the strike.
	strikeBegins = 6.0
	// airAttackEvery is the ticks between swings inside a pass.
	airAttackEvery = 3
	// strikeMax bounds a pass: a unit that stays among its target without
	// closing is going round it, not hitting it.
	strikeMax = 16
	// diveMax bounds a dive that never reaches the target.
	diveMax = 80
	// recoverTicks is how long the pull-up runs before the next climb or dive.
	recoverTicks = 14
	// rocketEvery spaces rockets: a rocket's push lasts about as long, so a
	// second one before it fades is wasted.
	rocketEvery = 24
	// slowSpeed is the speed (blocks per tick) below which a climb or a
	// recovery lights a rocket first.
	slowSpeed = 0.9
	// stallSpeed is the speed under which a glide with no rocket left to
	// lean on gives up the climb and goes down.
	stallSpeed = 0.35
	// leadCap bounds how far ahead of a moving target the aim leads, in
	// ticks.
	leadCap = 20.0
	// flareHeight is the height over the ground where a landing glide levels
	// off, and releaseHeight where it lets go: a short drop costs no health.
	flareHeight   = 12.0
	releaseHeight = 2.0
	landPitch     = 15
	// groundScan is how far down a landing looks for the ground.
	groundScan = 80
)

type airPhase uint8

const (
	airLift airPhase = iota
	airClimb
	airDive
	airStrike
	airRecover
	airLand
)

func (p airPhase) String() string {
	return [...]string{"lift", "climb", "dive", "strike", "recover", "land"}[p]
}

// airState is what a unit remembers between ticks of an air fight.
type airState struct {
	phase      airPhase
	phaseSince uint64
	rocketAt   uint64
	rocketed   bool
	attackAt   uint64
	attacked   bool
	lastD      float64

	// seen and seenAt are the target's last position and the tick of it, from
	// which its velocity is read: no packet carries a mob's velocity.
	seen   mgl32.Vec3
	seenAt uint64
	seenID uint64
	vel    mgl32.Vec3
}

var airStates unitScratch[airState]

func init() {
	RegisterTactic(&airTactic{name: "air/glide", fam: tactics.FamilyAir})
	RegisterTactic(&airTactic{name: "air/glide-zombie", fam: tactics.FamilyZombie})
	RegisterTactic(&airTactic{name: "air/glide-player", fam: tactics.FamilyPlayer})
}

// airTactic fights a target out of a walker's reach from the air: climb on
// rockets, dive on the target, swing through the pass and pull up for the
// next. It is registered once per family it may be listed under and takes
// the tick only against a target in the air, by a unit with the elytra on
// and a rocket to climb with; a unit already gliding keeps the tick until it
// is down, because the ground fight cannot fly.
type airTactic struct {
	name string
	fam  tactics.Family
}

func (t *airTactic) Name() string           { return t.name }
func (t *airTactic) Family() tactics.Family { return t.fam }

func (t *airTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	l, p, e, s, a := f.Life, f.Pose, f.Target, f.Snapshot, f.Actuator
	if l.warrior == nil || l.mode != modeFight || s.Chest != elytraName {
		return false, nil
	}
	gliding := p.Gliding()
	if !gliding {
		if _, armed := rocketAt(s); !armed || !l.airborne(e) {
			return false, nil
		}
		if l.warrior.healWant {
			return false, nil
		}
		switch l.warrior.order().Kind {
		case OrderHold, OrderRegroup, OrderRetreat:
			return false, nil
		}
	}
	st := airStates.of(l)
	st.watch(l, e)
	if _, armed := rocketAt(s); gliding && (!armed || !l.airborne(e)) {
		st.enter(l, airLand)
		return true, l.glideDown(a, p)
	}
	if !gliding {
		return true, t.takeoff(l, st, a, p, e)
	}
	return true, t.fly(l, st, a, p, e, s)
}

// watch reads the target's velocity from where it has been.
func (st *airState) watch(l *Life, e belief.Entity) {
	if st.seenID != e.ID || l.tick <= st.seenAt || l.tick-st.seenAt > 4 {
		st.seenID, st.seen, st.seenAt, st.vel = e.ID, e.Pos, l.tick, mgl32.Vec3{}
		return
	}
	dt := float32(l.tick - st.seenAt)
	v := e.Pos.Sub(st.seen).Mul(1 / dt)
	st.vel = st.vel.Mul(0.5).Add(v.Mul(0.5))
	st.seen, st.seenAt = e.Pos, l.tick
}

func (st *airState) enter(l *Life, ph airPhase) {
	if st.phase != ph {
		st.phase, st.phaseSince = ph, l.tick
	}
}

// takeoff jumps, then presses glide on the way down: the server starts a
// glide only from the air.
func (t *airTactic) takeoff(l *Life, st *airState, a skill.Actuator, p act.Pose, e belief.Entity) error {
	st.enter(l, airLift)
	yaw, _ := lookAt(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}), e.Pos)
	in := act.Input{Yaw: yaw, Pitch: 0}
	switch {
	case p.OnGround:
		in.Jump = true
	case p.Rise() <= 0:
		in.Glide, in.Pitch = true, climbPitch/2
	}
	return a.Tick(in)
}

func (t *airTactic) fly(l *Life, st *airState, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	eye := p.Feet.Add(mgl32.Vec3{0, 1.62, 0})
	centre := e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0})
	d := float64(centre.Sub(eye).Len())
	h := float64(eye.Y() - centre.Y())
	speed := float64(p.Velocity().Len())
	st.advance(l, d, h)
	st.lastD = d

	in := act.Input{Glide: true, Yaw: p.Yaw}
	var rocket bool
	switch st.phase {
	case airLift, airClimb:
		in.Yaw, _ = lookAt(eye, centre)
		if horizontal(eye, centre) < 8 {
			in.Yaw = p.Yaw
		}
		in.Pitch = climbPitch
		if speed < stallSpeed {
			in.Pitch = landPitch * 2
		}
		rocket = true
	case airDive:
		in.Yaw, in.Pitch = aimAt(eye, centre, st.vel, speed)
		in.Pitch = clampf(in.Pitch, -15, 65)
	case airStrike:
		in.Yaw, in.Pitch = aimAt(eye, centre, st.vel, speed)
	case airRecover:
		in.Pitch = climbPitch
		rocket = speed < slowSpeed
	}

	if st.phase == airStrike && d <= airReach && (!st.attacked || l.tick-st.attackAt >= airAttackEvery) {
		return t.swing(l, st, a, in, e, s)
	}
	if rocket && (!st.rocketed || l.tick-st.rocketAt >= rocketEvery) && (speed < slowSpeed || h < diveHeight) {
		if r, ok := rocketAt(s); ok {
			return t.light(l, st, a, in, r, s)
		}
	}
	return a.Tick(in)
}

// advance moves the pass along: climb until above the target, dive until in
// reach, strike until past, recover, and again.
func (st *airState) advance(l *Life, d, h float64) {
	age := l.tick - st.phaseSince
	switch st.phase {
	case airLift:
		st.enter(l, airClimb)
	case airClimb:
		if h >= diveHeight {
			st.enter(l, airDive)
		}
	case airDive:
		switch {
		case d <= strikeBegins:
			st.enter(l, airStrike)
		case h < -2, age > diveMax:
			st.enter(l, airRecover)
		}
	case airStrike:
		if age > strikeMax || (d > strikeBegins+1.5 && d > st.lastD) {
			st.enter(l, airRecover)
		}
	case airRecover:
		if age >= recoverTicks {
			st.enter(l, airClimb)
		}
	case airLand:
		st.enter(l, airClimb)
	}
}

// swing turns to the target and hits it with the sword.
func (t *airTactic) swing(l *Life, st *airState, a skill.Actuator, in act.Input, e belief.Entity, s belief.Snapshot) error {
	slot, held := loadout(s)
	if (s.HeldKnown && s.HeldSlot != slot) || (!s.HeldKnown && held.Name != "") {
		if err := a.SelectHotbar(slot, held); err != nil {
			return err
		}
	}
	if err := a.Tick(in); err != nil {
		return err
	}
	st.attackAt, st.attacked = l.tick, true
	if a.Attack(e.ID, slot, held) == nil {
		l.noteSwing(e, false)
	}
	return nil
}

// light holds a rocket and lights it, after this tick's input so the push
// leaves along the look the input has just set.
func (t *airTactic) light(l *Life, st *airState, a skill.Actuator, in act.Input, r belief.Item, s belief.Snapshot) error {
	if !s.HeldKnown || s.HeldSlot != r.Slot {
		if err := a.SelectHotbar(r.Slot, r); err != nil {
			return err
		}
	}
	if err := a.Tick(in); err != nil {
		return err
	}
	st.rocketAt, st.rocketed = l.tick, true
	return a.UseRocket(r.Slot, r)
}

// aimAt is the yaw and pitch toward where the target will be when the unit
// gets there.
func aimAt(eye, centre, vel mgl32.Vec3, speed float64) (yaw, pitch float32) {
	t := math.Min(float64(centre.Sub(eye).Len())/math.Max(speed, 1), leadCap)
	return lookAt(eye, centre.Add(vel.Mul(float32(t))))
}

func clampf(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }

// rocketAt is a rocket stack on the hotbar, when the unit carries one.
func rocketAt(s belief.Snapshot) (belief.Item, bool) {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && it.Name == rocketName && it.Count > 0 {
			return it, true
		}
	}
	return belief.Item{}, false
}

// canFly says whether the unit can climb to a target out of a walker's reach.
func canFly(s belief.Snapshot) bool {
	_, armed := rocketAt(s)
	return armed && s.Chest == elytraName
}

// airborne says whether e hangs in the air: no solid cell within a few
// blocks under it. An unknown cell is not ground, so a target over unloaded
// terrain is not mistaken for one the walkers can reach.
func (l *Life) airborne(e belief.Entity) bool {
	c := cellOf(e.Pos)
	for dy := int32(0); dy <= airGroundProbe; dy++ {
		n, ok := l.world.Block(protocol.BlockPos{c.X(), c.Y() - dy, c.Z()})
		if ok && !emptyCell(n) {
			return false
		}
	}
	return true
}

// emptyCell says a cell a unit flies through.
func emptyCell(block string) bool {
	switch block {
	case "air", "cave_air", "void_air":
		return true
	}
	return false
}

// glideDown brings a gliding unit to the ground gently: a shallow glide,
// levelling off near the ground and letting go close enough above it that
// the drop costs nothing.
func (l *Life) glideDown(a skill.Actuator, p act.Pose) error {
	height := l.heightAboveGround(p.Feet)
	in := act.Input{Glide: true, Yaw: p.Yaw, Pitch: landPitch}
	switch {
	case height <= releaseHeight:
		in.Glide = false
	case height <= flareHeight:
		in.Pitch = 0
	}
	return a.Tick(in)
}

// heightAboveGround is how far the feet are above the first solid cell
// under them, or the scan's depth when none is known.
func (l *Life) heightAboveGround(feet mgl32.Vec3) float64 {
	c := cellOf(feet)
	for dy := int32(0); dy <= groundScan; dy++ {
		if n, ok := l.world.Block(protocol.BlockPos{c.X(), c.Y() - dy, c.Z()}); ok && !emptyCell(n) {
			return float64(feet.Y()) - float64(c.Y()-dy+1)
		}
	}
	return groundScan
}
