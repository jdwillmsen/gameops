package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// Farther than this from the unit the death point is outside the loaded
	// chunks, and the walk could only fail.
	recoverReach = 96.0
	// The server picks an item up when the unit is this close.
	recoverPickup = 1.5
	// Items spread from where the unit fell; this far around it is swept.
	recoverSweep = 6.0
	// A sweep that finds items keeps at it this long, 20 s.
	recoverSweepTicks = 400
	// No item entity in view for this long means the drops are gone or were
	// never announced: the server sends them as their chunk loads.
	recoverQuietTicks = 60
	// One item that cannot be reached in this long is left.
	recoverItemTicks = 100
	// Items despawn after 6000 ticks, so a walk that long is for nothing.
	recoverGiveUpTicks = 6000
	// Below this the unit fell out of the world.
	recoverVoidY = -60
	// An item rests within this of the floor for the unit to be level with it.
	recoverLevel = 1.5
	// The walk is judged from the centre of a cell and the pickup from the
	// item itself, which lies up to 0.7 off that centre.
	recoverWithin = recoverPickup - 0.7
)

var (
	ErrRecoverTooFar     = errors.New("the death point is too far to walk back to")
	ErrRecoverNotLoaded  = errors.New("the death point's chunk is not loaded")
	ErrRecoverHazard     = errors.New("the unit died where the drops are lost or unreachable")
	ErrRecoverDimension  = errors.New("the unit is not in the dimension it died in")
	ErrRecoverGaveUp     = errors.New("recovery ran out of time")
	errRecoverNoPosition = errors.New("the unit's position is not known yet")
)

// DropSource lists the item entities lying near a point.
type DropSource interface {
	DroppedNear(pos mgl32.Vec3, radius float64) []belief.Dropped
}

type recoverPhase int

const (
	rWalk recoverPhase = iota
	rSweep
)

// Recover walks back to where the unit died and sweeps the ground around the
// point until the drops are picked up. It is done when the inventory has
// regained as many items as were lost, when no item is left in view, or after
// a sweep's time.
type Recover struct {
	world path.Blocks
	self  func() belief.Snapshot
	drops DropSource
	at    mgl32.Vec3
	dim   int32
	lost  int

	phase    recoverPhase
	nav      *navigator
	ticks    int
	base     int
	baseSet  bool
	arrived  int
	quiet    int
	target   uint64
	attempts int
	navCell  protocol.BlockPos
	skip     map[uint64]bool
}

// NewRecover recovers the lost items dropped at a feet position in a dimension.
// lost is how many items the unit held then, which ends the sweep early once
// they are back; zero ends it only when nothing is left in view.
func NewRecover(world path.Blocks, self func() belief.Snapshot, drops DropSource, at mgl32.Vec3, dim int32, lost int) Skill {
	return &Recover{world: world, self: self, drops: drops, at: at, dim: dim, lost: lost, skip: map[uint64]bool{}}
}

func (r *Recover) Name() string { return "recover" }

// Ready judges whether a walk back is worth starting: the point is near, its
// chunk is known, and it is not somewhere the drops burn or fall away.
func (r *Recover) Ready(s belief.Snapshot) error {
	if s.Dimension != r.dim {
		return ErrRecoverDimension
	}
	if !s.PositionKnown {
		return errRecoverNoPosition
	}
	if r.at.Y() < recoverVoidY {
		return fmt.Errorf("%w: below the world", ErrRecoverHazard)
	}
	if d := math.Hypot(float64(s.Position[0]-r.at.X()), float64(s.Position[2]-r.at.Z())); d > recoverReach {
		return ErrRecoverTooFar
	}
	feet := cellOf(r.at)
	for _, c := range []protocol.BlockPos{feet, {feet.X(), feet.Y() - 1, feet.Z()}} {
		name, known := r.world.Block(c)
		if !known {
			return ErrRecoverNotLoaded
		}
		if burns(name) {
			return fmt.Errorf("%w: %s", ErrRecoverHazard, name)
		}
	}
	return nil
}

// burns says whether a block destroys a stack lying in it.
func burns(block string) bool {
	return strings.Contains(block, "lava") || strings.Contains(block, "fire") || strings.Contains(block, "magma") || strings.Contains(block, "cactus")
}

// Resume plans the walk afresh from wherever the fight left the unit, keeping
// the time and the items already given up on.
func (r *Recover) Resume() {
	if r.nav != nil {
		r.nav.resume()
	}
}

func (r *Recover) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	snap := r.self()
	if snap.Dimension != r.dim {
		return Failed, ErrRecoverDimension
	}
	if r.ticks++; r.ticks > recoverGiveUpTicks {
		return Failed, ErrRecoverGaveUp
	}
	have := carried(snap)
	if !r.baseSet {
		r.base, r.baseSet = have, true
	}
	if r.lost > 0 && have-r.base >= r.lost {
		return Done, nil
	}
	if r.phase == rWalk {
		st, err := r.walk(a)
		if st != Done {
			return st, err
		}
		r.phase, r.arrived = rSweep, r.ticks
	}
	return r.sweep(a)
}

func (r *Recover) walk(a Actuator) (Status, error) {
	if r.nav == nil {
		r.nav = newNavigator(r.world, cellOf(r.at), recoverWithin)
	}
	st, err := r.nav.step(a)
	if st == Failed {
		return Failed, fmt.Errorf("walking back to the death point: %w", err)
	}
	return st, nil
}

func (r *Recover) sweep(a Actuator) (Status, error) {
	if r.ticks-r.arrived > recoverSweepTicks {
		return Done, nil
	}
	pose := a.Pose()
	var item *belief.Dropped
	for _, d := range r.drops.DroppedNear(r.at, recoverSweep) {
		if !r.skip[d.ID] {
			d := d
			item = &d
			break
		}
	}
	if item == nil {
		if r.quiet++; r.quiet >= recoverQuietTicks {
			return Done, nil
		}
		return r.idle(a)
	}
	r.quiet = 0
	if item.ID != r.target {
		r.target, r.attempts, r.nav = item.ID, 0, nil
	}
	if cell := cellOf(item.Pos); r.nav != nil && cell != r.navCell {
		// An item that drifted, as in water, needs a new route.
		r.nav = nil
	}
	if r.attempts++; r.attempts > recoverItemTicks {
		r.skip[item.ID] = true
		r.target, r.nav = 0, nil
		return r.idle(a)
	}
	onIt := math.Hypot(float64(pose.Feet.X()-item.Pos.X()), float64(pose.Feet.Z()-item.Pos.Z())) <= recoverPickup &&
		math.Abs(float64(pose.Feet.Y()-item.Pos.Y())) <= recoverLevel
	if onIt {
		// Standing on it is all the server asks; its pickup delay passes.
		return r.idle(a)
	}
	if r.nav == nil {
		r.navCell = cellOf(item.Pos)
		r.nav = newNavigator(r.world, r.navCell, recoverWithin)
	}
	st, err := r.nav.step(a)
	switch st {
	case Running:
		return Running, nil
	case Done:
		return r.idle(a)
	}
	var tick tickError
	if errors.As(err, &tick) {
		return Failed, err
	}
	// An item that cannot be walked to is left for the next one.
	r.skip[item.ID] = true
	r.target, r.nav = 0, nil
	return r.idle(a)
}

func (r *Recover) idle(a Actuator) (Status, error) {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return Failed, err
	}
	return Running, nil
}

// carried counts every item held or worn.
func carried(s belief.Snapshot) int {
	n := len(s.Armour)
	for _, it := range s.Inventory {
		n += it.Count
	}
	return n
}
