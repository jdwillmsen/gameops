package life

import (
	"context"
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	pauseMin, pauseMax = 40, 160
	lookMin, lookMax   = 30, 80
	// Degrees a head turns per tick; a look is a glance, not a snap.
	turnRate = 12
	// A walk covers this many blocks, never more than the radius allows.
	walkMin, walkMax = 2, 6
	walkNodes        = 400
	walkAttempts     = 4
	// A planned walk longer than this has gone around something.
	maxWalkCells = 14
	// Walks keep within this many blocks of the height the unit went idle at.
	heightBand = 2
	walkWithin = 0.5
)

type idleState struct {
	anchored bool
	anchor   protocol.BlockPos
	lastTick uint64
	walk     skill.Skill
	pause    int

	lookYaw, lookPitch float32
	lookLeft           int
}

// Idle sends this tick's input for a unit with nothing to do. It always
// sends one: the server expects an input every tick from a player that is
// alive, and moves a unit that sends none by knockback it never answers.
func (l *Life) Idle(ctx context.Context, a skill.Actuator) error {
	// A glide that outlived its fight comes down before anything else: the
	// walking idle would drop the unit out of the sky.
	if p := a.Pose(); p.Gliding() {
		return l.glideDown(a, p)
	}
	// A fight may have ended with the shield raised or a bow half drawn;
	// walking wants the hand.
	l.stowShield(a)
	l.cancelDraw(a)
	p := a.Pose()
	st := &l.idle
	// A unit that a fight or a retreat moved far off anchors where it is now.
	moved := !st.anchored || far(p, st.anchor, 2*float64(l.cfg.Radius))
	if moved {
		st.anchored, st.anchor = true, cellOf(p.Feet)
	}
	// A gap in the ticks means something else had the unit; start afresh.
	if moved || l.tick-st.lastTick > 2 {
		st.walk, st.pause = nil, l.between(pauseMin, pauseMax)
		st.lookYaw, st.lookPitch, st.lookLeft = p.Yaw, p.Pitch, 0
	}
	st.lastTick = l.tick
	if !l.cfg.Wander {
		return l.still(a)
	}

	if st.walk != nil {
		if idleWalkInBounds(p, st.anchor, l.cfg.Radius) {
			if status, _ := st.walk.Step(ctx, a); status == skill.Running {
				if !idleWalkInBounds(a.Pose(), st.anchor, l.cfg.Radius) {
					st.walk, st.pause = nil, l.between(pauseMin, pauseMax)
				}
				return nil
			}
		}
		st.walk, st.pause = nil, l.between(pauseMin, pauseMax)
		p = a.Pose()
	}
	if !idleWalkInBounds(p, st.anchor, l.cfg.Radius) {
		st.anchored, st.anchor = true, cellOf(p.Feet)
		st.pause = l.between(pauseMin, pauseMax)
		st.lookYaw, st.lookPitch, st.lookLeft = p.Yaw, p.Pitch, 0
	}
	if st.pause--; st.pause <= 0 {
		if l.startWalk(p) {
			if status, _ := st.walk.Step(ctx, a); status == skill.Running {
				if !idleWalkInBounds(a.Pose(), st.anchor, l.cfg.Radius) {
					st.walk, st.pause = nil, l.between(pauseMin, pauseMax)
				}
				return nil
			}
			st.walk = nil
		}
		st.pause = l.between(pauseMin, pauseMax)
	}
	return a.Tick(l.glance(p))
}

func idleWalkInBounds(p act.Pose, anchor protocol.BlockPos, radius int) bool {
	feet := cellOf(p.Feet)
	return !far(p, anchor, float64(radius)+0.6) &&
		feet.Y() >= anchor.Y()-heightBand && feet.Y() <= anchor.Y()+heightBand
}

func far(p act.Pose, anchor protocol.BlockPos, r float64) bool {
	return math.Hypot(float64(p.Feet.X())-float64(anchor.X())-0.5, float64(p.Feet.Z())-float64(anchor.Z())-0.5) > r
}

func (l *Life) between(lo, hi int) int { return lo + l.rng.Intn(hi-lo+1) }

// glance turns the head a little toward a random spot, picking a new one every
// second or two.
func (l *Life) glance(p act.Pose) act.Input {
	st := &l.idle
	if st.lookLeft--; st.lookLeft <= 0 {
		st.lookYaw = wrap(p.Yaw + float32(l.rng.Intn(181)-90))
		st.lookPitch = float32(l.rng.Intn(36) - 15)
		st.lookLeft = l.between(lookMin, lookMax)
	}
	diff := wrap(st.lookYaw - p.Yaw)
	step := float32(math.Max(-turnRate, math.Min(turnRate, float64(diff))))
	pitch := p.Pitch + float32(math.Max(-4, math.Min(4, float64(st.lookPitch-p.Pitch))))
	return act.Input{Yaw: wrap(p.Yaw + step), Pitch: pitch}
}

// wrap puts an angle in [-180, 180).
func wrap(deg float32) float32 {
	d := math.Mod(float64(deg)+180, 360)
	if d < 0 {
		d += 360
	}
	return float32(d - 180)
}

// startWalk plans a short walk to a random spot inside the radius and, if one
// is found, sets it going. Only safe floor within a band of heights is used, so
// a wander never leads off a ledge, into water or through unloaded terrain.
func (l *Life) startWalk(p act.Pose) bool {
	st := &l.idle
	from := cellOf(p.Feet)
	for i := 0; i < walkAttempts; i++ {
		angle := l.rng.Float64() * 2 * math.Pi
		dist := float64(walkMin) + l.rng.Float64()*float64(walkMax-walkMin)
		x := int32(math.Floor(float64(p.Feet.X()) + math.Cos(angle)*dist))
		z := int32(math.Floor(float64(p.Feet.Z()) + math.Sin(angle)*dist))
		if math.Hypot(float64(x-st.anchor.X())+0.5, float64(z-st.anchor.Z())+0.5) > float64(l.cfg.Radius) {
			continue
		}
		goal, ok := cellAt(l.world, x, z, from.Y())
		if !ok || goal == from {
			continue
		}
		minY, maxY := st.anchor.Y()-heightBand, st.anchor.Y()+heightBand
		plan, err := path.Find(l.world, from, goal, walkNodes)
		if err != nil || len(plan) == 0 || len(plan) > maxWalkCells ||
			!safePath(l.world, plan, minY, maxY) || !withinRadius(plan, st.anchor, float64(l.cfg.Radius)) {
			continue
		}
		st.walk = skill.NewNavigate(l.world, l.self, goal, walkWithin)
		return true
	}
	return false
}

func withinRadius(plan []protocol.BlockPos, anchor protocol.BlockPos, radius float64) bool {
	for _, p := range plan {
		dx := float64(p.X() - anchor.X())
		dz := float64(p.Z() - anchor.Z())
		if math.Hypot(dx, dz) > radius {
			return false
		}
	}
	return true
}
