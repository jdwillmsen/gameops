package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const inventorySlots = 36

type withdrawPhase int

const (
	wStart withdrawPhase = iota
	wWalk
	wOpen
	wTake
	wDrain
)

// Withdraw moves wanted items from a chest into the inventory.
type Withdraw struct {
	world ChestWorld
	self  func() belief.Snapshot
	chest protocol.BlockPos
	want  map[string]int
	log   Logger

	phase wPhaseState
}

// wPhaseState holds the mutable state, apart from the fixed request.
type wPhaseState struct {
	p       withdrawPhase
	nav     *navigator
	opened  bool
	waited  int
	names   map[int32]string
	taken   map[string]int
	full    bool
	drain   error
	pending bool
	reqID   int32
	// The request in flight: the item, how many, and the destination slot.
	item     string
	n        int
	toSlot   int
	before   int
	settling bool
	rejected map[int]bool
}

// NewWithdrawChest takes up to want[item] of each listed item out of the
// chest at the position, while the inventory has room, and closes it again.
// It only fills empty slots and stacks of the same item, so a tool in the
// inventory is never displaced.
func NewWithdrawChest(world ChestWorld, self func() belief.Snapshot, chest protocol.BlockPos, want map[string]int, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	w := &Withdraw{world: world, self: self, chest: chest, want: map[string]int{}, log: o.log}
	for k, v := range want {
		w.want[k] = v
	}
	w.phase = wPhaseState{taken: map[string]int{}, rejected: map[int]bool{}}
	return w
}

func (w *Withdraw) Name() string { return "withdraw_chest" }

func (w *Withdraw) Ready(s belief.Snapshot) error { return overworld(s) }

// Taken is what has left the chest so far, by item.
func (w *Withdraw) Taken() map[string]int {
	out := make(map[string]int, len(w.phase.taken))
	for k, v := range w.phase.taken {
		out[k] = v
	}
	return out
}

// Full reports that the visit ended because wanted items stayed behind with
// nowhere to put them.
func (w *Withdraw) Full() bool { return w.phase.full }

func (w *Withdraw) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return w.fail(a, err)
	}
	snap := w.self()
	if err := overworld(snap); err != nil {
		return w.fail(a, err)
	}
	for {
		var o outcome
		switch w.phase.p {
		case wStart:
			o = w.start(a)
		case wWalk:
			o = w.walk(a)
		case wOpen:
			o = w.open(a, snap)
		case wTake:
			o = w.take(a, snap)
		case wDrain:
			o = w.drain(a)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

func (w *Withdraw) fail(a Actuator, err error) (Status, error) {
	if open, _, _ := a.Container(); open {
		_ = a.CloseContainer()
	}
	return Failed, err
}

// Stop shuts the chest when the skill is abandoned.
func (w *Withdraw) Stop(a Actuator) { _, _ = w.fail(a, nil) }

func (w *Withdraw) failed(a Actuator, err error) outcome {
	st, err := w.fail(a, err)
	return outcome{st: st, err: err}
}

func (w *Withdraw) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return w.failed(a, err)
	}
	return outcome{st: Running}
}

func (w *Withdraw) start(a Actuator) outcome {
	if name, known := w.world.Block(w.chest); !known || (name != "chest" && name != "trapped_chest") {
		return w.failed(a, errors.New("no chest at the target"))
	}
	p := a.Pose()
	if centre(w.chest).Sub(eyeOf(p.Feet)).Len() <= chestRange {
		w.phase.p = wOpen
		return next
	}
	stand, ok := standCell(w.world, w.chest, p)
	if !ok {
		return w.failed(a, errors.New("no standing spot within reach of the chest"))
	}
	w.phase.nav, w.phase.p = newNavigator(w.world, stand, arriveSlack), wWalk
	return next
}

func (w *Withdraw) walk(a Actuator) outcome {
	st, err := w.phase.nav.step(a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		w.phase.p = wOpen
		return next
	}
	return w.failed(a, fmt.Errorf("getting to the chest: %w", err))
}

func (w *Withdraw) open(a Actuator, snap belief.Snapshot) outcome {
	ph := &w.phase
	if !ph.opened {
		held := holdable(snap, "")
		hash, known := w.world.BlockHash(w.chest)
		if !known {
			return w.failed(a, errors.New("the chest's block is not known"))
		}
		p := a.Pose()
		if err := a.SelectHotbar(held.Slot, held); err != nil {
			return w.failed(a, err)
		}
		if err := a.OpenChest(w.chest, faceToward(w.chest, eyeOf(p.Feet)), held.Slot, held, hash); err != nil {
			return w.failed(a, err)
		}
		ph.opened, ph.waited = true, 0
		return w.idle(a)
	}
	if open, _, pos := a.Container(); open && pos == w.chest && len(a.ContainerContent()) >= chestSlots {
		table := a.ItemTable()
		ph.names = make(map[int32]string, len(table))
		for n, i := range table {
			ph.names[i] = n
		}
		ph.p = wTake
		return next
	}
	if ph.waited++; ph.waited >= waitTicks {
		ph.drain = fmt.Errorf("chest did not open within %d ticks", waitTicks)
		ph.p, ph.waited = wDrain, 0
		return next
	}
	return w.idle(a)
}

// drain waits a few more ticks for a late ContainerOpen so the window is
// closed rather than left open on the server.
func (w *Withdraw) drain(a Actuator) outcome {
	if open, _, _ := a.Container(); open || w.phase.waited >= drainTicks {
		return w.failed(a, w.phase.drain)
	}
	w.phase.waited++
	return w.idle(a)
}

func (w *Withdraw) take(a Actuator, snap belief.Snapshot) outcome {
	ph := &w.phase
	if open, _, pos := a.Container(); !open || pos != w.chest {
		return w.failed(a, errors.New("chest closed before the withdrawal finished"))
	}
	if ph.pending {
		status, answered := a.Response(ph.reqID)
		if !answered {
			if ph.waited++; ph.waited >= waitTicks {
				return w.failed(a, fmt.Errorf("no answer to the withdraw request within %d ticks", waitTicks))
			}
			return w.idle(a)
		}
		ph.pending = false
		switch status {
		case act.StatusOK:
			ph.taken[ph.item] += ph.n
			// The inventory is read from belief, which applies the same
			// response, so wait until it has.
			ph.settling, ph.waited = true, 0
		case act.StatusOccupied:
			ph.rejected[ph.toSlot] = true
		case act.StatusBadSource:
			return w.failed(a, errors.New("server rejected the source stack"))
		default:
			return w.failed(a, fmt.Errorf("server rejected the withdrawal with status %d", status))
		}
	}
	if ph.settling {
		if count(snap, ph.item) < ph.before+ph.n {
			if ph.waited++; ph.waited >= waitTicks {
				return w.failed(a, fmt.Errorf("inventory did not show the withdrawal within %d ticks", waitTicks))
			}
			return w.idle(a)
		}
		ph.settling = false
	}

	content := a.ContainerContent()
	ph.full = false
	for i := 0; i < chestSlots && i < len(content); i++ {
		s := content[i].Stack
		if s.Count == 0 || s.NetworkID == 0 {
			continue
		}
		name, known := ph.names[s.NetworkID]
		left := w.want[name] - ph.taken[name]
		if !known || left <= 0 {
			continue
		}
		to, room, ok := w.destination(snap, name, ph.rejected)
		if !ok {
			ph.full = true
			continue
		}
		n := min(left, int(s.Count), room)
		id, err := a.Withdraw(byte(i), n, to, to.Slot)
		if err != nil {
			return w.failed(a, err)
		}
		ph.pending, ph.reqID, ph.item, ph.n, ph.toSlot, ph.before, ph.waited = true, id, name, n, to.Slot, count(snap, name), 0
		return w.idle(a)
	}
	return w.finish(a)
}

// destination finds where a withdrawn item can go: onto a stack of the same
// item that has room, else an empty slot. Slots holding anything else are
// left alone, so a tool is never displaced. The returned Item names the stack
// to merge onto, or only the slot when it is empty.
func (w *Withdraw) destination(s belief.Snapshot, item string, rejected map[int]bool) (to belief.Item, room int, ok bool) {
	used := map[int]bool{}
	for _, it := range s.Inventory {
		used[it.Slot] = true
		if it.Name == item && it.Count < fullStack && !rejected[it.Slot] {
			return it, fullStack - it.Count, true
		}
	}
	for slot := 0; slot < inventorySlots; slot++ {
		if !used[slot] && !rejected[slot] {
			return belief.Item{Slot: slot}, fullStack, true
		}
	}
	return belief.Item{}, 0, false
}

func (w *Withdraw) finish(a Actuator) outcome {
	if w.log != nil {
		items := map[string]int{}
		for _, s := range a.ContainerContent() {
			if s.Stack.Count == 0 || s.Stack.NetworkID == 0 {
				continue
			}
			name, ok := w.phase.names[s.Stack.NetworkID]
			if !ok {
				name = fmt.Sprintf("item#%d", s.Stack.NetworkID)
			}
			items[name] += int(s.Stack.Count)
		}
		w.log.Info("chest_contents", map[string]any{"items": items})
	}
	if err := a.CloseContainer(); err != nil {
		return w.failed(a, err)
	}
	return outcome{st: Done}
}
