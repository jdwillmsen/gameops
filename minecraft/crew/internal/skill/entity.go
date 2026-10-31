package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// entityReach is how near the feet must be, horizontally, to an entity
	// before the unit acts on it; inside the server's own reach for either
	// a hit or an interaction.
	entityReach = 2.6
	// entityStand is how close the walk brings the unit, a margin inside the
	// reach so the target's drift does not undo the arrival.
	entityStand = 2.0
	// entityReplan is the ticks between replans while chasing a moving target.
	entityReplan = 10
	// entityFindTicks is how long a target may be missing before the task fails.
	entityFindTicks = 60
	// entityBody is the height of the body's centre above the feet, which the
	// unit looks at.
	entityBody = 0.9
)

// EntityRef picks the entity to act on: by runtime ID, or the nearest of a
// type and, when set, a name (a player's username or a name tag).
type EntityRef struct {
	ID   uint64
	Type string
	Name string
}

// EntityKind is what is done to the entity.
type EntityKind int

const (
	// Interact is a right click: feeding, taming, toggling a pet's sit.
	Interact EntityKind = iota
	Hit
)

func (k EntityKind) String() string {
	if k == Hit {
		return "attack_entity"
	}
	return "interact_entity"
}

// EntityUse walks to an entity, holds an item (an empty hand when item is
// empty) and acts on it a number of times.
type EntityUse struct {
	world path.Blocks
	self  func() belief.Snapshot
	each  func(func(belief.Entity))
	log   Logger

	ref   EntityRef
	kind  EntityKind
	item  string
	times int
	every int

	walk     *navigator
	walkGoal [2]int32
	walkAt   int
	tick     int
	missing  int
	selected bool
	sent     int
	nextSend int
	done     bool
}

// NewEntityUse acts on the entity times times, every ticks apart. It needs the
// entity source of a Guard.
func NewEntityUse(world path.Blocks, self func() belief.Snapshot, ref EntityRef, kind EntityKind, item string, times, every int, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	u := &EntityUse{world: world, self: self, log: o.log, ref: ref, kind: kind, item: item, times: times, every: every}
	if o.guard != nil {
		u.each = o.guard.Each
	}
	return u
}

func (u *EntityUse) Name() string { return u.kind.String() }

func (u *EntityUse) Progress() int { return u.sent }

func (u *EntityUse) Ready(s belief.Snapshot) error {
	if u.each == nil {
		return errors.New("no entity source")
	}
	return overworld(s)
}

// Resume plans the walk again from where the unit now stands.
func (u *EntityUse) Resume() { u.walk = nil }

func (u *EntityUse) find(from mgl32.Vec3) (belief.Entity, bool) {
	var best belief.Entity
	found, bestD := false, math.MaxFloat64
	u.each(func(e belief.Entity) {
		switch {
		case u.ref.ID != 0:
			if e.ID != u.ref.ID {
				return
			}
		case u.ref.Type != "" && !sameType(e.Type, u.ref.Type):
			return
		}
		if u.ref.Name != "" && !strings.EqualFold(e.Name, u.ref.Name) && !strings.EqualFold(e.CustomName, u.ref.Name) {
			return
		}
		if d := float64(e.Pos.Sub(from).Len()); d < bestD {
			best, bestD, found = e, d, true
		}
	})
	return best, found
}

func sameType(have, want string) bool {
	return have == want || strings.TrimPrefix(have, "minecraft:") == strings.TrimPrefix(want, "minecraft:")
}

func (u *EntityUse) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	s := u.self()
	if err := overworld(s); err != nil {
		return Failed, err
	}
	p := a.Pose()
	u.tick++
	if u.sent >= u.times {
		// Checked first: the last feed eats the item and the last hit may
		// remove the target, and neither makes a finished task a failure.
		if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
			return Failed, err
		}
		return Done, nil
	}
	e, ok := u.find(p.Feet)
	if !ok {
		u.missing++
		if u.missing > entityFindTicks {
			return Failed, errors.New("target not found")
		}
		return u.idle(a, p)
	}
	u.missing = 0
	slot, held, err := u.hand(s)
	if err != nil {
		return Failed, err
	}
	if !u.selected {
		u.selected = true
		if err := a.SelectHotbar(slot, held); err != nil {
			return Failed, err
		}
		u.nextSend = u.tick + 2
		return u.idle(a, p)
	}
	if horizontal(p.Feet, e.Pos) > entityReach {
		return u.approach(a, p, e)
	}
	u.walk = nil
	yaw, pitch := lookAt(eyeOf(p.Feet), e.Pos.Add(mgl32.Vec3{0, entityBody, 0}))
	if err := a.Tick(act.Input{Yaw: yaw, Pitch: pitch}); err != nil {
		return Failed, err
	}
	if u.tick < u.nextSend {
		return Running, nil
	}
	send := a.InteractEntity
	if u.kind == Hit {
		send = a.Attack
	}
	if err := send(e.ID, slot, held); err != nil {
		return Failed, fmt.Errorf("%s: %w", u.kind, err)
	}
	u.sent++
	u.nextSend = u.tick + u.every
	if u.log != nil {
		u.log.Info("entity_use", map[string]any{"kind": u.kind.String(), "type": e.Type, "n": u.sent, "item": u.item})
	}
	return Running, nil
}

func (u *EntityUse) idle(a Actuator, p act.Pose) (Status, error) {
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return Failed, err
	}
	return Running, nil
}

func (u *EntityUse) approach(a Actuator, p act.Pose, e belief.Entity) (Status, error) {
	cell := cellOf(e.Pos)
	goal := [2]int32{cell.X(), cell.Z()}
	if u.walk == nil || (u.walkGoal != goal && u.tick-u.walkAt >= entityReplan) {
		u.walk, u.walkGoal, u.walkAt = newNavigator(u.world, cell, entityStand), goal, u.tick
	}
	st, err := u.walk.step(a)
	if st == Done {
		// Close enough by the navigator's measure but not the entity's:
		// stand still this tick and let the next one re-judge.
		return u.idle(a, p)
	}
	return st, err
}

// hand returns the hotbar slot and stack the action is made with: the named
// item, or a free slot for an empty hand.
func (u *EntityUse) hand(s belief.Snapshot) (int, belief.Item, error) {
	if u.item != "" {
		for _, it := range s.Inventory {
			if it.Name == u.item && it.Slot < 9 {
				return it.Slot, it, nil
			}
		}
		return 0, belief.Item{}, fmt.Errorf("no %s on the hotbar", u.item)
	}
	used := [9]bool{}
	for _, it := range s.Inventory {
		if it.Slot < 9 {
			used[it.Slot] = true
		}
	}
	for i, taken := range used {
		if !taken {
			return i, belief.Item{}, nil
		}
	}
	return 0, belief.Item{}, errors.New("no empty hotbar slot for an empty hand")
}

// Equip holds an item from the hotbar, or an empty hand when item is empty.
type Equip struct {
	use  *EntityUse
	done bool
}

func NewEquip(self func() belief.Snapshot, item string) Skill {
	return &Equip{use: &EntityUse{self: self, item: item}}
}

func (e *Equip) Name() string                  { return "equip_item" }
func (e *Equip) Ready(s belief.Snapshot) error { return overworld(s) }

func (e *Equip) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	slot, held, err := e.use.hand(e.use.self())
	if err != nil {
		return Failed, err
	}
	if !e.done {
		e.done = true
		if err := a.SelectHotbar(slot, held); err != nil {
			return Failed, err
		}
	}
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return Failed, err
	}
	return Done, nil
}

func horizontal(a, b mgl32.Vec3) float64 {
	return math.Hypot(float64(a.X()-b.X()), float64(a.Z()-b.Z()))
}
