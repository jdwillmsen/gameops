package act

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The server's movement model, as measured on the lab. Unit tests pin these;
// a unit that drifts from them is corrected by the server.
const (
	eyeHeight  = 1.62
	halfWidth  = 0.3
	bodyHeight = 1.8

	walkAccel   = 0.098
	groundCarry = 0.546 // share of a ground step that survives into the next tick
	airDrag     = 0.91
	airAccel    = 0.02

	jumpVelocity = 0.42
	gravity      = 0.08
	verticalDrag = 0.98

	// Applied to a standing unit's delta so the server sees gravity's first
	// step even though the position does not move.
	standingDeltaY = (0 - gravity) * verticalDrag

	// Shrinks the body box so a unit resting against a face does not count as
	// overlapping it.
	skin = 1e-3
	// How far below the feet counts as standing on something.
	supportReach = 0.02
)

// Solid answers collision queries. Unknown blocks must report true, so the
// unit never walks into unloaded terrain.
type Solid interface{ Solid(protocol.BlockPos) bool }

// Pose is the unit's predicted state, which the server must agree with.
type Pose struct {
	// Feet is the block-aligned position; the packets carry it plus the eye
	// height.
	Feet       mgl32.Vec3
	Yaw, Pitch float32
	OnGround   bool
	// Carry is the horizontal velocity (x, z) that survives into the next
	// tick. On the ground it is already scaled by the carry factor; in the
	// air it is the last step itself, which the air model drags in turn.
	Carry mgl32.Vec2

	// The next vertical step; only meaningful while airborne.
	vy float32
	// Set for a jump, so falling off an edge does not claim to be jumping.
	jumped bool
}

// stepInfo is what a tick tells the packet about the move it made.
type stepInfo struct {
	delta    mgl32.Vec3
	takeoff  bool
	airborne bool // began the tick off the ground after a jump
	landed   bool
}

func forwardDir(yaw float32) mgl32.Vec2 {
	r := float64(yaw) * math.Pi / 180
	return mgl32.Vec2{float32(-math.Sin(r)), float32(math.Cos(r))}
}

// advance runs one tick of the movement model against the world.
func (p Pose) advance(forward float32, jump bool, w Solid) (Pose, stepInfo) {
	var info stepInfo
	dir := forwardDir(p.Yaw)

	var v mgl32.Vec2
	dy := float32(0)
	switch {
	case p.OnGround:
		v = p.Carry.Add(dir.Mul(walkAccel * forward))
		if jump {
			info.takeoff = true
			p.OnGround, p.jumped = false, true
			dy = jumpVelocity
			p.vy = (dy - gravity) * verticalDrag
		}
	default:
		info.airborne = p.jumped
		v = p.Carry.Mul(airDrag).Add(dir.Mul(airAccel * forward))
		dy = p.vy
		p.vy = (dy - gravity) * verticalDrag
	}

	feet := p.Feet
	if !p.OnGround {
		feet[1], dy, info.landed = moveY(w, feet, dy)
		if info.landed {
			p.OnGround, p.jumped, p.vy = true, false, 0
		} else if dy > 0 && headBlocked(w, feet) {
			p.vy = (0 - gravity) * verticalDrag
		}
		info.delta[1] = dy
	}
	moved := feet
	feet[0], v[0] = moveX(w, feet, v[0])
	feet[2], v[1] = moveZ(w, feet, v[1])
	info.delta[0], info.delta[2] = feet[0]-moved[0], feet[2]-moved[2]

	if p.OnGround {
		if !info.landed {
			info.delta[1] = standingDeltaY
			if !supported(w, feet) {
				p.OnGround, p.vy = false, standingDeltaY
			}
		}
		p.Carry = v.Mul(groundCarry)
	} else {
		p.Carry = v
	}
	p.Feet = feet
	return p, info
}

// knock adopts a velocity the server gave the unit, such as knockback. The
// server applies it on its own side, so a prediction that ignored it would be
// corrected on the next tick.
func (p *Pose) knock(v mgl32.Vec3) {
	p.Carry = mgl32.Vec2{v.X(), v.Z()}
	if v.Y() > 0 || !p.OnGround {
		p.OnGround, p.jumped = false, false
		p.vy = v.Y()
	}
}

func headBlocked(w Solid, f mgl32.Vec3) bool {
	return overlaps(w, f[0], f[1]+supportReach, f[2])
}

func supported(w Solid, f mgl32.Vec3) bool { return overlaps(w, f[0], f[1]-supportReach, f[2]) }

// overlaps reports whether the body box at feet (x, y, z) touches a solid block.
func overlaps(w Solid, x, y, z float32) bool {
	x0, x1 := blockRange(x-halfWidth, x+halfWidth)
	y0, y1 := blockRange(y, y+bodyHeight)
	z0, z1 := blockRange(z-halfWidth, z+halfWidth)
	for bx := x0; bx <= x1; bx++ {
		for by := y0; by <= y1; by++ {
			for bz := z0; bz <= z1; bz++ {
				if w.Solid(protocol.BlockPos{bx, by, bz}) {
					return true
				}
			}
		}
	}
	return false
}

func blockRange(lo, hi float32) (int32, int32) {
	return int32(math.Floor(float64(lo) + skin)), int32(math.Floor(float64(hi) - skin))
}

// moveX and moveZ stop the unit with its box touching the first solid face in
// its way, which is where the server stops it, and zero the blocked velocity.
func moveX(w Solid, f mgl32.Vec3, dx float32) (float32, float32) {
	nx := f[0] + dx
	if dx == 0 || !overlaps(w, nx, f[1], f[2]) {
		return nx, dx
	}
	if dx > 0 {
		nx = float32(math.Floor(float64(nx+halfWidth))) - halfWidth
	} else {
		nx = float32(math.Floor(float64(nx-halfWidth))) + 1 + halfWidth
	}
	return clampStep(f[0], nx, dx), 0
}

func moveZ(w Solid, f mgl32.Vec3, dz float32) (float32, float32) {
	nz := f[2] + dz
	if dz == 0 || !overlaps(w, f[0], f[1], nz) {
		return nz, dz
	}
	if dz > 0 {
		nz = float32(math.Floor(float64(nz+halfWidth))) - halfWidth
	} else {
		nz = float32(math.Floor(float64(nz-halfWidth))) + 1 + halfWidth
	}
	return clampStep(f[2], nz, dz), 0
}

// clampStep keeps a collision from pushing the unit backwards.
func clampStep(from, to, d float32) float32 {
	if (d > 0 && to < from) || (d < 0 && to > from) {
		return from
	}
	return to
}

// moveY applies a vertical step, clamping it at a floor (landed) or a ceiling.
func moveY(w Solid, f mgl32.Vec3, dy float32) (y, applied float32, landed bool) {
	ny := f[1] + dy
	if !overlaps(w, f[0], ny, f[2]) {
		return ny, dy, false
	}
	if dy > 0 {
		ny = float32(math.Floor(float64(ny+bodyHeight))) - bodyHeight
		return ny, ny - f[1], false
	}
	ny = float32(math.Floor(float64(ny))) + 1
	return ny, ny - f[1], true
}
