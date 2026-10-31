package camera

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
)

const (
	frameActorEvent byte = 23

	// eventAttack is the swing a mob makes at its target.
	eventAttack byte = 1
)

// encodeActorEvent is [23][id uvarint][event u8]. It is not kept for viewers
// that attach later: an attack is over in under a second.
func encodeActorEvent(id uint64, event byte) []byte {
	return append(binary.AppendUvarint([]byte{frameActorEvent}, id), event)
}

// swingRange and swingGap bound the inferred swing: how close an attacker
// stands to what it hurt, and how soon it may swing again.
const (
	swingRange = 3.5
	swingGap   = 350 * time.Millisecond
)

// actorEvent forwards the swing a server announces. BDS does not announce a
// mob's melee swing at all (no ActorEvent, no Animate packet reached the unit
// while a tamed tiger killed a zombie), so a hurt entity also makes the
// nearest pack entity within reach swing: a pack animal next to something that
// just took damage is almost always what struck it. The pose is inferred, not
// received.
func (h *Hub) actorEvent(p *packet.ActorEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch p.EventType {
	case packet.ActorEventStartAttacking:
		if _, ok := h.ents[p.EntityRuntimeID]; ok {
			h.pushOrderedLocked(encodeActorEvent(p.EntityRuntimeID, eventAttack))
		}
	case packet.ActorEventHurt:
		h.inferSwingLocked(p.EntityRuntimeID)
	}
}

func (h *Hub) inferSwingLocked(victim uint64) {
	v, ok := h.ents[victim]
	if !ok {
		return
	}
	var best *entity
	bestD := float32(swingRange * swingRange)
	for _, e := range h.ents {
		if e.ID == victim || strings.HasPrefix(e.Type, "minecraft:") || e.Type == "player" {
			continue
		}
		dx, dz := e.X-v.X, e.Z-v.Z
		if d := dx*dx + dz*dz; d <= bestD {
			best, bestD = e, d
		}
	}
	if best == nil {
		return
	}
	now := time.Now()
	if now.Sub(h.swung[best.ID]) < swingGap {
		return
	}
	if h.swung == nil {
		h.swung = map[uint64]time.Time{}
	}
	h.swung[best.ID] = now
	h.pushOrderedLocked(encodeActorEvent(best.ID, eventAttack))
}

// SetPackEntities makes the models of a pack's own entities available and
// tells every viewer. It needs no atlas, so those entities are drawn even
// where the vanilla assets could not be fetched.
func (h *Hub) SetPackEntities(png []byte, table atlas.EntityTable) error {
	doc, err := json.Marshal(table)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.packPNG, h.packDoc = png, doc
	h.pushOrderedLocked(modelsFrame())
	return nil
}

// packData returns the pack entity sheet, or ok=false while there is none.
func (h *Hub) packData() (png, doc []byte, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.packPNG, h.packDoc, h.packDoc != nil
}
