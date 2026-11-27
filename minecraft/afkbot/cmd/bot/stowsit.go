package main

import (
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/afkbot/internal/logging"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	hotbarSize = 9

	// stowEvery and seatEvery keep a server that fights the bot from being
	// answered every tick.
	stowEvery = 10 * time.Second
	seatEvery = 10 * time.Second

	seatMaxTries = 6
	seatRadius   = 1.5
	// seatAimFor is how long the bot looks at the seat before using it, so
	// the server has the new look before the click.
	seatAimFor = 500 * time.Millisecond
	// seatAimHeight is the middle of a cushion above its feet.
	seatAimHeight = 0.125

	seatKind = "minecraft:cushion"
)

// seatActor is a seat entity as the server announced it.
type seatActor struct {
	runtimeID uint64
	pos       mgl32.Vec3
	uniqueID  int64
}

// stower keeps the bot's hands empty without touching any item: it only moves
// the selection to a hotbar slot that holds nothing.
type stower struct {
	log      *logging.Logger
	now      func() time.Time
	occupied [hotbarSize]bool
	known    bool
	selected byte
	pending  bool
	// blocked is set when a stow found no empty slot, so a slot that frees up
	// later can try again.
	blocked bool
	last    time.Time
}

// selectedEmpty reports whether the bot is known to hold nothing, which is
// what a client's interact transaction has to describe.
func (s *stower) selectedEmpty() bool {
	return s.known && int(s.selected) < hotbarSize && !s.occupied[s.selected]
}

func newStower(log *logging.Logger) *stower {
	return &stower{log: log, now: time.Now, pending: true}
}

// handle follows the bot's own inventory and sends an equip when one is due.
func (s *stower) handle(pk packet.Packet, w packetWriter, runtimeID uint64) {
	switch m := pk.(type) {
	case *packet.InventoryContent:
		if m.WindowID == protocol.WindowIDInventory {
			for i := 0; i < hotbarSize; i++ {
				s.occupied[i] = i < len(m.Content) && m.Content[i].Stack.ItemType.NetworkID != 0
			}
			s.known = true
		}
	case *packet.InventorySlot:
		if m.WindowID == protocol.WindowIDInventory && m.Slot < hotbarSize {
			s.occupied[m.Slot] = m.NewItem.Stack.ItemType.NetworkID != 0
			if s.blocked && !s.occupied[m.Slot] {
				s.blocked, s.pending = false, true
			}
		}
	case *packet.MobEquipment:
		if m.EntityRuntimeID != runtimeID || m.WindowID != protocol.WindowIDInventory {
			return
		}
		s.selected = m.HotBarSlot
		if m.NewItem.Stack.ItemType.NetworkID != 0 {
			s.pending = true
		}
	}
	s.maybeStow(w, runtimeID)
}

func (s *stower) maybeStow(w packetWriter, runtimeID uint64) {
	if !s.pending || !s.known {
		return
	}
	if now := s.now(); !s.last.IsZero() && now.Sub(s.last) < stowEvery {
		return
	}
	s.last = s.now()
	s.pending = false
	slot := -1
	for i, used := range s.occupied {
		if !used {
			slot = i
			break
		}
	}
	if slot < 0 {
		s.blocked = true
		s.log.Warn("stow_no_empty_slot", logging.Fields{"reason": "every hotbar slot holds an item; nothing was changed"})
		return
	}
	if err := w.WritePacket(&packet.MobEquipment{
		EntityRuntimeID: runtimeID,
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
		WindowID:        protocol.WindowIDInventory,
	}); err != nil {
		s.pending = true
		return
	}
	s.selected = byte(slot)
	s.log.Info("stow_sent", logging.Fields{"slot": slot})
}

// seater mounts the seat entity near a configured point the way a client does.
type seater struct {
	log   *logging.Logger
	now   func() time.Time
	seat  mgl32.Vec3
	tries int
	last  time.Time
	// aiming is the seat being looked at before the click, from aimedAt.
	aiming  *seatActor
	aimedAt time.Time
}

func newSeater(log *logging.Logger, seat [3]float32) *seater {
	return &seater{log: log, now: time.Now, seat: mgl32.Vec3(seat)}
}

// step tries a mount when the bot is unseated and a retry is due. It is
// called per packet and does nothing between retry times.
func (s *seater) step(w packetWriter, p *pose, l *poseLog, runtimeID uint64, hotbarSlot byte, heldEmpty bool) {
	pos, _, paused, vehicle := p.get()
	if vehicle != 0 || paused || !heldEmpty {
		s.stopAim(p)
	}
	if vehicle != 0 {
		// Seated: a later dismount or respawn gets a fresh set of tries.
		s.tries = 0
		return
	}
	// A dead bot cannot interact, and the transaction must describe the
	// selected slot, which is only known to be empty after a stow.
	if paused || !heldEmpty {
		return
	}
	now := s.now()
	if s.aiming != nil {
		if now.Sub(s.aimedAt) >= seatAimFor {
			s.click(w, p, *s.aiming, pos, runtimeID, hotbarSlot)
		}
		return
	}
	if s.tries >= seatMaxTries {
		return
	}
	if !s.last.IsZero() && now.Sub(s.last) < seatEvery {
		return
	}
	s.last = now
	s.tries++

	var (
		best   seatActor
		bestID int64
		bestD  float32
		found  bool
	)
	for id, a := range l.seats {
		d := a.pos.Sub(s.seat).Len()
		if d <= seatRadius && (!found || d < bestD || (d == bestD && id < bestID)) {
			best, bestID, bestD, found = a, id, d, true
		}
	}
	if !found {
		s.log.Warn("seat_missing", logging.Fields{"try": s.tries, "kind": seatKind})
		return
	}
	look := lookAt(pos, best.pos.Add(mgl32.Vec3{0, seatAimHeight, 0}))
	s.log.Info("seat_found", logging.Fields{"unique_id": bestID, "distance": bestD, "try": s.tries, "pitch": look[0], "yaw": look[1]})
	best.uniqueID = bestID
	s.aiming, s.aimedAt = &best, now
	p.setAim(&look)
}

func (s *seater) stopAim(p *pose) {
	if s.aiming != nil {
		s.aiming = nil
		p.setAim(nil)
	}
}

// click uses the seat the bot is looking at like a client's right click,
// then gives the held look back.
func (s *seater) click(w packetWriter, p *pose, seat seatActor, pos mgl32.Vec3, runtimeID uint64, hotbarSlot byte) {
	defer s.stopAim(p)
	err := w.WritePacket(&packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: seat.runtimeID,
			ActionType:            protocol.UseItemOnEntityActionInteract,
			HotBarSlot:            int32(hotbarSlot),
			Position:              pos,
			ClickedPosition:       mgl32.Vec3{0, 1, 0},
		},
	})
	if err != nil {
		return
	}
	if err := w.WritePacket(&packet.Animate{ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: runtimeID}); err != nil {
		return
	}
	s.log.Info("seat_mount_sent", logging.Fields{"unique_id": seat.uniqueID, "try": s.tries})
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
