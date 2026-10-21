package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// placeReach is how far the eye may be from the target's centre, the
	// server's own placement reach.
	placeReach = 4.5
	// confirmTicks is the window the server has to show the placed block:
	// about two seconds at its tick rate, generous for a loaded lab server.
	confirmTicks = 40
	// placeSpan bounds the stand-cell search around the target.
	placeSpan = 4
)

type placePhase int

const (
	plWalk placePhase = iota
	plPlace
	plConfirm
)

// Place puts one block of an item into a target cell. The build materials
// name their blocks as the items do, so the world confirming the item's own
// name at the target is the placement confirmed.
type Place struct {
	world  path.Blocks
	self   func() belief.Snapshot
	target protocol.BlockPos
	item   string
	log    Logger

	phase   placePhase
	nav     *navigator
	waited  int
	retried bool
	placed  bool
}

// NewPlaceBlock places one of item at the target cell.
func NewPlaceBlock(world path.Blocks, self func() belief.Snapshot, target protocol.BlockPos, item string, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &Place{world: world, self: self, target: target, item: item, log: o.log}
}

func (p *Place) Name() string { return "place_block" }

func (p *Place) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if count(s, p.item) < 1 {
		return fmt.Errorf("no %s in inventory", p.item)
	}
	return nil
}

func (p *Place) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(p.self()); err != nil {
		return Failed, err
	}
	for {
		var o outcome
		switch p.phase {
		case plWalk:
			o = p.walk(a)
		case plPlace:
			o = p.place(a)
		case plConfirm:
			o = p.confirm(a)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

// Progress is 1 once the block is confirmed, which the runner cannot count
// from the inventory: a placement spends the item rather than gathers it.
func (p *Place) Progress() int {
	if p.placed {
		return 1
	}
	return 0
}

func (p *Place) failed(err error) outcome {
	return outcome{st: Failed, err: err}
}

// walk brings the eye within reach of the target.
func (p *Place) walk(a Actuator) outcome {
	pose := a.Pose()
	if centre(p.target).Sub(eyeOf(pose.Feet)).Len() <= placeReach-arriveSlack {
		p.phase = plPlace
		return next
	}
	if p.nav == nil {
		stand, ok := p.standCell(pose)
		if !ok {
			return p.failed(errors.New("no standing spot within reach of the target"))
		}
		p.nav = newNavigator(p.world, stand, arriveSlack)
	}
	st, err := p.nav.step(a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		p.phase = plPlace
		return next
	}
	return p.failed(fmt.Errorf("getting to the target: %w", err))
}

// standCell picks the standable cell within reach of the target, nearest the
// unit. A cell whose feet or head would be in the target is not one: the
// server refuses a placement into the unit's own body.
func (p *Place) standCell(pose act.Pose) (protocol.BlockPos, bool) {
	t := p.target
	best, bestD, found := protocol.BlockPos{}, float32(1e30), false
	for dx := int32(-placeSpan); dx <= placeSpan; dx++ {
		for dy := int32(-placeSpan); dy <= placeSpan; dy++ {
			for dz := int32(-placeSpan); dz <= placeSpan; dz++ {
				c := protocol.BlockPos{t.X() + dx, t.Y() + dy, t.Z() + dz}
				if c.X() == t.X() && c.Z() == t.Z() && (c.Y() == t.Y() || c.Y()+1 == t.Y()) {
					continue
				}
				if centre(t).Sub(standEye(c)).Len() > placeReach-arriveSlack || !path.Standable(p.world, c) {
					continue
				}
				if d := centre(c).Sub(pose.Feet).LenSqr(); d < bestD {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}

func (p *Place) place(a Actuator) outcome {
	err := a.PlaceBlock(p.target, p.item)
	switch {
	case err == nil:
		p.phase, p.waited = plConfirm, 0
		return next
	case errors.Is(err, act.ErrItemMoving):
		// The stack is still travelling to the hotbar; keep the unit
		// ticking and try again.
		return p.idle(a)
	}
	return p.failed(err)
}

// confirm waits for the world to show the block, and on silence retries the
// placement once before failing.
func (p *Place) confirm(a Actuator) outcome {
	if name, known := p.world.Block(p.target); known && name == p.item {
		p.placed = true
		if p.log != nil {
			// The item only: the target is a lab coordinate.
			p.log.Info("block_placed", map[string]any{"item": p.item})
		}
		return outcome{st: Done}
	}
	if p.waited++; p.waited >= confirmTicks {
		if p.retried {
			return p.failed(errors.New("the server did not confirm the placed block"))
		}
		p.retried, p.phase, p.waited = true, plPlace, 0
		return next
	}
	return p.idle(a)
}

func (p *Place) idle(a Actuator) outcome {
	pose := a.Pose()
	if err := a.Tick(act.Input{Yaw: pose.Yaw, Pitch: pose.Pitch}); err != nil {
		return p.failed(err)
	}
	return outcome{st: Running}
}
