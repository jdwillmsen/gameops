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
	// The chest opens from 6 blocks; staying well inside leaves room for the
	// unit to still be drifting when the click lands.
	chestRange = 3.0
	chestSlots = 27
	// The server gives no signal for a stack limit, so a stack of this size is
	// full whatever it holds.
	fullStack = 64
)

// ChestWorld is the block store a deposit needs: blocks to walk and the chest's
// network hash to click it with.
type ChestWorld interface {
	path.Blocks
	BlockHash(protocol.BlockPos) (hash uint32, known bool)
}

type depositPhase int

const (
	dStart depositPhase = iota
	dWalk
	dOpen
	dMove
)

// Deposit moves an item from the inventory into a chest.
type Deposit struct {
	world ChestWorld
	self  func() belief.Snapshot
	chest protocol.BlockPos
	item  string
	qty   int
	log   Logger

	started bool
	base    int
	phase   depositPhase
	nav     *navigator

	opened   bool
	waited   int
	names    map[int32]string
	itemID   int32
	rejected map[int]bool

	pending  bool
	reqID    int32
	reqSlot  int
	before   int
	settling bool
}

// NewDepositChest places qty of item into the chest at the position, filling
// slots 0-26 in order, and closes it again.
func NewDepositChest(world ChestWorld, self func() belief.Snapshot, chest protocol.BlockPos, item string, qty int, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &Deposit{world: world, self: self, chest: chest, item: item, qty: qty, log: o.log, rejected: map[int]bool{}}
}

func (d *Deposit) Name() string { return "deposit_chest" }

func (d *Deposit) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if have := count(s, d.item); have < d.qty {
		return fmt.Errorf("have %d %s, need %d", have, d.item, d.qty)
	}
	return nil
}

func (d *Deposit) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return d.fail(a, err)
	}
	snap := d.self()
	if err := overworld(snap); err != nil {
		return d.fail(a, err)
	}
	have := count(snap, d.item)
	if !d.started {
		d.started, d.base = true, have
	}
	for {
		var o outcome
		switch d.phase {
		case dStart:
			o = d.start(a)
		case dWalk:
			o = d.walk(a)
		case dOpen:
			o = d.open(a, snap)
		case dMove:
			o = d.move(a, snap, have)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

// fail ends the skill with the chest shut.
func (d *Deposit) fail(a Actuator, err error) (Status, error) {
	if open, _, _ := a.Container(); open {
		_ = a.CloseContainer()
	}
	return Failed, err
}

func (d *Deposit) failed(a Actuator, err error) outcome {
	st, err := d.fail(a, err)
	return outcome{st: st, err: err}
}

func (d *Deposit) start(a Actuator) outcome {
	if name, known := d.world.Block(d.chest); !known || (name != "chest" && name != "trapped_chest") {
		return d.failed(a, errors.New("no chest at the target"))
	}
	p := a.Pose()
	if centre(d.chest).Sub(eyeOf(p.Feet)).Len() <= chestRange {
		d.phase = dOpen
		return next
	}
	stand, ok := d.standCell(p)
	if !ok {
		return d.failed(a, errors.New("no standing spot within reach of the chest"))
	}
	d.nav, d.phase = newNavigator(d.world, stand, arriveSlack), dWalk
	return next
}

func (d *Deposit) standCell(p act.Pose) (protocol.BlockPos, bool) {
	best, bestD, found := protocol.BlockPos{}, float32(1e30), false
	const span = 3
	for dx := int32(-span); dx <= span; dx++ {
		for dy := int32(-span); dy <= span; dy++ {
			for dz := int32(-span); dz <= span; dz++ {
				c := protocol.BlockPos{d.chest.X() + dx, d.chest.Y() + dy, d.chest.Z() + dz}
				if centre(d.chest).Sub(standEye(c)).Len() > chestRange-arriveSlack || !path.Standable(d.world, c) {
					continue
				}
				if dist := centre(c).Sub(p.Feet).LenSqr(); dist < bestD {
					best, bestD, found = c, dist, true
				}
			}
		}
	}
	return best, found
}

func (d *Deposit) walk(a Actuator) outcome {
	st, err := d.nav.step(a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		d.phase = dOpen
		return next
	}
	return d.failed(a, fmt.Errorf("getting to the chest: %w", err))
}

func (d *Deposit) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return d.failed(a, err)
	}
	return outcome{st: Running}
}

func (d *Deposit) open(a Actuator, snap belief.Snapshot) outcome {
	if !d.opened {
		held, ok := holdable(snap, d.item)
		if !ok {
			return d.failed(a, errors.New("nothing on the hotbar to hold"))
		}
		hash, known := d.world.BlockHash(d.chest)
		if !known {
			return d.failed(a, errors.New("the chest's block is not known"))
		}
		p := a.Pose()
		if err := a.SelectHotbar(held.Slot, held); err != nil {
			return d.failed(a, err)
		}
		if err := a.OpenChest(d.chest, faceToward(d.chest, eyeOf(p.Feet)), held.Slot, held, hash); err != nil {
			return d.failed(a, err)
		}
		d.opened, d.waited = true, 0
		return d.idle(a)
	}
	if open, _, pos := a.Container(); open && pos == d.chest && len(a.ContainerContent()) >= chestSlots {
		table := a.ItemTable()
		id, ok := table[d.item]
		if !ok {
			return d.failed(a, fmt.Errorf("item %q is not in the server's item table", d.item))
		}
		d.itemID = id
		d.names = make(map[int32]string, len(table))
		for n, i := range table {
			d.names[i] = n
		}
		d.phase = dMove
		return next
	}
	if d.waited++; d.waited >= waitTicks {
		return d.failed(a, fmt.Errorf("chest did not open within %d ticks", waitTicks))
	}
	return d.idle(a)
}

// holdable picks the hotbar stack to hold while clicking: the item itself if
// it is there, else anything, since the click quotes the held item.
func holdable(s belief.Snapshot, item string) (belief.Item, bool) {
	var any belief.Item
	found := false
	for _, it := range s.Inventory {
		if it.Slot >= hotbarSlots {
			continue
		}
		if it.Name == item {
			return it, true
		}
		if !found {
			any, found = it, true
		}
	}
	return any, found
}

func (d *Deposit) move(a Actuator, snap belief.Snapshot, have int) outcome {
	if open, _, pos := a.Container(); !open || pos != d.chest {
		return d.failed(a, errors.New("chest closed before the deposit finished"))
	}

	if d.pending {
		status, answered := a.Response(d.reqID)
		if !answered {
			if d.waited++; d.waited >= waitTicks {
				return d.failed(a, fmt.Errorf("no answer to the deposit request within %d ticks", waitTicks))
			}
			return d.idle(a)
		}
		d.pending = false
		switch status {
		case act.StatusOK:
			// What is left is read from belief, which applies the same
			// response, so wait until it has.
			d.settling, d.waited = true, 0
		case act.StatusOccupied:
			d.rejected[d.reqSlot] = true
		case act.StatusBadSource:
			return d.failed(a, errors.New("server rejected the source stack"))
		default:
			return d.failed(a, fmt.Errorf("server rejected the deposit with status %d", status))
		}
	}
	if d.settling {
		if have >= d.before {
			if d.waited++; d.waited >= waitTicks {
				return d.failed(a, fmt.Errorf("inventory did not show the deposit within %d ticks", waitTicks))
			}
			return d.idle(a)
		}
		d.settling = false
	}

	remaining := d.qty - (d.base - have)
	if remaining <= 0 {
		return d.finish(a)
	}
	var from belief.Item
	for _, it := range snap.Inventory {
		if it.Name == d.item {
			from = it
			break
		}
	}
	if from.Count == 0 {
		return d.failed(a, fmt.Errorf("no %s left to deposit", d.item))
	}
	content := a.ContainerContent()
	slot, room, ok := d.slotFor(content)
	if !ok {
		return d.failed(a, errors.New("chest is full"))
	}
	n := min(remaining, from.Count, room)
	id, err := a.Deposit(from, n, byte(slot))
	if err != nil {
		return d.failed(a, err)
	}
	d.pending, d.reqID, d.reqSlot, d.before, d.waited = true, id, slot, have, 0
	return d.idle(a)
}

// slotFor finds the first chest slot that takes more of the item. The server
// never reports a full chest, so fullness is read from the slots: each holds
// another item or a full stack.
func (d *Deposit) slotFor(content []protocol.ItemInstance) (slot, room int, ok bool) {
	for i := 0; i < chestSlots && i < len(content); i++ {
		if d.rejected[i] {
			continue
		}
		s := content[i].Stack
		switch {
		case s.Count == 0 || s.NetworkID == 0:
			return i, fullStack, true
		case s.NetworkID == d.itemID && int(s.Count) < fullStack:
			return i, fullStack - int(s.Count), true
		}
	}
	return 0, 0, false
}

func (d *Deposit) finish(a Actuator) outcome {
	if d.log != nil {
		items := map[string]int{}
		for _, s := range a.ContainerContent() {
			if s.Stack.Count == 0 || s.Stack.NetworkID == 0 {
				continue
			}
			name, ok := d.names[s.Stack.NetworkID]
			if !ok {
				name = fmt.Sprintf("item#%d", s.Stack.NetworkID)
			}
			items[name] += int(s.Stack.Count)
		}
		d.log.Info("chest_contents", map[string]any{"items": items})
	}
	if err := a.CloseContainer(); err != nil {
		return d.failed(a, err)
	}
	return outcome{st: Done}
}
