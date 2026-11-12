package skill

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// homeHop is the longest single walk toward home. The planner reads loaded
	// blocks only and the server streams chunks near the unit, so a hop stays
	// well inside what the unit has loaded.
	homeHop = 32
	// homeStallTicks is how long the unit waits for ground to load before it
	// gives up: a respawn at the world spawn starts with few chunks, and they
	// arrive over seconds.
	homeStallTicks = 400
	// homeMaxHopFails is how many hops in a row may fail to plan or to walk
	// before the trip is abandoned.
	homeMaxHopFails = 6
	// homeLevel is how far above or below the home cell's floor still counts
	// as home.
	homeLevel = 3
)

var (
	errHomeNoGround = errors.New("no loaded ground toward home")
	errHomeStuck    = errors.New("could not make headway toward home")
)

// GoHome walks the unit back to a cell by hops over loaded ground, a hop at a
// time, for a unit that came back at the world spawn far from its base. It
// waits for chunks to load when the ground ahead is unknown, tries shorter
// hops when a long one cannot be planned, and gives up only after both have
// run out.
type GoHome struct {
	world  path.Blocks
	self   func() belief.Snapshot
	home   protocol.BlockPos
	within float64

	nav      *navigator
	final    bool
	stalled  int
	hopFails int
}

// NewGoHome is done when the unit stands within a distance of the home cell,
// measured on the ground plane.
func NewGoHome(world path.Blocks, self func() belief.Snapshot, home protocol.BlockPos, within float64) Skill {
	return &GoHome{world: world, self: self, home: home, within: within}
}

func (g *GoHome) Name() string { return "go_home" }

func (g *GoHome) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if !s.PositionKnown {
		return errors.New("the unit's position is not known yet")
	}
	return nil
}

// Resume plans afresh after a fight: the unit has moved since the plan.
func (g *GoHome) Resume() {
	if g.nav != nil {
		g.nav.resume()
	}
}

// AtHome says whether the feet stand within a distance of the cell, on the
// ground plane and within a few levels.
func AtHome(feet [3]float32, home protocol.BlockPos, within float64) bool {
	dx := float64(feet[0]) - (float64(home.X()) + 0.5)
	dz := float64(feet[2]) - (float64(home.Z()) + 0.5)
	return math.Hypot(dx, dz) <= within && math.Abs(float64(feet[1])-float64(home.Y())) <= homeLevel
}

func (g *GoHome) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(g.self()); err != nil {
		return Failed, err
	}
	p := a.Pose()
	if AtHome(p.Feet, g.home, g.within) {
		return Done, nil
	}
	if g.nav == nil {
		goal, final, ok := g.nextHop(p)
		if !ok {
			// Ground that is not loaded yet arrives with time; send idle inputs
			// until it does.
			if g.stalled++; g.stalled >= homeStallTicks {
				return Failed, errHomeNoGround
			}
			return g.idle(a)
		}
		g.nav, g.final = newNavigator(g.world, goal, arriveSlack), final
	}
	st, err := g.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return Failed, te.err
	}
	switch st {
	case Running:
		g.stalled = 0
		return Running, nil
	case Done:
		g.nav, g.hopFails, g.stalled = nil, 0, 0
		if g.final {
			return Done, nil
		}
		return g.idle(a)
	}
	g.nav = nil
	if g.hopFails++; g.hopFails >= homeMaxHopFails {
		return Failed, fmt.Errorf("%w: %v", errHomeStuck, err)
	}
	return g.idle(a)
}

func (g *GoHome) idle(a Actuator) (Status, error) {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return Failed, err
	}
	return Running, nil
}

// nextHop picks the next standing spot: the home cell itself once it is within
// a hop and its ground is loaded, else a spot on the line toward it. Each
// failed hop in a row shortens the next, so a cliff or a lake across the line
// is sidestepped by a shorter walk.
func (g *GoHome) nextHop(p act.Pose) (goal protocol.BlockPos, final, ok bool) {
	feet := cellOf(p.Feet)
	dx, dz := float64(g.home.X()-feet.X()), float64(g.home.Z()-feet.Z())
	d := math.Hypot(dx, dz)
	if d <= homeHop && g.hopFails == 0 {
		// Ground in the home column that is a ledge far above or below the home
		// cell is not home: reaching it would end the trip without arriving.
		if c, found := groundNear(g.world, g.home.X(), g.home.Z(), g.home.Y()); found && abs32(c.Y()-g.home.Y()) <= homeLevel {
			return c, true, true
		}
	}
	fracs := []float64{1, 0.75, 0.5, 0.25}
	for _, frac := range fracs[min(g.hopFails, len(fracs)-1):] {
		l := math.Min(homeHop, d) * frac
		x := feet.X() + int32(math.Round(dx/d*l))
		z := feet.Z() + int32(math.Round(dz/d*l))
		// A hop that rounds to the cell the unit stands in goes nowhere, and
		// would only reset the failure count.
		if c, found := groundNear(g.world, x, z, feet.Y()); found && (c.X() != feet.X() || c.Z() != feet.Z()) {
			return c, false, true
		}
	}
	return protocol.BlockPos{}, false, false
}

// groundNear is the standable cell in a loaded column nearest the height y0,
// within the band explore scans; unloaded cells are never read as ground.
func groundNear(world path.Blocks, x, z, y0 int32) (protocol.BlockPos, bool) {
	for d := int32(0); d <= scanBelow+scanAbove; d++ {
		for _, y := range [2]int32{y0 + d, y0 - d} {
			if d == 0 && y != y0 {
				continue
			}
			if y < y0-scanBelow || y > y0+scanAbove {
				continue
			}
			below, known := world.Block(protocol.BlockPos{x, y - 1, z})
			if !known || path.Passable(below) {
				continue
			}
			if c := (protocol.BlockPos{x, y, z}); path.Standable(world, c) {
				return c, true
			}
		}
	}
	return protocol.BlockPos{}, false
}
