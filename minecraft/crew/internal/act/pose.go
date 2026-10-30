package act

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
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

	// Sprinting scales the movement attribute by 1.3 (a +30% modifier on the
	// 0.1 base), which the server applies to both the ground step and the air
	// control; the airborne 0.02 is the flying speed the same modifier scales.
	sprintScale = 1.3
	sprintAccel = walkAccel * sprintScale
	// A jump taken while sprinting adds a fixed push along the facing on top
	// of the jump itself, which is what lets a sprint-jump outrun a sprint.
	sprintJumpBoost = 0.2
	airSprintAccel  = airAccel * sprintScale

	jumpVelocity = 0.42
	gravity      = 0.08
	verticalDrag = 0.98

	// Applied to a standing unit's delta so the server sees gravity's first
	// step even though the position does not move.
	standingDeltaY = (0 - gravity) * verticalDrag

	// In water the unit accelerates weakly, loses a fifth of its speed each
	// tick and sinks at a gentle terminal speed.
	waterAccel  = 0.02
	waterDrag   = 0.8
	waterSink   = 0.005
	swimUpAccel = 0.04
	climbOutVy  = 0.3
	// The top layer of water stops this far up its block.
	surfaceLevel = 8.0 / 9
	// With less water than this over the feet a held jump gives no swim-up,
	// which is what lets a unit bob at the surface.
	swimDepth = 0.4
	// How far above the feet a block edge may stand for the unit to climb onto it.
	climbReach = 0.6

	// Shrinks the body box so a unit resting against a face does not count as
	// overlapping it.
	skin = 1e-3
	// How far into a box the body may sit and still be held by it. A pose
	// the server accepted, or a landing rounded by float32, can end a few
	// millimetres inside a face; moving on through it would clip the unit
	// into the block, where the server pushes it back out.
	settle = 0.01
	// How far below the feet counts as standing on something.
	supportReach = 0.02

	// The most a grounded unit rises onto a ledge without jumping.
	stepHeight = 0.6

	// On a ladder the server lifts a unit that presses into it at a steady
	// rate and lets one that does not slide down no faster than this.
	ladderClimb = 0.2
	ladderSlide = 0.15
)

// Solid answers collision queries. Unknown blocks must report true, so the
// unit never walks into unloaded terrain.
type Solid interface{ Solid(protocol.BlockPos) bool }

// Shaper is what a Solid offers when it knows block shapes: the collision
// boxes of a block, in block-local coordinates and possibly reaching above
// the block, as a fence's do. A Solid without it is taken to be full cubes.
// An unknown block must be a full cube.
type Shaper interface {
	Shape(protocol.BlockPos) []shape.Box
}

// Climber is what a Solid offers when it knows which blocks the unit climbs.
type Climber interface{ Climbable(protocol.BlockPos) bool }

// Liquid is what a Solid also implements when the world has water. Water is
// never Solid: the body moves through it by the swimming model.
type Liquid interface{ Liquid(protocol.BlockPos) bool }

// Pose is the unit's predicted state, which the server must agree with.
type Pose struct {
	// Feet is the block-aligned position; the packets carry it plus the eye
	// height.
	Feet       mgl32.Vec3
	Yaw, Pitch float32
	OnGround   bool
	// Carry is the horizontal velocity (x, z) that survives into the next
	// tick. On the ground it is already scaled by the carry factor; in the
	// air it is the last step itself, which the air model drags in turn; in
	// water it is already dragged.
	Carry mgl32.Vec2

	// The next vertical step; only meaningful while airborne.
	vy float32
	// Set for a jump, so falling off an edge does not claim to be jumping.
	jumped bool
	// pushed says the unit walked into something on its last tick, which on
	// a ladder is what makes it climb.
	pushed bool
	// sprinting is what the server has been told: set while the unit sprints,
	// and dropped by the model when the sprint ends.
	sprinting bool
	// sprintHalt latches a sprint the model ended by walking into something,
	// until the caller lets go of the sprint input: the server does not
	// resume a sprint broken by a collision while the key is still held.
	sprintHalt bool

	// gliding is an elytra glide, which keeps its own velocity in glideV and
	// the ticks since it began in glideAge. boost counts the ticks a rocket
	// still pushes.
	gliding  bool
	glideV   mgl32.Vec3
	glideAge uint8
	boost    int
}

// Sprinting says whether the unit is sprinting as of its last tick.
func (p Pose) Sprinting() bool { return p.sprinting }

// Rise is the vertical step the unit makes next tick if nothing changes, which
// is zero on the ground.
func (p Pose) Rise() float32 {
	if p.OnGround {
		return 0
	}
	return p.vy
}

// stepInfo is what a tick tells the packet about the move it made.
type stepInfo struct {
	delta    mgl32.Vec3
	takeoff  bool
	airborne bool // began the tick off the ground after a jump
	landed   bool
	swimUp   bool // held the swim-up input in water
	stepUp   bool
}

func forwardDir(yaw float32) mgl32.Vec2 {
	r := float64(yaw) * math.Pi / 180
	return mgl32.Vec2{float32(-math.Sin(r)), float32(math.Cos(r))}
}

// advance runs one tick of the movement model against the world. A sprint is
// only honoured moving forward and out of water, and ends when the unit stops
// or walks into something; the caller picks the hunger floor, which the
// model cannot see.
func (p Pose) advance(forward float32, jump, sprint bool, w Solid) (Pose, stepInfo) {
	if !sprint {
		p.sprintHalt = false
	}
	if depth := WaterDepth(w, p.Feet); depth > 0 {
		p.sprinting = false
		return p.swim(depth, forward, jump, w)
	}
	var info stepInfo
	dir := forwardDir(p.Yaw)

	p.sprinting = sprint && forward > 0 && !p.sprintHalt
	walk, air := float32(walkAccel), float32(airAccel)
	if p.sprinting {
		walk, air = sprintAccel, airSprintAccel
	}

	climbing := onLadder(w, p.Feet)
	if climbing && forward > 0 && p.pushed {
		p.OnGround, p.jumped, p.vy = false, false, ladderClimb
	}

	var v mgl32.Vec2
	dy := float32(0)
	switch {
	case p.OnGround:
		v = p.Carry.Add(dir.Mul(walk * forward))
		if jump {
			info.takeoff = true
			p.OnGround, p.jumped = false, true
			dy = jumpVelocity
			p.vy = (dy - gravity) * verticalDrag
			if p.sprinting {
				v = v.Add(dir.Mul(sprintJumpBoost))
			}
		}
	default:
		info.airborne = p.jumped
		v = p.Carry.Mul(airDrag).Add(dir.Mul(air * forward))
		dy = p.vy
		p.vy = (dy - gravity) * verticalDrag
		if climbing {
			dy, p.vy = ladderMotion(dy, p.vy, forward > 0 && p.pushed)
		}
	}
	if climbing {
		for i := range v {
			v[i] = clamp(v[i], ladderSlide)
		}
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
	wanted := v
	feet[0], v[0] = moveX(w, feet, v[0])
	feet[2], v[1] = moveZ(w, feet, v[1])
	if p.OnGround && (v != wanted) {
		if up, ok := stepOnto(w, moved, wanted, feet); ok {
			// The delta stays the standing one although the position rose: the
			// server reads it as velocity, and a delta carrying the rise is
			// corrected.
			feet, v = up.feet, up.v
			info.stepUp = true
		}
	}
	info.delta[0], info.delta[2] = feet[0]-moved[0], feet[2]-moved[2]
	p.pushed = forward > 0 && v != wanted
	if p.sprinting && p.pushed {
		p.sprinting, p.sprintHalt = false, true
	}

	if p.OnGround {
		if !info.landed {
			info.delta[1] = standingDeltaY
		}
		if !info.landed && !supported(w, feet) {
			p.OnGround, p.vy = false, standingDeltaY
		}
		p.Carry = v.Mul(groundCarry)
	} else {
		p.Carry = v
	}
	p.Feet = feet
	return p, info
}

// onLadder reports whether the unit's feet are in a block it climbs.
func onLadder(w Solid, feet mgl32.Vec3) bool {
	c, ok := w.(Climber)
	return ok && c.Climbable(protocol.BlockPos{int32(math.Floor(float64(feet[0]))), int32(math.Floor(float64(feet[1]) + skin)), int32(math.Floor(float64(feet[2])))})
}

// ladderMotion is the vertical step of a unit on a ladder: steady up while it
// presses in, otherwise falling but never faster than the slide.
func ladderMotion(dy, next float32, pressing bool) (float32, float32) {
	if pressing {
		return ladderClimb, ladderClimb
	}
	if dy < -ladderSlide {
		dy = -ladderSlide
	}
	if next < -ladderSlide {
		next = -ladderSlide
	}
	return dy, next
}

func clamp(x, limit float32) float32 {
	return float32(math.Max(-float64(limit), math.Min(float64(limit), float64(x))))
}

// swim runs one tick of the water model: weak steering, heavy drag, a slow
// sink, and a kick onto any block edge the unit pushes against.
func (p Pose) swim(depth, forward float32, jump bool, w Solid) (Pose, stepInfo) {
	var info stepInfo
	dir := forwardDir(p.Yaw)
	v := p.Carry.Add(dir.Mul(waterAccel * forward))

	dy := p.vy
	if p.OnGround {
		dy = -waterSink
	}
	switch {
	case jump && depth > swimDepth:
		dy += swimUpAccel
		info.swimUp = true
	case jump && p.OnGround:
		info.takeoff = true
		dy = jumpVelocity
	}
	p.OnGround, p.jumped = false, false

	feet := p.Feet
	prevY := feet[1]
	feet[1], dy, info.landed = moveY(w, feet, dy)
	info.delta[1] = dy
	moved := feet
	want := v
	feet[0], v[0] = moveX(w, feet, v[0])
	feet[2], v[1] = moveZ(w, feet, v[1])
	info.delta[0], info.delta[2] = feet[0]-moved[0], feet[2]-moved[2]

	p.vy = dy*waterDrag - waterSink
	switch {
	case info.landed:
		p.OnGround, p.vy = true, -waterSink
	case (v[0] != want[0] || v[1] != want[1]) &&
		canClimb(w, feet[0]+want[0]*waterDrag, prevY+p.vy+climbReach, feet[2]+want[1]*waterDrag):
		p.vy = climbOutVy
	}
	p.Carry = v.Mul(waterDrag)
	p.Feet = feet
	return p, info
}

// WaterDepth is how far the surface stands above the unit's feet, or zero when
// the body is dry. The top layer of a body of water stops short of the block's
// top. A Solid that is not also a Liquid has no water.
func WaterDepth(w Solid, f mgl32.Vec3) float32 {
	l, ok := w.(Liquid)
	if !ok {
		return 0
	}
	x0, x1 := blockRange(f[0]-halfWidth, f[0]+halfWidth)
	y0, y1 := blockRange(f[1], f[1]+bodyHeight)
	z0, z1 := blockRange(f[2]-halfWidth, f[2]+halfWidth)
	var depth float32
	for bx := x0; bx <= x1; bx++ {
		for by := y0; by <= y1; by++ {
			for bz := z0; bz <= z1; bz++ {
				if !l.Liquid(protocol.BlockPos{bx, by, bz}) {
					continue
				}
				top := float32(by + 1)
				if !l.Liquid(protocol.BlockPos{bx, by + 1, bz}) {
					top = float32(by) + surfaceLevel
				}
				depth = max(depth, top-f[1])
			}
		}
	}
	return depth
}

// canClimb says whether the body would fit, clear of blocks and water, at a
// spot a step above the feet and beyond the block the unit pushes on.
func canClimb(w Solid, x, y, z float32) bool {
	f := mgl32.Vec3{x, y, z}
	for _, o := range obstacles(w, f.Sub(bodyLo), f.Add(bodyHi)) {
		if crosses(o, f, 1) && o.hi[1] > f[1]+skin && o.lo[1] < f[1]+bodyHeight-skin {
			return false
		}
	}
	return WaterDepth(w, mgl32.Vec3{x, y, z}) == 0
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

// aabb is a world-space collision box.
type aabb struct{ lo, hi mgl32.Vec3 }

// bodyLo and bodyHi are how far the body box reaches below and above a
// coordinate on each axis; the feet are its bottom face.
var (
	bodyLo = mgl32.Vec3{halfWidth, 0, halfWidth}
	bodyHi = mgl32.Vec3{halfWidth, bodyHeight, halfWidth}
)

// obstacles lists the collision boxes of every block the region touches.
func obstacles(w Solid, lo, hi mgl32.Vec3) []aabb {
	x0, x1 := blockRange(lo[0], hi[0])
	y0, y1 := blockRange(lo[1], hi[1])
	z0, z1 := blockRange(lo[2], hi[2])
	sh, shaped := w.(Shaper)
	var out []aabb
	// The cell below can reach up into the region: fences and walls are
	// taller than their block.
	for bx := x0; bx <= x1; bx++ {
		for by := y0 - 1; by <= y1; by++ {
			for bz := z0; bz <= z1; bz++ {
				pos := protocol.BlockPos{bx, by, bz}
				origin := mgl32.Vec3{float32(bx), float32(by), float32(bz)}
				if !shaped {
					if w.Solid(pos) {
						out = append(out, aabb{origin, origin.Add(mgl32.Vec3{1, 1, 1})})
					}
					continue
				}
				for _, b := range sh.Shape(pos) {
					out = append(out, aabb{origin.Add(b.Min), origin.Add(b.Max)})
				}
			}
		}
	}
	return out
}

// region is the body box at feet, grown by d along one axis.
func region(f mgl32.Vec3, axis int, d float32) (lo, hi mgl32.Vec3) {
	lo, hi = f.Sub(bodyLo), f.Add(bodyHi)
	if d > 0 {
		hi[axis] += d
	} else {
		lo[axis] += d
	}
	return lo, hi
}

// sweep moves the body along one axis by d and returns where it ends and
// whether a box stopped it. It ends touching the first box in its way, which
// is where the server stops it.
func sweep(w Solid, f mgl32.Vec3, axis int, d float32) (float32, bool) {
	target := f[axis] + d
	if d == 0 {
		return target, false
	}
	lo, hi := region(f, axis, d)
	stopped := false
	for _, o := range obstacles(w, lo, hi) {
		if !crosses(o, f, axis) {
			continue
		}
		if d > 0 {
			// Only boxes ahead of the front face block; one the body is well
			// inside is something it is leaving, not something it hits.
			if o.lo[axis] < f[axis]+bodyHi[axis]-settle {
				continue
			}
			if limit := o.lo[axis] - bodyHi[axis]; limit < target {
				target, stopped = limit, true
			}
			continue
		}
		if o.hi[axis] > f[axis]-bodyLo[axis]+settle {
			continue
		}
		if limit := o.hi[axis] + bodyLo[axis]; limit > target {
			target, stopped = limit, true
		}
	}
	if stopped && ((d > 0 && target < f[axis]) || (d < 0 && target > f[axis])) {
		target = f[axis]
	}
	return target, stopped
}

// crosses reports whether the body, at feet f, overlaps the box on the two
// axes other than the one it moves along.
func crosses(o aabb, f mgl32.Vec3, axis int) bool {
	for a := 0; a < 3; a++ {
		if a == axis {
			continue
		}
		if o.lo[a] >= f[a]+bodyHi[a]-skin || o.hi[a] <= f[a]-bodyLo[a]+skin {
			return false
		}
	}
	return true
}

func headBlocked(w Solid, f mgl32.Vec3) bool {
	lo, hi := region(f, 1, supportReach)
	top := f[1] + bodyHeight
	for _, o := range obstacles(w, lo, hi) {
		if crosses(o, f, 1) && o.lo[1] >= top-skin {
			return true
		}
	}
	return false
}

func supported(w Solid, f mgl32.Vec3) bool {
	lo, hi := region(f, 1, -supportReach)
	for _, o := range obstacles(w, lo, hi) {
		if crosses(o, f, 1) && o.hi[1] > f[1]-supportReach && o.hi[1] <= f[1]+skin {
			return true
		}
	}
	return false
}

func blockRange(lo, hi float32) (int32, int32) {
	return int32(math.Floor(float64(lo) + skin)), int32(math.Floor(float64(hi) - skin))
}

// moveX and moveZ stop the unit with its box touching the first face in its
// way and zero the blocked velocity.
func moveX(w Solid, f mgl32.Vec3, dx float32) (float32, float32) {
	nx, stopped := sweep(w, f, 0, dx)
	if stopped {
		return nx, 0
	}
	return nx, dx
}

func moveZ(w Solid, f mgl32.Vec3, dz float32) (float32, float32) {
	nz, stopped := sweep(w, f, 2, dz)
	if stopped {
		return nz, 0
	}
	return nz, dz
}

// moveY applies a vertical step, clamping it at a floor (landed) or a ceiling.
func moveY(w Solid, f mgl32.Vec3, dy float32) (y, applied float32, landed bool) {
	ny, stopped := sweep(w, f, 1, dy)
	return ny, ny - f[1], stopped && dy < 0
}

// stepResult is where a step onto a ledge leaves the unit.
type stepResult struct {
	feet mgl32.Vec3
	v    mgl32.Vec2
}

// stepOnto retries a blocked ground move from a raised start, as the server
// does for a unit on the ground: it lifts the unit by the step height (less
// if a ceiling is in the way), moves horizontally, and sets it back down. The
// result is used only if it carries the unit further than the flat move did,
// which ended at flat.
func stepOnto(w Solid, start mgl32.Vec3, v mgl32.Vec2, flat mgl32.Vec3) (stepResult, bool) {
	lift := stepLift(w, start, v)
	if lift <= skin {
		return stepResult{}, false
	}
	f := start
	f[1] += lift
	var out stepResult
	f[0], out.v[0] = moveX(w, f, v[0])
	f[2], out.v[1] = moveZ(w, f, v[1])
	gained := sq(f[0]-start[0]) + sq(f[2]-start[2])
	if gained <= sq(flat[0]-start[0])+sq(flat[2]-start[2]) {
		return stepResult{}, false
	}
	f[1], _, _ = moveY(w, f, -lift)
	out.feet = f
	return out, true
}

// stepLift is how far a unit can rise to step along v: the step height, or
// less if something overhangs where it is going. The server stretches the
// body towards the move before it measures, so a lintel over the ledge limits
// the lift though nothing is above the unit where it stands.
func stepLift(w Solid, f mgl32.Vec3, v mgl32.Vec2) float32 {
	lo, hi := region(f, 1, stepHeight)
	for i, a := range [2]int{0, 2} {
		if v[i] > 0 {
			hi[a] += v[i]
		} else {
			lo[a] += v[i]
		}
	}
	top := f[1] + bodyHeight
	lift := float32(stepHeight)
	for _, o := range obstacles(w, lo, hi) {
		if o.lo[0] >= hi[0]-skin || o.hi[0] <= lo[0]+skin || o.lo[2] >= hi[2]-skin || o.hi[2] <= lo[2]+skin {
			continue
		}
		if o.lo[1] >= top-skin && o.lo[1]-top < lift {
			lift = o.lo[1] - top
		}
	}
	return lift
}

func sq(x float32) float32 { return x * x }
