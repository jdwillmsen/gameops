package skill

import (
	"context"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// observeWithin is how close to a box's centre the unit walks before reading
// it, in chunks: inside the server's view distance the box's sub-chunks have
// been streamed to the unit, so their cells read back instead of unknown.
const observeWithin = 8

// Observe walks near a box and reports every cell of it from the unit's own
// world view.
type Observe struct {
	world ChestWorld
	self  func() belief.Snapshot
	min   protocol.BlockPos
	max   protocol.BlockPos
	walk  *navigator

	blocks    Blocks
	hasBlocks bool
}

// NewObserve reads the inclusive box once the unit stands close enough for the
// server to have streamed it.
func NewObserve(world ChestWorld, self func() belief.Snapshot, min, max protocol.BlockPos) Skill {
	centre := protocol.BlockPos{(min.X() + max.X()) / 2, (min.Y() + max.Y()) / 2, (min.Z() + max.Z()) / 2}
	return &Observe{world: world, self: self, min: min, max: max, walk: newNavigator(world, centre, observeWithin*16)}
}

func (o *Observe) Name() string { return "observe_blocks" }

// Blocks is the observation, once the box has been read.
func (o *Observe) Blocks() (Blocks, bool) { return o.blocks, o.hasBlocks }

func (o *Observe) Ready(s belief.Snapshot) error { return overworld(s) }

// Resume plans the walk again from wherever the unit now stands. A fight can
// set the walk back, but nothing has been read until the whole box has.
func (o *Observe) Resume() { o.walk.resume() }

func (o *Observe) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(o.self()); err != nil {
		return Failed, err
	}
	st, err := o.walk.step(a)
	switch {
	case st == Running:
		return Running, nil
	case st == Failed:
		return Failed, fmt.Errorf("walking to the box: %w", err)
	}
	b, err := observeBlocks(o.world, o.min, o.max)
	if err != nil {
		return Failed, err
	}
	o.blocks, o.hasBlocks = b, true
	return Done, nil
}
