package main

import (
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

	seatKind = "minecraft:cushion"
)

// seatActor is a seat entity as the server announced it.
type seatActor struct {
	runtimeID uint64
	pos       mgl32.Vec3
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
	last     time.Time
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
}

func newSeater(log *logging.Logger, seat [3]float32) *seater {
	return &seater{log: log, now: time.Now, seat: mgl32.Vec3(seat)}
}

// step tries a mount when the bot is unseated and a retry is due. It is
// called per packet and does nothing between retry times.
func (s *seater) step(w packetWriter, p *pose, l *poseLog, hotbarSlot byte) {
	pos, _, _, vehicle := p.get()
	if vehicle != 0 || s.tries >= seatMaxTries {
		return
	}
	now := s.now()
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
	s.log.Info("seat_found", logging.Fields{"unique_id": bestID, "distance": bestD, "try": s.tries})
	err := w.WritePacket(&packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: best.runtimeID,
			ActionType:            protocol.UseItemOnEntityActionInteract,
			HotBarSlot:            int32(hotbarSlot),
			Position:              pos,
		},
	})
	if err != nil {
		return
	}
	s.log.Info("seat_mount_sent", logging.Fields{"unique_id": bestID, "try": s.tries})
}
