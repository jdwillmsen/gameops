package life

import (
	"context"
	"errors"
	"math"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/inventory"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	// A trip is a walk and a few chest visits; one that takes a minute is stuck.
	invTripLimit = 1200
	// A trip a fight cut short is tried again soon.
	invAbortBackoff = 200
	// A trip that failed is left alone, doubling with each failure in a row.
	invFailBackoff = 3600
	invMaxBackoff  = 18000
	// After a trip that left the inventory still full (the chest is full, or
	// there was little to store) the next waits this long.
	invFullBackoff = 1200
	// A drop request the server never answers is given up after this.
	invAnswerTicks = 100
	// Drops of one item this close together are one cycle: the stacks of a
	// move go out back to back.
	invDropCycleGap = 60
	// A drop of an item that follows an earlier cycle this soon means the unit
	// picked its own litter up again.
	invRepeatWindow = 1200
	invDropBackoff  = 100
	// An item picked up again this many times is no longer dropped: it goes
	// to the chest, or stays if there is none.
	invRepeatLimit = 2
	// Farther than this from the chest a unit stays where it is, as for the
	// kit chest.
	invReach = 96
)

// Inventory switches the inventory manager on: junk and surplus filler are
// dropped, and when the inventory is nearly full the surplus is stored in a
// chest, keeping the unit's tools, weapon, armour, food and building stock.
type Inventory struct {
	Policy inventory.Policy
	// Chest is where surplus goes; nil leaves a unit to drop junk only.
	Chest *protocol.BlockPos
	World skill.ChestWorld
	// Reach is how far from the chest, in blocks, a unit still sets out for
	// it; zero is invReach.
	Reach float64
}

// itemDropper is what an actuator offers to throw a stack out. Kept apart
// from skill.Actuator so the many fakes of that interface need not grow it.
type itemDropper interface {
	Drop(from belief.Item, count int) (int32, error)
}

type invAct uint8

const (
	invNone invAct = iota
	invDrop
	invDeposit
)

type invState struct {
	// next is what Tending chose for the tick Tend is about to be given.
	next invAct
	move inventory.Move

	// A drop request waits for the server's answer.
	pending bool
	req     int32
	reqName string
	reqN    int
	reqAt   uint64

	// A trip is the queue of deposits and the one under way.
	queue     []inventory.Move
	visit     skill.Skill
	current   inventory.Move
	startedAt uint64
	// blockedUntil is the tick before which no trip starts.
	blockedUntil uint64
	fails        int

	dropAt      map[string]uint64
	dropRepeats map[string]int
	dropBlock   map[string]uint64
	// dropHold is a hard stop: a drop the server refused, or an actuator that
	// cannot drop. dropBlock only spaces out repeats and lets a cycle finish.
	dropHold map[string]uint64
}

// busy says a drop awaits its answer or a trip is under way, which hold a
// meal back and the tick for themselves.
func (s *invState) busy() bool { return s.pending || s.visit != nil || len(s.queue) > 0 }

// invWants says the unit has something to drop or store. Dropping is a single
// request and happens at once, task or not; a trip pauses a running task, as a
// full inventory would stop it anyway.
func (l *Life) invWants(s belief.Snapshot) bool {
	c := l.cfg.Inventory
	if c == nil || !s.Spawned || s.Dead || s.Creative {
		return false
	}
	st := &l.inv
	if st.busy() {
		return true
	}
	plan := c.Policy.Plan(s.Inventory)
	plan = l.routeLitter(plan)
	if m, ok := l.nextDrop(plan); ok {
		st.next, st.move = invDrop, m
		return true
	}
	if c.Chest != nil && len(plan.Deposits) > 0 && plan.Full(c.Policy) && l.tick >= st.blockedUntil && l.invInReach(s) {
		st.next = invDeposit
		return true
	}
	return false
}

// routeLitter sends an item the unit keeps picking up again to the chest
// instead of the ground, or, with no chest, leaves it be. It goes first in
// the trip: clutter the unit cannot shake off is what fills the pack.
func (l *Life) routeLitter(plan inventory.Plan) inventory.Plan {
	st := &l.inv
	var drops []inventory.Move
	for _, m := range plan.Drops {
		if st.dropRepeats[m.Name] < invRepeatLimit || l.tick-st.dropAt[m.Name] > invRepeatWindow {
			drops = append(drops, m)
			continue
		}
		if l.cfg.Inventory.Chest != nil {
			plan.Deposits = append([]inventory.Move{m}, plan.Deposits...)
			if plan.Freed != nil {
				plan.Freed[m.Name] = 1
			}
		}
	}
	plan.Drops = drops
	return plan
}

func (l *Life) nextDrop(plan inventory.Plan) (inventory.Move, bool) {
	st := &l.inv
	for _, m := range plan.Drops {
		if l.tick < st.dropHold[m.Name] || (l.tick < st.dropBlock[m.Name] && l.tick-st.dropAt[m.Name] > invDropCycleGap) {
			continue
		}
		return m, true
	}
	return inventory.Move{}, false
}

func (l *Life) invInReach(s belief.Snapshot) bool {
	chest := *l.cfg.Inventory.Chest
	dx, dz := float64(s.Position[0])-float64(chest.X()), float64(s.Position[2])-float64(chest.Z())
	reach := l.cfg.Inventory.Reach
	if reach <= 0 {
		reach = invReach
	}
	return math.Hypot(dx, dz) <= min(reach, kitMaxReach)
}

// tendInventory sends exactly one input for a tick Tending claimed for the
// inventory.
func (l *Life) tendInventory(ctx context.Context, a skill.Actuator, s belief.Snapshot) error {
	st := &l.inv
	if st.dropAt == nil {
		st.dropAt, st.dropRepeats, st.dropBlock, st.dropHold = map[string]uint64{}, map[string]int{}, map[string]uint64{}, map[string]uint64{}
	}
	next := st.next
	st.next = invNone
	switch {
	case st.pending:
		return l.awaitDrop(a)
	case st.visit != nil || len(st.queue) > 0:
		return l.stepTrip(ctx, a, s)
	case next == invDrop:
		return l.dropStack(a, s)
	case next == invDeposit:
		st.queue = l.cfg.Inventory.Policy.Trip(l.routeLitter(l.cfg.Inventory.Policy.Plan(s.Inventory)))
		st.startedAt = l.tick
		return l.stepTrip(ctx, a, s)
	}
	return l.still(a)
}

func (l *Life) dropStack(a skill.Actuator, s belief.Snapshot) error {
	st := &l.inv
	m := st.move
	takes := inventory.Stacks(s.Inventory, m)
	if len(takes) == 0 {
		return l.still(a)
	}
	d, ok := a.(itemDropper)
	if !ok {
		st.dropHold[m.Name] = l.tick + invMaxBackoff
		l.logEvent("inv_drop_failed", map[string]any{"item": m.Name, "error": "actuator cannot drop"})
		return l.still(a)
	}
	take := takes[0]
	id, err := d.Drop(take.Item, take.Count)
	if err != nil {
		st.dropHold[m.Name] = l.tick + invFailBackoff
		l.logEvent("inv_drop_failed", map[string]any{"item": m.Name, "error": err.Error()})
		return l.still(a)
	}
	l.noteDrop(m.Name)
	st.pending, st.req, st.reqName, st.reqN, st.reqAt = true, id, m.Name, take.Count, l.tick
	return l.still(a)
}

// noteDrop counts a drop of an item that comes after an earlier cycle of the
// same item, and backs off the next one.
func (l *Life) noteDrop(name string) {
	st := &l.inv
	last, seen := st.dropAt[name]
	gap := l.tick - last
	switch {
	case !seen || gap > invRepeatWindow:
		st.dropRepeats[name] = 0
	case gap > invDropCycleGap:
		st.dropRepeats[name]++
		st.dropBlock[name] = l.tick + uint64(min(invDropBackoff<<st.dropRepeats[name], invMaxBackoff))
	}
	st.dropAt[name] = l.tick
}

func (l *Life) awaitDrop(a skill.Actuator) error {
	st := &l.inv
	status, done := a.Response(st.req)
	switch {
	case done && status == act.StatusOK:
		l.logEvent("inv_drop", map[string]any{"item": st.reqName, "count": st.reqN})
	case done:
		st.dropHold[st.reqName] = l.tick + invFailBackoff
		l.logEvent("inv_drop_failed", map[string]any{"item": st.reqName, "status": int(status)})
	case l.tick-st.reqAt >= invAnswerTicks:
		st.dropHold[st.reqName] = l.tick + invFailBackoff
		l.logEvent("inv_drop_failed", map[string]any{"item": st.reqName, "error": "no answer"})
	default:
		return l.still(a)
	}
	st.pending = false
	return l.still(a)
}

// stepTrip advances the deposits by one tick. A deposit that ends without
// sending an input lets the next start in the same tick, so the tick still
// carries exactly one.
func (l *Life) stepTrip(ctx context.Context, a skill.Actuator, s belief.Snapshot) error {
	st := &l.inv
	for {
		if st.visit == nil {
			if len(st.queue) == 0 {
				l.endTrip(nil)
				return l.still(a)
			}
			m := st.queue[0]
			st.queue = st.queue[1:]
			sk := l.newDeposit(m)
			if sk.Ready(s) != nil {
				// What was planned is no longer carried; skip it.
				continue
			}
			st.visit, st.current = sk, m
		}
		if l.tick-st.startedAt > invTripLimit {
			l.abortTrip(a)
			l.endTrip(errors.New("trip took too long"))
			return l.still(a)
		}
		before := a.Ticks()
		status, err := st.visit.Step(ctx, a)
		if status == skill.Running {
			return nil
		}
		if status == skill.Failed && err == nil {
			err = errors.New("deposit failed")
		}
		st.visit = nil
		if err != nil {
			st.queue = nil
			l.endTrip(err)
		} else {
			l.logEvent("inv_deposit", map[string]any{"item": st.current.Name, "count": st.current.Count})
			if len(st.queue) == 0 {
				l.endTrip(nil)
			}
		}
		switch {
		case a.Ticks() != before:
			return nil
		case !st.busy():
			return l.still(a)
		}
	}
}

func (l *Life) abortTrip(a skill.Actuator) {
	st := &l.inv
	if st.visit != nil {
		if sp, ok := st.visit.(skill.Stopper); ok {
			sp.Stop(a)
		}
	}
	st.visit, st.queue = nil, nil
}

// endTrip closes a trip and sets when the next may start.
func (l *Life) endTrip(err error) {
	st := &l.inv
	if err != nil {
		l.logEvent("inv_trip_failed", map[string]any{"error": err.Error()})
		st.blockedUntil = l.tick + uint64(min(invFailBackoff<<st.fails, invMaxBackoff))
		st.fails = min(st.fails+1, 4)
		return
	}
	st.fails = 0
	// The skill waited for its deposits to show in belief, so a fresh snapshot
	// already counts them.
	if now := l.self(); l.cfg.Inventory.Policy.Plan(now.Inventory).Full(l.cfg.Inventory.Policy) {
		l.logEvent("inv_still_full", nil)
		st.blockedUntil = l.tick + invFullBackoff
	}
}

// stopInventory abandons a trip a fight interrupts, closing the chest. It
// sends no input, which the fight's own tick does.
func (l *Life) stopInventory(a skill.Actuator) {
	st := &l.inv
	if st.visit == nil && len(st.queue) == 0 {
		return
	}
	l.abortTrip(a)
	st.blockedUntil = l.tick + invAbortBackoff
}

func (l *Life) newDeposit(m inventory.Move) skill.Skill {
	if l.makeDeposit != nil {
		return l.makeDeposit(m)
	}
	var opts []skill.Option
	if l.log != nil {
		opts = append(opts, skill.WithLogger(l.log))
	}
	c := l.cfg.Inventory
	return skill.NewDepositChest(c.World, l.self, *c.Chest, m.Name, m.Count, opts...)
}
