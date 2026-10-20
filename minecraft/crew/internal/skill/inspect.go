package skill

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// inspectReach is how near the chest the unit must be to open it. A chest
// opens from 6 blocks, which leaves room for drift while the click lands.
const inspectReach = 4.5

var errNotChest = errors.New("not a chest")

type inspectPhase int

const (
	iWalk inspectPhase = iota
	iOpen
	iDrain
)

// Inspect opens a chest only to read what is in it, and takes nothing.
type Inspect struct {
	world ChestWorld
	self  func() belief.Snapshot
	chest protocol.BlockPos
	// walk goes to a cell beside the chest; the chest's own cell cannot be
	// walked to. It is made when the walk starts, from where the unit stands.
	walk Skill

	phase   inspectPhase
	opened  bool
	waited  int
	seen    ChestSeen
	hasSeen bool
}

// NewInspect walks within reach of the chest, opens it, records its contents
// and closes it.
func NewInspect(world ChestWorld, self func() belief.Snapshot, chest protocol.BlockPos) Skill {
	return &Inspect{world: world, self: self, chest: chest}
}

func (i *Inspect) Name() string { return "inspect" }

func (i *Inspect) Ready(s belief.Snapshot) error { return overworld(s) }

// Seen is the chest's contents as they stood when it was closed.
func (i *Inspect) Seen() (ChestSeen, bool) { return i.seen, i.hasSeen }

// Resume walks again: a fight may have moved the unit, and Stop shut the window.
func (i *Inspect) Resume() {
	i.phase, i.opened, i.waited = iWalk, false, 0
	i.walk = nil
}

// Stop shuts the chest when the skill is abandoned.
func (i *Inspect) Stop(a Actuator) { i.close(a) }

func (i *Inspect) close(a Actuator) {
	if open, _, _ := a.Container(); open {
		_ = a.CloseContainer()
	}
}

func (i *Inspect) fail(a Actuator, err error) (Status, error) {
	i.close(a)
	return Failed, err
}

func (i *Inspect) idle(a Actuator) (Status, error) {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return i.fail(a, err)
	}
	return Running, nil
}

func (i *Inspect) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return i.fail(a, err)
	}
	snap := i.self()
	if err := overworld(snap); err != nil {
		return i.fail(a, err)
	}
	if i.phase == iWalk {
		if i.walk == nil {
			p := a.Pose()
			if centre(i.chest).Sub(eyeOf(p.Feet)).Len() <= inspectReach {
				i.phase = iOpen
				return i.open(a, snap)
			}
			stand, ok := standCell(i.world, i.chest, p)
			if !ok {
				return i.fail(a, errors.New("no standing spot within reach of the chest"))
			}
			// Arriving within this of the stand cell keeps the chest inside
			// inspectReach, as the stand cell is within chestRange of it.
			i.walk = NewNavigate(i.world, i.self, stand, inspectReach-chestRange)
		}
		st, err := i.walk.Step(ctx, a)
		switch st {
		case Running:
			return Running, nil
		case Failed:
			return i.fail(a, fmt.Errorf("getting to the chest: %w", err))
		}
		i.phase = iOpen
	}
	if i.phase == iDrain {
		// A ContainerOpen that arrives after the wait gave up is closed rather
		// than left open on the server.
		if open, _, _ := a.Container(); open || i.waited >= drainTicks {
			return i.fail(a, errNotChest)
		}
		i.waited++
		return i.idle(a)
	}
	return i.open(a, snap)
}

func (i *Inspect) open(a Actuator, snap belief.Snapshot) (Status, error) {
	if !i.opened {
		// Clicking any other block would use it, a lever or a door included.
		if name, known := i.world.Block(i.chest); !known || (name != "chest" && name != "trapped_chest") {
			return i.fail(a, errNotChest)
		}
		hash, known := i.world.BlockHash(i.chest)
		if !known {
			return i.fail(a, errNotChest)
		}
		held := holdable(snap, "")
		p := a.Pose()
		if err := a.SelectHotbar(held.Slot, held); err != nil {
			return i.fail(a, err)
		}
		if err := a.OpenChest(i.chest, faceToward(i.chest, eyeOf(p.Feet)), held.Slot, held, hash); err != nil {
			return i.fail(a, err)
		}
		i.opened, i.waited = true, 0
		return i.idle(a)
	}
	if open, _, pos := a.Container(); open && pos == i.chest && len(a.ContainerContent()) >= chestSlots {
		items := chestItems(a, itemNames(a.ItemTable()))
		if err := a.CloseContainer(); err != nil {
			return i.fail(a, err)
		}
		i.seen, i.hasSeen = ChestSeen{Chest: i.chest, Items: items, At: time.Now()}, true
		return Done, nil
	}
	if i.waited++; i.waited >= waitTicks {
		i.phase, i.waited = iDrain, 0
	}
	return i.idle(a)
}
