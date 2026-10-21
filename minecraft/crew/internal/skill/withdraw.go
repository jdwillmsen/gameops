package skill

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const inventorySlots = 36

// ErrChestChunkNotLoaded is the failure of a withdraw whose target block is
// still unknown after waiting out its chance to arrive, as on a cold join
// where the server has not sent the chunk the chest sits in.
var ErrChestChunkNotLoaded = errors.New("the chest's chunk is not loaded")

// ErrChestNotOpened is the failure of a chest open whose click on a known,
// reachable chest brought no ContainerOpen within its answer window, as on a
// cold join while the unit is still settling into the world. Both a withdraw
// and a deposit open a chest, so both time out into it.
var ErrChestNotOpened = fmt.Errorf("chest did not open within %d ticks", answerTicks)

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
	// groups ask for items by ranking rather than by name.
	groups []group
	// slots bounds the inventory slots withdrawn items may land in.
	slots int

	phase wPhaseState

	seen    ChestSeen
	hasSeen bool
}

// wPhaseState holds the mutable state, apart from the fixed request.
type wPhaseState struct {
	p       withdrawPhase
	nav     *navigator
	opened  bool
	waited  int
	clicks  int
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
	w := &Withdraw{world: world, self: self, chest: chest, want: map[string]int{}, log: o.log, groups: o.groups, slots: inventorySlots}
	if o.hotbar {
		w.slots = hotbarSlots
	}
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

// Resume starts the visit again, keeping what was taken. A request in flight
// is forgotten, so a stack the server moved after the fight began is counted
// short and may be taken once more, up to what the inventory has room for.
func (w *Withdraw) Resume() {
	ph := &w.phase
	ph.p, ph.nav, ph.opened, ph.waited, ph.clicks = wStart, nil, false, 0, 0
	ph.pending, ph.settling, ph.drain = false, false, nil
}

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
	ph := &w.phase
	name, known := w.world.Block(w.chest)
	if !known {
		// An unknown target is usually a chunk that has not arrived yet, as
		// on a cold join, not a missing chest; it is waited for briefly and
		// only given up once the wait says the chunk is not coming.
		if ph.waited++; ph.waited >= waitTicks {
			return w.failed(a, ErrChestChunkNotLoaded)
		}
		return w.idle(a)
	}
	ph.waited = 0
	if name != "chest" && name != "trapped_chest" {
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
		if err := w.clickOn(a, snap); err != nil {
			return w.failed(a, err)
		}
		ph.opened, ph.waited, ph.clicks = true, 0, 1
		return w.idle(a)
	}
	if open, _, pos := a.Container(); open && pos == w.chest && len(a.ContainerContent()) >= chestSlots {
		if ph.clicks > 1 && w.log != nil {
			w.log.Info("chest_open_clicks", map[string]any{"clicks": ph.clicks})
		}
		table := a.ItemTable()
		ph.names = itemNames(table)
		ph.p = wTake
		return next
	}
	if ph.waited++; ph.waited >= answerTicks {
		if w.log != nil {
			w.log.Info("chest_open_clicks", map[string]any{"clicks": ph.clicks, "opened": false})
		}
		ph.drain = ErrChestNotOpened
		ph.p, ph.waited = wDrain, 0
		return next
	}
	// One click can be swallowed, so the same one is sent again while the
	// window stays shut.
	if ph.clicks < openClicks && ph.waited%redoOpenTicks == 0 {
		if err := w.clickOn(a, snap); err != nil {
			return w.failed(a, err)
		}
		ph.clicks++
	}
	return w.idle(a)
}

func (w *Withdraw) clickOn(a Actuator, snap belief.Snapshot) error {
	held := holdable(snap, "")
	hash, known := w.world.BlockHash(w.chest)
	if !known {
		return errors.New("the chest's block is not known")
	}
	p := a.Pose()
	if err := a.SelectHotbar(held.Slot, held); err != nil {
		return err
	}
	return a.OpenChest(w.chest, faceToward(w.chest, eyeOf(p.Feet)), held.Slot, held, hash)
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
			if ph.waited++; ph.waited >= answerTicks {
				return w.failed(a, fmt.Errorf("no answer to the withdraw request within %d ticks", answerTicks))
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
			if ph.waited++; ph.waited >= answerTicks {
				return w.failed(a, fmt.Errorf("inventory did not show the withdrawal within %d ticks", answerTicks))
			}
			return w.idle(a)
		}
		ph.settling = false
	}

	content := a.ContainerContent()
	ph.full = false
	present := w.present(content)
	for i := 0; i < chestSlots && i < len(content); i++ {
		s := content[i].Stack
		if s.Count == 0 || s.NetworkID == 0 {
			continue
		}
		name, known := ph.names[s.NetworkID]
		left := w.left(name, present)
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

// present lists the items the chest holds right now.
func (w *Withdraw) present(content []protocol.ItemInstance) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < chestSlots && i < len(content); i++ {
		if s := content[i].Stack; s.Count > 0 && s.NetworkID != 0 {
			out[w.phase.names[s.NetworkID]] = true
		}
	}
	return out
}

// left is how many more of an item the visit may take: its own want, plus its
// group's remaining budget unless a better-ranked item is still in the chest.
func (w *Withdraw) left(name string, present map[string]bool) int {
	left := w.want[name] - w.phase.taken[name]
	for _, g := range w.groups {
		rank := slices.Index(g.names, name)
		if rank < 0 {
			continue
		}
		budget := g.n
		for _, m := range g.names {
			budget -= w.phase.taken[m]
		}
		for _, better := range g.names[:rank] {
			if present[better] {
				budget = 0
			}
		}
		left += max(budget, 0)
	}
	return max(left, 0)
}

// destination finds where a withdrawn item can go: onto a stack of the same
// item that has room, else an empty slot. Slots holding anything else are
// left alone, so a tool is never displaced. The returned Item names the stack
// to merge onto, or only the slot when it is empty.
func (w *Withdraw) destination(s belief.Snapshot, item string, rejected map[int]bool) (to belief.Item, room int, ok bool) {
	used := map[int]bool{}
	for _, it := range s.Inventory {
		used[it.Slot] = true
		if it.Name == item && it.Count < fullStack && it.Slot < w.slots && !rejected[it.Slot] {
			return it, fullStack - it.Count, true
		}
	}
	for slot := 0; slot < w.slots; slot++ {
		if !used[slot] && !rejected[slot] {
			return belief.Item{Slot: slot}, fullStack, true
		}
	}
	return belief.Item{}, 0, false
}

// Seen is the chest's contents as they stood when the visit finished.
func (w *Withdraw) Seen() (ChestSeen, bool) { return w.seen, w.hasSeen }

func (w *Withdraw) finish(a Actuator) outcome {
	items := chestItems(a, w.phase.names)
	if w.log != nil {
		w.log.Info("chest_contents", map[string]any{"items": items})
	}
	if err := a.CloseContainer(); err != nil {
		return w.failed(a, err)
	}
	w.seen, w.hasSeen = ChestSeen{Chest: w.chest, Items: items, At: time.Now()}, true
	return outcome{st: Done}
}
