package skill

import (
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// climbMax bounds the pillar a trapped unit builds before it gives up.
const climbMax = 8

var errClimbNoBlocks = errors.New("trapped with no walking path and out of blocks to climb with")

// A jump under a roof two blocks up never rises far enough to put a block under
// the unit's feet, and a block placed there while it stands in the cell is
// refused, so more tries would only wait it out.
var errClimbNoHeadroom = errors.New("no headroom to jump and place a block")

// climber lifts a unit that has no walking path to its goal, as one knocked
// into a cave under its stair: it jumps, places a block under its feet at the
// top of the jump and lands on it, until a walking path exists.
type climber struct {
	world path.Blocks
	self  func() belief.Snapshot
	goal  protocol.BlockPos

	built   int
	rising  bool
	placed  bool
	waited  int
	grace   int
	tries   int
	cell    protocol.BlockPos
	item    string
	settled int
	log     Logger
}

// step does one tick of the climb. done says a walking path now exists.
func (c *climber) step(a Actuator) (done bool, err error) {
	p := a.Pose()
	idle := act.Input{Yaw: p.Yaw, Pitch: 90}
	if !c.rising {
		if _, e := path.FindFromPos(c.world, p.Feet.X(), p.Feet.Y(), p.Feet.Z(), c.goal, 0); e == nil {
			return true, nil
		}
		if c.built >= climbMax {
			return false, fmt.Errorf("still no path after climbing %d blocks", c.built)
		}
		if !p.OnGround || p.Carry.Len() > settledSpeed {
			if c.settled++; c.settled > waitTicks {
				return false, errors.New("never settled on the ground")
			}
			return false, c.tick(a, idle)
		}
		item, ok := bridgeBlock(c.self())
		if !ok {
			return false, errClimbNoBlocks
		}
		c.item, c.cell = item, cellOf(p.Feet)
		c.rising, c.placed, c.waited, c.grace, c.settled = true, false, 0, 0, 0
		idle.Jump = true
		return false, c.tick(a, idle)
	}

	c.waited++
	if p.OnGround && c.waited > 1 {
		name, _ := c.world.Block(c.cell)
		switch {
		case name == c.item:
			c.built++
			c.rising, c.tries = false, 0
		case c.placed && c.grace < placeGrace:
			c.grace++
		default:
			if c.tries++; c.tries >= placeTries {
				c.note("climb_place_failed", p)
				if c.roofed() {
					return false, errClimbNoHeadroom
				}
				return false, errors.New("the server did not place the block")
			}
			c.rising = false
		}
	} else if c.waited > riseWait {
		return false, errors.New("jump did not land")
	}
	if c.rising && !c.placed && !p.OnGround && p.Feet.Y() >= float32(c.cell.Y())+1+placeClearance {
		switch err := a.PlaceBlock(c.cell, c.item); {
		case err == nil:
			c.placed = true
			c.note("climb_place_sent", p)
		case errors.Is(err, act.ErrItemMoving):
		default:
			c.note("climb_place_error", p)
			return false, err
		}
	}
	return false, c.tick(a, idle)
}

// roofed says whether the cell above the unit's head is known to be solid.
// An unloaded cell is not a roof: the failure then stays the unexplained one.
func (c *climber) roofed() bool {
	name, known := c.world.Block(protocol.BlockPos{c.cell.X(), c.cell.Y() + 2, c.cell.Z()})
	return known && !path.Passable(name)
}

func (c *climber) tick(a Actuator, in act.Input) error { return a.Tick(in) }

// note logs where a placement stood, so a block the server never confirms
// can be told from one that was never sent. Heights only: no coordinates.
func (c *climber) note(event string, p act.Pose) {
	if c.log == nil {
		return
	}
	name, known := c.world.Block(c.cell)
	above, _ := c.world.Block(protocol.BlockPos{c.cell.X(), c.cell.Y() + 2, c.cell.Z()})
	c.log.Info(event, map[string]any{
		"above": above,
		"built": c.built, "feet_y": p.Feet.Y(), "cell_y_offset": float32(c.cell.Y()) - p.Feet.Y(),
		"on_ground": p.OnGround, "waited": c.waited, "tries": c.tries, "cell": name, "known": known, "item": c.item,
	})
}
