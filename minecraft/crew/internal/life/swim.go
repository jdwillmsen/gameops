package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// Swimming up is held until the unit would coast to rest this deep, so it
	// rides just under the surface instead of leaping out of it and dropping
	// back, a bob the server's own surface rules do not reproduce tick for
	// tick. Deep enough to stay in the water, shallow enough that a body
	// pushing on a shore as high as the surface can climb it.
	holdDepth = 0.45
	// Water takes a fifth off a rising unit's speed each tick, so it coasts
	// about this many ticks' worth of its current step before it rests.
	coastTicks = 4
	// How far a shore is looked for, and how many columns are searched.
	swimReach = 24
	swimNodes = 1500
	// A route is planned again this often, since the unit drifts off it.
	replanEvery = 5
	// A shore that was not found is looked for again this often.
	retryEvery = 20
	// How far ahead on the route the unit steers, in columns.
	lookAhead = 4
	// Spacing of the points checked along a straight swim.
	sampleStep = 0.4
)

type column struct{ x, z int32 }

type swimState struct {
	active  bool
	started uint64
	planAt  uint64
	route   []column
	water   map[column]bool
	stuck   bool
}

// Swimming says whether the unit is in water and must get out of it before
// anything else. The runner asks every tick, so it also notices the unit has
// left the water.
func (l *Life) Swimming(p act.Pose) bool {
	in := act.WaterDepth(l.body, p.Feet) > 0
	st := &l.swim
	if !in && st.active {
		l.logEvent("swim_ended", map[string]any{"ticks": l.tick - st.started})
		*st = swimState{}
	}
	return in
}

// Swim sends this tick's one input: toward the nearest shore the unit can step
// onto, rising from the depths and then riding just under the surface.
func (l *Life) Swim(ctx context.Context, a skill.Actuator) error {
	p := a.Pose()
	st := &l.swim
	if !st.active {
		*st = swimState{active: true, started: l.tick}
		l.logEvent("swim_started", nil)
		l.planSwim(p)
	} else if l.tick-st.planAt >= replanInterval(st.stuck) {
		l.planSwim(p)
	}

	in := act.Input{Yaw: p.Yaw, Pitch: 0, Jump: coastDepth(l.body, p) > holdDepth}
	if len(st.route) > 0 {
		in.Yaw, in.Forward = l.heading(p), 1
	}
	return a.Tick(in)
}

// coastDepth is how deep the feet will be once the unit's rise has run out.
func coastDepth(w act.Solid, p act.Pose) float32 {
	return act.WaterDepth(w, p.Feet) - coastTicks*max(p.Rise(), 0)
}

func replanInterval(stuck bool) uint64 {
	if stuck {
		return retryEvery
	}
	return replanEvery
}

// planSwim searches the water for the nearest cell the unit can stand on.
func (l *Life) planSwim(p act.Pose) {
	st := &l.swim
	st.planAt = l.tick
	st.route, st.water, st.stuck = nil, nil, false
	start := cellOf(p.Feet)
	surf, ok := l.surfaceY(start)
	if !ok {
		st.stuck = true
		return
	}
	from := column{start.X(), start.Z()}
	prev := map[column]column{from: from}
	water := map[column]bool{from: true}
	queue := []column{from}
	for n := 0; len(queue) > 0 && n < swimNodes; n++ {
		c := queue[0]
		queue = queue[1:]
		for _, d := range [...]column{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			next := column{c.x + d.x, c.z + d.z}
			if _, seen := prev[next]; seen {
				continue
			}
			if math.Hypot(float64(next.x-from.x), float64(next.z-from.z)) > swimReach {
				continue
			}
			prev[next] = c
			if l.swimmable(next, surf) {
				water[next] = true
				queue = append(queue, next)
				continue
			}
			if _, ok := l.shoreCell(next, surf); ok {
				var route []column
				for at := next; at != from; at = prev[at] {
					route = append(route, at)
				}
				for i, j := 0, len(route)-1; i < j; i, j = i+1, j-1 {
					route[i], route[j] = route[j], route[i]
				}
				water[next] = true
				st.route, st.water = route, water
				return
			}
		}
	}
	st.stuck = true
}

// surfaceY is the height of the first cell above the water in the unit's
// column, found from the water the unit is in.
func (l *Life) surfaceY(feet protocol.BlockPos) (int32, bool) {
	y := feet.Y()
	// The feet can be above the water the head is in only by a cell.
	for i := 0; i < 2 && !l.waterAt(protocol.BlockPos{feet.X(), y, feet.Z()}); i++ {
		y++
	}
	for i := 0; i < 64; i++ {
		if !l.waterAt(protocol.BlockPos{feet.X(), y, feet.Z()}) {
			return y, true
		}
		y++
	}
	return 0, false
}

func (l *Life) waterAt(c protocol.BlockPos) bool {
	name, known := l.world.Block(c)
	return known && path.Water(name)
}

// swimmable says whether the unit can swim through a column whose water ends
// at the height of the surface cell it is already in.
func (l *Life) swimmable(c column, surf int32) bool {
	if !l.waterAt(protocol.BlockPos{c.x, surf - 1, c.z}) {
		return false
	}
	name, known := l.world.Block(protocol.BlockPos{c.x, surf, c.z})
	return known && (path.Passable(name) || path.Water(name))
}

// shoreCell finds where in a column the unit can stand: level with the surface
// or a step above it, which the climb out of the water can reach.
func (l *Life) shoreCell(c column, surf int32) (protocol.BlockPos, bool) {
	for _, dy := range [...]int32{0, 1} {
		cell := protocol.BlockPos{c.x, surf + dy, c.z}
		if safeStand(l.world, cell) {
			return cell, true
		}
	}
	return protocol.BlockPos{}, false
}

// heading is the yaw toward the farthest point of the route that a straight
// swim reaches without leaving the water.
func (l *Life) heading(p act.Pose) float32 {
	st := &l.swim
	here := column{int32(math.Floor(float64(p.Feet.X()))), int32(math.Floor(float64(p.Feet.Z())))}
	// The nearest route column to where the unit is now.
	at := -1
	best := math.MaxFloat64
	for i, c := range st.route {
		if d := math.Hypot(float64(c.x-here.x), float64(c.z-here.z)); d < best {
			at, best = i, d
		}
	}
	aim := st.route[len(st.route)-1]
	for i := min(len(st.route)-1, at+lookAhead); i >= at && i >= 0; i-- {
		if l.straightSwim(p.Feet, st.route[i]) {
			aim = st.route[i]
			break
		}
	}
	to := mgl32.Vec3{float32(aim.x) + 0.5, p.Feet.Y(), float32(aim.z) + 0.5}
	yaw, _ := lookAt(p.Feet, to)
	return yaw
}

// straightSwim says whether the line from the feet to the middle of a column
// stays over water, except for its last column, which may be the shore.
func (l *Life) straightSwim(from mgl32.Vec3, to column) bool {
	st := &l.swim
	dx, dz := float64(to.x)+0.5-float64(from.X()), float64(to.z)+0.5-float64(from.Z())
	n := int(math.Ceil(math.Hypot(dx, dz) / sampleStep))
	for i := 1; i < n; i++ {
		t := float64(i) / float64(n)
		c := column{int32(math.Floor(float64(from.X()) + dx*t)), int32(math.Floor(float64(from.Z()) + dz*t))}
		if c != to && !st.water[c] {
			return false
		}
	}
	return true
}
