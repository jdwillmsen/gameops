package camera

import (
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// maxSkins bounds what one session remembers: the server lists every player
// that ever joins, and a skin is up to a few hundred KB.
const maxSkins = 256

// setSkin keeps a player's listed skin and shows it on that player if they are
// in view. A skin the camera cannot draw leaves the model's default.
func (h *Hub) setSkin(id uuid.UUID, s protocol.Skin) {
	info, ok := skinOf(s)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, known := h.skins[id]; !known && len(h.skins) >= maxSkins {
		return
	}
	h.skins[id] = &info
	for _, e := range h.ents {
		if e.UUID == id {
			e.Skin = &info
			h.pushOrderedLocked(encodeSkin(e.ID, info))
		}
	}
}

// SetOwnSkin gives the unit's own appearance, which the server never sends
// back: the camera draws the unit's body in the third-person modes with it.
func (h *Hub) SetOwnSkin(width, height int, rgba []byte, slim bool) {
	info, ok := skinFromRGBA(width, height, rgba, slim)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ownSkin = &info
	h.pushOrderedLocked(encodeSkin(0, info))
}

// armourName is an item instance's name without namespace, or "" for an empty
// slot or an item the table does not name.
func (h *Hub) armourNameLocked(it protocol.ItemInstance) string {
	if it.Stack.NetworkID == 0 || it.Stack.Count == 0 {
		return ""
	}
	return h.itemName[it.Stack.NetworkID]
}

func (h *Hub) setArmour(runtimeID uint64, helmet, chest, legs, boots protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	worn := armourSlots{h.armourNameLocked(helmet), h.armourNameLocked(chest), h.armourNameLocked(legs), h.armourNameLocked(boots)}
	if h.runtime != 0 && runtimeID == h.runtime {
		h.setOwnArmourLocked(worn)
		return
	}
	e, ok := h.ents[runtimeID]
	if !ok || e.Armor == worn {
		return
	}
	e.Armor = worn
	h.pushOrderedLocked(encodeArmour(map[uint64]armourSlots{e.ID: worn}))
}

// setOwnArmour reads the unit's armour window, which holds helmet,
// chestplate, leggings and boots in its first four slots.
func (h *Hub) setOwnArmour(content []protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var worn armourSlots
	for i := 0; i < len(content) && i < len(worn); i++ {
		worn[i] = h.armourNameLocked(content[i])
	}
	h.setOwnArmourLocked(worn)
}

func (h *Hub) setOwnArmourSlot(slot int, it protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if slot < 0 || slot >= len(h.ownArmor) {
		return
	}
	worn := h.ownArmor
	worn[slot] = h.armourNameLocked(it)
	h.setOwnArmourLocked(worn)
}

func (h *Hub) setOwnArmourLocked(worn armourSlots) {
	if h.ownArmor == worn {
		return
	}
	h.ownArmor = worn
	h.pushOrderedLocked(encodeArmour(map[uint64]armourSlots{0: worn}))
}
