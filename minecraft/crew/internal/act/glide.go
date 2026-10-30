package act

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Elytra flight, as the server runs it and as the lab measured it: the unit
// keeps its own velocity vector and turns it toward the look, trading height
// for speed in a dive and speed for height in a climb.
const (
	glideGravity = 0.08
	// glideStartTicks is how many ticks the jump press that starts a glide is
	// held. The server only starts a glide on a jump press in the air; the
	// start flag alone is ignored.
	glideStartTicks = 3

	// A firework rocket pushes the unit toward 1.5 blocks per tick along the
	// look for as long as its flight lasts, which a plain rocket does for
	// about a second.
	rocketBoostTicks = 20
	rocketTopSpeed   = 1.5
)

// Gliding says whether the unit is in an elytra glide as of its last tick.
func (p Pose) Gliding() bool { return p.gliding }

// Boosting says whether a rocket is still pushing the glide.
func (p Pose) Boosting() bool { return p.gliding && p.boost > 0 }

// Velocity is the unit's step per tick while gliding, and the step it would
// carry on falling with otherwise.
func (p Pose) Velocity() mgl32.Vec3 {
	if p.gliding {
		return p.glideV
	}
	return mgl32.Vec3{p.Carry[0], p.vy, p.Carry[1]}
}

// glideVelocity is one tick of the server's elytra update on v.
func glideVelocity(v mgl32.Vec3, yaw, pitch float32, boosted bool) mgl32.Vec3 {
	yr, pr := float64(yaw)*math.Pi/180, float64(pitch)*math.Pi/180
	look := [3]float64{-math.Sin(yr) * math.Cos(pr), -math.Sin(pr), math.Cos(yr) * math.Cos(pr)}
	vx, vy, vz := float64(v[0]), float64(v[1]), float64(v[2])
	hs := math.Hypot(vx, vz)
	lh := math.Cos(pr)
	lsq := lh * lh
	vy += glideGravity * (-1 + lsq*0.75)
	if vy < 0 && lh > 0 {
		d := vy * -0.1 * lsq
		vy += d
		vx += look[0] * d / lh
		vz += look[2] * d / lh
	}
	if pr < 0 && lh > 0 {
		d := hs * -math.Sin(pr) * 0.04
		vy += d * 3.2
		vx -= look[0] * d / lh
		vz -= look[2] * d / lh
	}
	if lh > 0 {
		vx += (look[0]/lh*hs - vx) * 0.1
		vz += (look[2]/lh*hs - vz) * 0.1
	}
	if boosted {
		vx += look[0]*0.1 + (look[0]*rocketTopSpeed-vx)*0.5
		vy += look[1]*0.1 + (look[1]*rocketTopSpeed-vy)*0.5
		vz += look[2]*0.1 + (look[2]*rocketTopSpeed-vz)*0.5
	}
	return mgl32.Vec3{float32(vx * 0.99), float32(vy * 0.98), float32(vz * 0.99)}
}

// glide runs one tick of an elytra glide, starting it from the air's momentum
// when the unit is not yet gliding. A floor under the unit ends the glide on
// the ground, and a wall zeroes the velocity into it.
func (p Pose) glide(w Solid) (Pose, stepInfo) {
	var info stepInfo
	if !p.gliding {
		p.gliding, p.glideAge = true, 0
		p.glideV = mgl32.Vec3{p.Carry[0], p.vy, p.Carry[1]}
		p.sprinting, p.jumped = false, false
	}
	boosted := p.boost > 0
	if boosted {
		p.boost--
	}
	p.glideAge = min(p.glideAge+1, 255)
	v := glideVelocity(p.glideV, p.Yaw, p.Pitch, boosted)

	feet, moved := p.Feet, p.Feet
	var landed bool
	feet[1], v[1], landed = moveY(w, feet, v[1])
	feet[0], v[0] = moveX(w, feet, v[0])
	feet[2], v[2] = moveZ(w, feet, v[2])
	info.delta = feet.Sub(moved)
	p.Feet, p.glideV = feet, v
	if landed {
		p.gliding, p.boost, p.glideAge = false, 0, 0
		p.OnGround, p.vy = true, 0
		p.Carry = mgl32.Vec2{v[0], v[2]}.Mul(groundCarry)
		info.landed = true
	}
	return p, info
}

// endGlide hands the glide's momentum back to the falling model, so a unit
// that lets go carries on from where it was.
func (p Pose) endGlide() Pose {
	p.Carry = mgl32.Vec2{p.glideV[0], p.glideV[2]}
	p.vy = p.glideV[1]
	p.gliding, p.boost, p.glideAge = false, 0, 0
	return p
}
