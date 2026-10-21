package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

type craftPhase int

const (
	// kCraft makes the item, or discovers the recipe needs a table first.
	kCraft craftPhase = iota
	// kOpen waits for the table window the click asked for.
	kOpen
)

// Craft makes count of an item from the server's recipe book. A 2x2 recipe
// is made wherever the unit stands; a 3x3 one is made through a crafting
// table within reach, which the skill opens and closes itself. Placing a
// table is a later task, so one that is not within reach fails.
type Craft struct {
	world ChestWorld
	self  func() belief.Snapshot
	item  string
	count int

	phase  craftPhase
	table  protocol.BlockPos
	waited int
	// have counts the item: what belief saw at the start plus what the
	// crafts returned, because a stack born in a slot belief thought empty
	// stays invisible to it.
	have int
	// clicked guards against re-clicking a table that never opened, and
	// opened says the window is this skill's to close.
	clicked bool
	opened  bool
}

func NewCraft(world ChestWorld, self func() belief.Snapshot, item string, count int) Skill {
	return &Craft{world: world, self: self, item: item, count: count}
}

func (c *Craft) Name() string { return "craft" }

func (c *Craft) Ready(s belief.Snapshot) error {
	return overworld(s)
}

// Resume takes the craft back up where the reflex interrupted it. Craft
// re-diagnoses the table from the window state it finds, so a window that
// closed during the fight is simply opened again.
func (c *Craft) Resume() {
	c.phase, c.clicked = kCraft, false
	c.waited = 0
}

func (c *Craft) Progress() int { return c.have }

func (c *Craft) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return c.fail(a, err)
	}
	snap := c.self()
	if err := overworld(snap); err != nil {
		return c.fail(a, err)
	}
	if c.phase == kCraft && c.have == 0 {
		// What the unit already holds counts toward the count, so a repeat
		// request crafts only the difference.
		c.have = count(snap, c.item)
		if c.have >= c.count {
			return Done, nil
		}
	}
	for {
		var o outcome
		switch c.phase {
		case kCraft:
			o = c.craft(a, snap)
		case kOpen:
			o = c.waitOpen(a)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

// craft makes one craft at a time, because the recipe decides how many of
// the item a single one yields.
func (c *Craft) craft(a Actuator, snap belief.Snapshot) outcome {
	made, err := a.Craft(c.item, 1)
	if errors.Is(err, act.ErrNeedsTable) {
		return c.openTable(a, snap)
	}
	if err != nil {
		return c.failed(a, fmt.Errorf("craft %s: %w", c.item, err))
	}
	c.have += made
	if c.have >= c.count {
		return c.finish(a)
	}
	return c.idle(a)
}

// openTable finds a crafting table the unit can click from where it stands
// and clicks it the way a chest is opened.
func (c *Craft) openTable(a Actuator, snap belief.Snapshot) outcome {
	if c.clicked {
		return c.failed(a, errors.New("the crafting table window closed before the craft finished"))
	}
	table, ok := findTable(c.world, a.Pose())
	if !ok {
		return c.failed(a, errors.New("needs crafting table"))
	}
	hash, known := c.world.BlockHash(table)
	if !known {
		return c.failed(a, errors.New("the crafting table's block is not known"))
	}
	held := holdable(snap, "")
	if err := a.SelectHotbar(held.Slot, held); err != nil {
		return c.failed(a, err)
	}
	p := a.Pose()
	if err := a.OpenChest(table, faceToward(table, eyeOf(p.Feet)), held.Slot, held, hash); err != nil {
		return c.failed(a, err)
	}
	c.table, c.clicked, c.opened, c.phase, c.waited = table, true, true, kOpen, 0
	// The click sent no input; the idle tick keeps the one-input rhythm.
	return c.idle(a)
}

// findTable picks the crafting table within reach of the unit's eyes that is
// nearest to it.
func findTable(world ChestWorld, p act.Pose) (protocol.BlockPos, bool) {
	feet := cellOf(p.Feet)
	best, bestD, found := protocol.BlockPos{}, float32(1e30), false
	const span = 3
	for dx := int32(-span); dx <= span; dx++ {
		for dy := int32(-span); dy <= span; dy++ {
			for dz := int32(-span); dz <= span; dz++ {
				cell := protocol.BlockPos{feet[0] + dx, feet[1] + dy, feet[2] + dz}
				if name, known := world.Block(cell); !known || name != "crafting_table" {
					continue
				}
				if centre(cell).Sub(eyeOf(p.Feet)).Len() > chestRange {
					continue
				}
				if d := centre(cell).Sub(p.Feet).LenSqr(); d < bestD {
					best, bestD, found = cell, d, true
				}
			}
		}
	}
	return best, found
}

func (c *Craft) waitOpen(a Actuator) outcome {
	if open, _, pos := a.Container(); open && pos == c.table {
		c.phase = kCraft
		return next
	}
	c.waited++
	if c.waited >= answerTicks {
		return c.failed(a, fmt.Errorf("crafting table did not open within %d ticks", answerTicks))
	}
	return c.idle(a)
}

func (c *Craft) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return c.failed(a, err)
	}
	return outcome{st: Running}
}

// finish closes the window the skill opened, so the unit is left without
// one, the way Deposit leaves the chest.
func (c *Craft) finish(a Actuator) outcome {
	if c.opened {
		if open, _, _ := a.Container(); open {
			if err := a.CloseContainer(); err != nil {
				return c.failed(a, err)
			}
		}
	}
	return outcome{st: Done}
}

// fail shuts the window the skill opened and reports the error.
func (c *Craft) fail(a Actuator, err error) (Status, error) {
	if c.opened {
		if open, _, _ := a.Container(); open {
			_ = a.CloseContainer()
		}
		c.opened = false
	}
	return Failed, err
}

func (c *Craft) failed(a Actuator, err error) outcome {
	st, err := c.fail(a, err)
	return outcome{st: st, err: err}
}

// Stop shuts the window when the skill is abandoned.
func (c *Craft) Stop(a Actuator) { _, _ = c.fail(a, nil) }
