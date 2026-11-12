package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// hop is the distance between waypoints. The server only streams chunks
	// near the unit and the planner reads loaded blocks only, so a hop must
	// stay well inside what the unit has loaded.
	exploreHop = 32
	// scanReach is the horizontal radius read at a waypoint. It equals the hop
	// so that neighbouring waypoints leave no unread strip between them.
	scanReach = exploreHop
	// The scan reads a band of heights around the feet: tall enough for a
	// tree on a hillside, short enough that a scan stays a few thousand
	// columns of a few dozen cells.
	scanBelow, scanAbove = 12, 28
	// A block this near is left to the mining skill, which walks the rest.
	exploreHandoff = 12
	// Nearest sightings kept per scan; a few are enough to find one that can
	// be stood beside.
	exploreCandidates = 24
	// Sightings examined, nearest first, for one that can be stood beside.
	// Canopy and cliff blocks outnumber the reachable ones, so the nearest few
	// would hide a block just past them.
	exploreExamined = 200
	// Offsets tried around a waypoint column when it has nothing to stand on.
	exploreNudge = 6
	// The lab's walkable square; waypoints are clamped inside it.
	exploreMinXZ, exploreMaxXZ = -500, 499
)

type explorePhase int

const (
	eScan explorePhase = iota
	eApproach
	eWalk
	eTravel
)

// Explore finds a kind of block in the loaded world and brings the unit near
// it. It reads the loaded blocks around the unit and, when none is there,
// visits an expanding ring of waypoints a hop apart, reading again at each,
// until the ring has covered the search box.
type Explore struct {
	world path.Blocks
	self  func() belief.Snapshot
	block string
	// box confines the search; accept, when set, further confines which
	// sightings count, so a unit that is to cut only inside a small area does
	// not stop beside a tree outside it.
	box    Area
	accept *Area
	// exclude holds blocks already tried and found unreachable, so a search
	// that is run again looks past them.
	exclude map[protocol.BlockPos]bool

	started bool
	// travelFails counts hops toward the box that could not be made.
	travelFails int
	origin      protocol.BlockPos
	// points are the waypoints, a hop apart on a grid through the origin, that
	// lie in the box. visited holds those walked to or ruled out, by grid
	// index; arrived the cells the unit actually reached, which a next hop
	// must lie near.
	points  []waypoint
	visited map[[2]int32]bool
	arrived []protocol.BlockPos
	target  waypoint

	phase explorePhase
	sight []protocol.BlockPos
	nav   *navigator
}

// NewExplore is done once a block of the kind lies within reach of a walk from
// where the unit stands. accept may be nil.
func NewExplore(world path.Blocks, self func() belief.Snapshot, block string, box Area, accept *Area) Skill {
	return newExplore(world, self, block, box, accept, nil)
}

func newExplore(world path.Blocks, self func() belief.Snapshot, block string, box Area, accept *Area, exclude map[protocol.BlockPos]bool) *Explore {
	box = clampToLab(box)
	return &Explore{world: world, self: self, block: block, box: box, accept: accept, exclude: exclude, visited: map[[2]int32]bool{}}
}

func (e *Explore) Name() string { return "explore" }

func (e *Explore) Ready(s belief.Snapshot) error { return overworld(s) }

// Resume reads the surroundings again. The waypoints already visited stay
// visited, since they are what the skill has learned; one the unit was still
// walking to was never reached and stays to be tried.
func (e *Explore) Resume() { e.phase, e.nav, e.sight = eScan, nil, nil }

func clampToLab(a Area) Area {
	c := func(v, lo, hi int32) int32 { return max(lo, min(hi, v)) }
	a.Min = protocol.BlockPos{c(a.Min.X(), exploreMinXZ, exploreMaxXZ), a.Min.Y(), c(a.Min.Z(), exploreMinXZ, exploreMaxXZ)}
	a.Max = protocol.BlockPos{c(a.Max.X(), exploreMinXZ, exploreMaxXZ), a.Max.Y(), c(a.Max.Z(), exploreMinXZ, exploreMaxXZ)}
	return a
}

func (e *Explore) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(e.self()); err != nil {
		return Failed, err
	}
	for {
		var o outcome
		switch e.phase {
		case eScan:
			o = e.scanStep(a)
		case eApproach:
			o = e.approachStep(a)
		case eWalk:
			o = e.walkStep(a)
		case eTravel:
			o = e.travelStep(a)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

func (e *Explore) scanStep(a Actuator) outcome {
	p := a.Pose()
	feet := cellOf(p.Feet)
	if !e.started {
		// A unit far from the box first travels to it: the rings are laid out
		// from where the search starts.
		if e.farFromBox(feet) {
			e.phase = eTravel
			return next
		}
		e.started = true
		e.origin = feet
		e.arrived = append(e.arrived, feet)
		e.points = e.grid()
	}
	e.sight = e.scan(feet, p)
	if len(e.sight) == 0 {
		return e.nextWaypoint(a)
	}
	if centre(e.sight[0]).Sub(p.Feet).Len() <= exploreHandoff {
		return outcome{st: Done}
	}
	e.phase = eApproach
	return next
}

// scan lists the sightings of the block in the loaded cells around feet that
// lie in the box, nearest first. Only a band of heights is read, and only once
// per waypoint. A sighting no standable cell reaches is left out: handing it
// to the mining skill would only fail there.
func (e *Explore) scan(feet protocol.BlockPos, pose act.Pose) []protocol.BlockPos {
	x0, x1 := max(feet.X()-scanReach, e.box.Min.X()), min(feet.X()+scanReach, e.box.Max.X())
	z0, z1 := max(feet.Z()-scanReach, e.box.Min.Z()), min(feet.Z()+scanReach, e.box.Max.Z())
	y0, y1 := max(feet.Y()-scanBelow, e.box.Min.Y()), min(feet.Y()+scanAbove, e.box.Max.Y())
	var found []protocol.BlockPos
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			for y := y0; y <= y1; y++ {
				b := protocol.BlockPos{x, y, z}
				if name, known := e.world.Block(b); known && name == e.block && !e.exclude[b] && (e.accept == nil || e.accept.contains(b)) {
					found = append(found, b)
				}
			}
		}
	}
	d2 := func(b protocol.BlockPos) int64 {
		dx, dy, dz := int64(b.X()-feet.X()), int64(b.Y()-feet.Y()), int64(b.Z()-feet.Z())
		return dx*dx + dy*dy + dz*dz
	}
	sort.SliceStable(found, func(i, j int) bool { return d2(found[i]) < d2(found[j]) })
	if len(found) > exploreExamined {
		found = found[:exploreExamined]
	}
	var reachable []protocol.BlockPos
	for _, b := range found {
		if _, ok := standNear(e.world, b, pose, 0); ok {
			reachable = append(reachable, b)
			if len(reachable) == exploreCandidates {
				break
			}
		}
	}
	return reachable
}

// approachStep walks to a cell beside the nearest sighting that has one.
func (e *Explore) approachStep(a Actuator) outcome {
	if e.nav == nil {
		for len(e.sight) > 0 {
			b := e.sight[0]
			e.sight = e.sight[1:]
			if stand, ok := standNear(e.world, b, a.Pose(), 0); ok {
				e.nav = newNavigator(e.world, stand, arriveSlack)
				break
			}
		}
		if e.nav == nil {
			// Every sighting was out of reach of any standing cell: look on.
			return e.nextWaypoint(a)
		}
	}
	st, err := e.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return outcome{st: Failed, err: te.err}
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		return outcome{st: Done}
	}
	// This sighting cannot be walked to; another may be.
	e.nav = nil
	return e.idle(a)
}

// waypoint is a grid point: its index from the origin and the cell.
type waypoint struct {
	idx  [2]int32
	cell protocol.BlockPos
}

// nextWaypoint picks the next waypoint it can plan to and starts the walk
// there, or fails once none is left. A waypoint with no ground to stand on is
// ruled out and the next is tried.
func (e *Explore) nextWaypoint(a Actuator) outcome {
	for {
		wp, ok := e.pick(a.Pose())
		if !ok {
			return outcome{st: Failed, err: fmt.Errorf("no %s found in search area", e.block)}
		}
		stand, ok := e.standNearColumn(wp.cell, a.Pose(), true)
		if !ok {
			// Unloaded or unwalkable ground: ruled out, and the next may do.
			e.visited[wp.idx] = true
			continue
		}
		e.target = wp
		e.nav = newNavigator(e.world, stand, arriveSlack)
		e.phase = eWalk
		return next
	}
}

// grid lists the waypoints inside the box: cells a hop apart in x and z from
// the origin. The scan at each reaches a hop, so none of the box is left
// unread between them.
func (e *Explore) grid() []waypoint {
	lo := func(min, o int32) int32 { return -((o - min) / exploreHop) }
	hi := func(max, o int32) int32 { return (max - o) / exploreHop }
	var pts []waypoint
	for gx := lo(e.box.Min.X(), e.origin.X()); gx <= hi(e.box.Max.X(), e.origin.X()); gx++ {
		for gz := lo(e.box.Min.Z(), e.origin.Z()); gz <= hi(e.box.Max.Z(), e.origin.Z()); gz++ {
			c := protocol.BlockPos{e.origin.X() + gx*exploreHop, e.origin.Y(), e.origin.Z() + gz*exploreHop}
			if c.X() < e.box.Min.X() || c.X() > e.box.Max.X() || c.Z() < e.box.Min.Z() || c.Z() > e.box.Max.Z() {
				continue
			}
			pts = append(pts, waypoint{idx: [2]int32{gx, gz}, cell: c})
		}
	}
	return pts
}

// reach is the farthest apart two waypoints may be for a walk between them to
// stay inside the chunks the unit has loaded: a diagonal hop, with some slack.
const hopLimit = exploreHop * 3 / 2

func sqDist(a, b protocol.BlockPos) int64 {
	dx, dz := int64(a.X())-int64(b.X()), int64(a.Z())-int64(b.Z())
	return dx*dx + dz*dz
}

// pick chooses the unvisited waypoint nearest the origin's ring that the unit
// can hop to: among those a hop from where it stands, the innermost ring
// first, so the search widens evenly; failing that the one nearest it. Only
// waypoints a hop from a place it has reached qualify, since a walk further
// would run out of loaded ground.
func (e *Explore) pick(p act.Pose) (waypoint, bool) {
	here := cellOf(p.Feet)
	limit := int64(hopLimit) * int64(hopLimit)
	var best waypoint
	bestRing, bestD, found, bestNear := int32(0), int64(0), false, false
	for _, w := range e.points {
		if e.visited[w.idx] {
			continue
		}
		reachable := false
		for _, r := range e.arrived {
			if sqDist(r, w.cell) <= limit {
				reachable = true
				break
			}
		}
		if !reachable {
			continue
		}
		ring := max(abs32(w.idx[0]), abs32(w.idx[1]))
		d := sqDist(here, w.cell)
		near := d <= limit
		better := !found ||
			(near && !bestNear) ||
			(near == bestNear && near && (ring < bestRing || ring == bestRing && d < bestD)) ||
			(near == bestNear && !near && d < bestD)
		if better {
			best, bestRing, bestD, bestNear, found = w, ring, d, near, true
		}
	}
	return best, found
}

func (e *Explore) walkStep(a Actuator) outcome {
	st, err := e.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return outcome{st: Failed, err: te.err}
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		e.visited[e.target.idx] = true
		e.arrived = append(e.arrived, e.target.cell)
		e.nav, e.phase = nil, eScan
		return next
	}
	e.nav = nil
	e.visited[e.target.idx] = true
	if o := e.nextWaypoint(a); !o.again {
		return o
	}
	// The new walk starts next step; this one still owes the server an input.
	return e.idle(a)
}

func (e *Explore) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return outcome{st: Failed, err: err}
	}
	return outcome{st: Running}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// standNearColumn finds a standable cell in or beside the waypoint's column,
// at the height nearest the unit's own, or none when the ground there is not
// loaded or is not walkable.
func (e *Explore) standNearColumn(wp protocol.BlockPos, pose act.Pose, inBox bool) (protocol.BlockPos, bool) {
	y0 := cellOf(pose.Feet).Y()
	offs := []int32{0, 3, -3, exploreNudge, -exploreNudge}
	for _, dx := range offs {
		for _, dz := range offs {
			x, z := wp.X()+dx, wp.Z()+dz
			if inBox && (x < e.box.Min.X() || x > e.box.Max.X() || z < e.box.Min.Z() || z > e.box.Max.Z()) {
				continue
			}
			if c, ok := e.groundAt(x, z, y0); ok {
				return c, true
			}
		}
	}
	return protocol.BlockPos{}, false
}

// groundAt is the standable cell in a column nearest the height y0, within the
// scan band; only the loaded cells are read.
func (e *Explore) groundAt(x, z, y0 int32) (protocol.BlockPos, bool) {
	return groundNear(e.world, x, z, y0)
}

// maxTravelFails is how many hops toward the box may fail before the search
// gives up reaching it.
const maxTravelFails = 4

// toBox is the point of the box nearest the unit, and how far off it is.
func (e *Explore) toBox(feet protocol.BlockPos) (protocol.BlockPos, float64) {
	c := func(v, lo, hi int32) int32 { return max(lo, min(hi, v)) }
	t := protocol.BlockPos{c(feet.X(), e.box.Min.X(), e.box.Max.X()), feet.Y(), c(feet.Z(), e.box.Min.Z(), e.box.Max.Z())}
	return t, math.Sqrt(float64(sqDist(feet, t)))
}

func (e *Explore) farFromBox(feet protocol.BlockPos) bool {
	_, d := e.toBox(feet)
	return d > exploreHop
}

// travelStep hops toward the box over loaded ground, a hop at a time, trying
// a shorter hop when the ground at the full one is not loaded or not walkable.
func (e *Explore) travelStep(a Actuator) outcome {
	feet := cellOf(a.Pose().Feet)
	if e.nav == nil {
		if !e.farFromBox(feet) {
			e.phase = eScan
			return next
		}
		t, d := e.toBox(feet)
		for _, frac := range []float64{1, 0.75, 0.5, 0.25} {
			l := math.Min(exploreHop, d) * frac
			hop := protocol.BlockPos{feet.X() + int32(math.Round(float64(t.X()-feet.X())/d*l)), feet.Y(), feet.Z() + int32(math.Round(float64(t.Z()-feet.Z())/d*l))}
			if stand, ok := e.standNearColumn(hop, a.Pose(), false); ok {
				e.nav = newNavigator(e.world, stand, arriveSlack)
				break
			}
		}
		if e.nav == nil {
			return e.travelStalled(a)
		}
	}
	st, err := e.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return outcome{st: Failed, err: te.err}
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		e.nav, e.travelFails = nil, 0
		e.phase = eScan
		return next
	}
	e.nav = nil
	return e.travelStalled(a)
}

func (e *Explore) travelStalled(a Actuator) outcome {
	if e.travelFails++; e.travelFails >= maxTravelFails {
		return outcome{st: Failed, err: errors.New("cannot reach the search area")}
	}
	return e.idle(a)
}
