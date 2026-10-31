package camera

import (
	"encoding/binary"
	"encoding/json"

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

func (h *Hub) actorEvent(p *packet.ActorEvent) {
	if p.EventType != packet.ActorEventStartAttacking {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.ents[p.EntityRuntimeID]; ok {
		h.pushOrderedLocked(encodeActorEvent(p.EntityRuntimeID, eventAttack))
	}
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
