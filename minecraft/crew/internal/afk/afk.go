// Package afk is what an idle unit does instead of living: hold a facing,
// keep its hands empty and sit on a cushion, as the AFK bots on FWB did
// before crew replaced them (docs/idle-mode.md).
//
// Its Reflex never fights, swims or tends. That is a guarantee of the type,
// where the same unit under life would be a set of flags someone can get
// wrong.
package afk

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	hotbarSize = 9

	// stowEvery and seatEvery keep a server that fights the unit from being
	// answered every tick.
	stowEvery = 10 * time.Second
	seatEvery = 10 * time.Second

	seatMaxTries = 6
	seatRadius   = 1.5
	// seatAimFor is how long the unit looks at the seat before using it, so
	// the server has the new look before the click.
	seatAimFor = 500 * time.Millisecond
	// seatAimHeight is the middle of a cushion above its feet.
	seatAimHeight = 0.125
	// eyeHeight lifts the pose's feet to the eye the look is cast from.
	// afkbot aimed from the server's position, which is already the eye;
	// crew's pose is the feet, and a straight port would aim 1.6 blocks low.
	eyeHeight = 1.62

	seatKind = "minecraft:cushion"

	// maxKinds bounds the actor tables the logs read, so a busy world cannot
	// grow them without limit.
	maxKinds = 4096
	// logEvery is how often one kind of repeated event is logged, with how
	// many were held back: a server that fights the seat can relink every
	// tick.
	logEvery = 2 * time.Second
)

// Logger is the part of crew's logger the reflex writes to.
type Logger interface {
	Info(event string, fields map[string]any)
	Warn(event string, fields map[string]any)
}

// Config is what the unit holds. Without HasFacing it keeps the rotation the
// server gave it; without HasSeat it stands.
type Config struct {
	Facing    float32
	HasFacing bool
	Stow      bool
	Seat      [3]float32
	HasSeat   bool
}

// Entities walks the entity table; life.Life.EachEntity is one.
type Entities func(func(belief.Entity))

// rider is the actuator's account of the input it last sent seated.
type rider interface{ Riding() int64 }

type seatTarget struct {
	runtimeID uint64
	uniqueID  int64
	pos       mgl32.Vec3
}

// Reflex implements task.Reflex for an idle unit. Apply runs on the read
// loop and the rest on the runner's goroutine; what both touch sits under mu.
type Reflex struct {
	cfg  Config
	log  Logger
	now  func() time.Time
	self func() belief.Snapshot
	ents Entities

	mu sync.Mutex
	// The stow's view of the hotbar, from the server's packets.
	occupied [hotbarSize]bool
	known    bool
	selected byte
	pending  bool
	// blocked is set when a stow found no empty slot, so a slot that frees up
	// later can try again.
	blocked  bool
	stowLast time.Time

	// The runner's goroutine alone touches the seat.
	tries    int
	seatLast time.Time
	// aiming is the seat being looked at before the click, from aimedAt and
	// aimFrom, the feet the look was worked out from.
	aiming  *seatTarget
	aimedAt time.Time
	aimFrom mgl32.Vec3
	look    mgl32.Vec2

	// The read loop alone touches the rest.
	runtimeID uint64
	uniqueID  int64
	kinds     map[int64]string
	seats     map[int64]bool
	last      map[string]time.Time
	held      map[string]int
}

func New(cfg Config, self func() belief.Snapshot, ents Entities, log Logger) *Reflex {
	return &Reflex{
		cfg: cfg, log: log, now: time.Now, self: self, ents: ents,
		pending: true,
		kinds:   map[int64]string{}, seats: map[int64]bool{},
		last: map[string]time.Time{}, held: map[string]int{},
	}
}

// Begin starts a session. Like Apply it is for the read loop.
func (r *Reflex) Begin(runtimeID uint64, uniqueID int64) {
	r.runtimeID, r.uniqueID = runtimeID, uniqueID
}

// End forgets the session's hotbar and actors: the next session's server
// sends both again.
func (r *Reflex) End() {
	r.mu.Lock()
	r.occupied, r.known, r.selected, r.pending, r.blocked, r.stowLast = [hotbarSize]bool{}, false, 0, true, false, time.Time{}
	r.mu.Unlock()
	clear(r.kinds)
	clear(r.seats)
	clear(r.last)
	clear(r.held)
}

// Reset gives the seat and the stow fresh tries, as after a death. The
// hotbar's contents are the server's and stay known.
func (r *Reflex) Reset() {
	r.tries, r.seatLast, r.aiming = 0, time.Time{}, nil
	r.mu.Lock()
	r.pending, r.blocked, r.stowLast = true, false, time.Time{}
	r.mu.Unlock()
}

func (r *Reflex) Engaged(act.Pose, belief.Snapshot) bool         { return false }
func (r *Reflex) Swimming(act.Pose) bool                         { return false }
func (r *Reflex) Tending(belief.Snapshot, bool) bool             { return false }
func (r *Reflex) Cancel()                                        {}
func (r *Reflex) Swim(_ context.Context, a skill.Actuator) error { return still(a) }
func (r *Reflex) Fight(_ context.Context, a skill.Actuator, _ belief.Snapshot) error {
	return still(a)
}
func (r *Reflex) Tend(_ context.Context, a skill.Actuator, _ belief.Snapshot) error {
	return still(a)
}

// still is the one input the runner's contract asks for, should it ever call
// what the reflex says it never needs.
func still(a skill.Actuator) error {
	p := a.Pose()
	return a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch})
}

// Idle stows, seats and sends the tick's one input, looking along the facing
// or, while a seat is being aimed at, at the seat.
func (r *Reflex) Idle(_ context.Context, a skill.Actuator) error {
	if r.cfg.Stow {
		r.stow(a)
	}
	if r.cfg.HasSeat {
		r.seat(a)
	}
	p := a.Pose()
	in := act.Input{Yaw: p.Yaw, Pitch: p.Pitch}
	if r.cfg.HasFacing {
		in.Yaw = r.cfg.Facing
	}
	if r.aiming != nil {
		in.Pitch, in.Yaw = r.look[0], r.look[1]
	}
	return a.Tick(in)
}

// Apply follows the unit's hotbar and the actors the logs name. Only the read
// loop calls it.
func (r *Reflex) Apply(pk packet.Packet) {
	switch m := pk.(type) {
	case *packet.InventoryContent:
		if m.WindowID != protocol.WindowIDInventory {
			return
		}
		r.mu.Lock()
		for i := 0; i < hotbarSize; i++ {
			r.occupied[i] = i < len(m.Content) && m.Content[i].Stack.ItemType.NetworkID != 0
		}
		r.known = true
		r.mu.Unlock()
	case *packet.InventorySlot:
		if m.WindowID != protocol.WindowIDInventory || m.Slot >= hotbarSize {
			return
		}
		r.mu.Lock()
		r.occupied[m.Slot] = m.NewItem.Stack.ItemType.NetworkID != 0
		if r.blocked && !r.occupied[m.Slot] {
			r.blocked, r.pending = false, true
		}
		r.mu.Unlock()
	case *packet.MobEquipment:
		if m.EntityRuntimeID != r.runtimeID || m.WindowID != protocol.WindowIDInventory {
			return
		}
		r.mu.Lock()
		r.selected = m.HotBarSlot
		// The server put something in the unit's hand.
		if m.NewItem.Stack.ItemType.NetworkID != 0 {
			r.pending = true
		}
		r.mu.Unlock()
	case *packet.ChunkRadiusUpdated:
		// The server's answer to the unit's request, and the only evidence
		// the farm radius was granted.
		r.log.Info("chunk_radius_granted", map[string]any{"radius": m.ChunkRadius})
	case *packet.AddActor:
		if len(r.kinds) < maxKinds {
			r.kinds[m.EntityUniqueID] = m.EntityType
		}
		if m.EntityType != seatKind || len(r.seats) >= maxKinds {
			return
		}
		r.seats[m.EntityUniqueID] = true
		// A seat that already has a rider refuses the unit without a word;
		// its links are the only sign.
		for _, link := range m.EntityLinks {
			r.log.Info("seat_link", map[string]any{
				"seat": m.EntityUniqueID, "ridden": link.RiddenEntityUniqueID,
				"rider": link.RiderEntityUniqueID, "type": link.Type, "self": link.RiderEntityUniqueID == r.uniqueID,
			})
		}
	case *packet.RemoveActor:
		delete(r.kinds, m.EntityUniqueID)
		delete(r.seats, m.EntityUniqueID)
	case *packet.SetActorLink:
		l := m.EntityLink
		if r.seats[l.RiddenEntityUniqueID] && l.RiderEntityUniqueID != r.uniqueID {
			r.log.Info("seat_link", map[string]any{
				"seat": l.RiddenEntityUniqueID, "rider": l.RiderEntityUniqueID,
				"type": l.Type, "rider_kind": r.kinds[l.RiderEntityUniqueID],
			})
		}
		// The cutover check reads this: a seated unit cannot see itself sit.
		if l.RiderEntityUniqueID == r.uniqueID {
			r.emit("ride_link", map[string]any{
				"ridden": l.RiddenEntityUniqueID, "type": l.Type,
				"ridden_kind": r.kinds[l.RiddenEntityUniqueID],
			})
		}
	}
}

func (r *Reflex) emit(kind string, f map[string]any) {
	now := r.now()
	if t, ok := r.last[kind]; ok && now.Sub(t) < logEvery {
		r.held[kind]++
		return
	}
	r.last[kind] = now
	f["suppressed"] = r.held[kind]
	r.held[kind] = 0
	r.log.Info(kind, f)
}

// selectedEmpty reports whether the unit is known to hold nothing, which is
// what the seat's interact transaction has to describe. mu must be held.
func (r *Reflex) selectedEmpty() bool {
	return r.known && int(r.selected) < hotbarSize && !r.occupied[r.selected]
}

// stow selects the first empty hotbar slot when one is due. It moves no item,
// only the selection.
func (r *Reflex) stow(a skill.Actuator) {
	r.mu.Lock()
	if !r.pending || !r.known {
		r.mu.Unlock()
		return
	}
	now := r.now()
	if !r.stowLast.IsZero() && now.Sub(r.stowLast) < stowEvery {
		r.mu.Unlock()
		return
	}
	r.stowLast, r.pending = now, false
	slot := -1
	for i, used := range r.occupied {
		if !used {
			slot = i
			break
		}
	}
	if slot < 0 {
		r.blocked = true
		r.mu.Unlock()
		r.log.Warn("stow_no_empty_slot", map[string]any{"reason": "every hotbar slot holds an item; nothing was changed"})
		return
	}
	r.mu.Unlock()
	err := a.SelectHotbar(slot, belief.Item{})
	r.mu.Lock()
	if err != nil {
		r.pending = true
		r.mu.Unlock()
		return
	}
	r.selected = byte(slot)
	r.mu.Unlock()
	r.log.Info("stow_sent", map[string]any{"slot": slot})
}

// seat tries a mount when the unit is unseated and a retry is due, and clicks
// once the look has had time to reach the server.
func (r *Reflex) seat(a skill.Actuator) {
	s := r.self()
	vehicle := s.Vehicle
	if rd, ok := a.(rider); ok && vehicle == 0 {
		vehicle = rd.Riding()
	}
	dead := s.Dead || !s.Spawned
	r.mu.Lock()
	empty, slot := r.selectedEmpty(), int(r.selected)
	r.mu.Unlock()
	if vehicle != 0 || dead || !empty {
		r.aiming = nil
	}
	if vehicle != 0 {
		// Seated: a later dismount or respawn gets a fresh set of tries.
		r.tries = 0
		return
	}
	// A dead unit cannot interact, and the transaction must describe the
	// selected slot, which is only known to be empty after a stow.
	if dead || !empty {
		return
	}
	now := r.now()
	feet := a.Pose().Feet
	if r.aiming != nil {
		if feet != r.aimFrom {
			// Moved mid-aim: the look no longer points at the seat.
			r.aim(*r.aiming, feet, now)
			return
		}
		if now.Sub(r.aimedAt) >= seatAimFor {
			r.click(a, *r.aiming, slot)
		}
		return
	}
	if r.tries >= seatMaxTries {
		return
	}
	if !r.seatLast.IsZero() && now.Sub(r.seatLast) < seatEvery {
		return
	}
	r.seatLast = now
	r.tries++

	want := mgl32.Vec3(r.cfg.Seat)
	var (
		best  seatTarget
		bestD float32
		found bool
	)
	r.ents(func(e belief.Entity) {
		if e.Type != seatKind {
			return
		}
		d := e.Pos.Sub(want).Len()
		if d <= seatRadius && (!found || d < bestD || (d == bestD && e.UniqueID < best.uniqueID)) {
			best, bestD, found = seatTarget{runtimeID: e.ID, uniqueID: e.UniqueID, pos: e.Pos}, d, true
		}
	})
	if !found {
		r.log.Warn("seat_missing", map[string]any{"try": r.tries, "kind": seatKind})
		return
	}
	r.log.Info("seat_found", map[string]any{"unique_id": best.uniqueID, "distance": bestD, "try": r.tries})
	r.aim(best, feet, now)
}

// aim looks from the eye above feet at the seat and starts the wait before
// the click.
func (r *Reflex) aim(seat seatTarget, feet mgl32.Vec3, now time.Time) {
	r.look = lookAt(feet.Add(mgl32.Vec3{0, eyeHeight, 0}), seat.pos.Add(mgl32.Vec3{0, seatAimHeight, 0}))
	r.aiming, r.aimedAt, r.aimFrom = &seat, now, feet
}

// click uses the seat the unit is looking at like a client's right click,
// then gives the held look back.
func (r *Reflex) click(a skill.Actuator, seat seatTarget, slot int) {
	r.aiming = nil
	if err := a.InteractEntity(seat.runtimeID, slot, belief.Item{}); err != nil {
		return
	}
	r.log.Info("seat_mount_sent", map[string]any{"unique_id": seat.uniqueID, "try": r.tries})
}

// lookAt is the {pitch, yaw} that points an eye at target; pitch is positive
// looking down.
func lookAt(eye, target mgl32.Vec3) mgl32.Vec2 {
	d := target.Sub(eye)
	h := math.Hypot(float64(d[0]), float64(d[2]))
	yaw := math.Mod(degrees(math.Atan2(-float64(d[0]), float64(d[2])))+360, 360)
	pitch := -degrees(math.Atan2(float64(d[1]), h))
	return mgl32.Vec2{float32(pitch), float32(yaw)}
}

func degrees(r float64) float64 { return r * 180 / math.Pi }
