package camera

import (
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const hotbarSize = 9

// alwaysGlint lists items the game draws enchanted whatever their NBT.
var alwaysGlint = map[string]bool{"enchanted_golden_apple": true, "enchanted_book": true, "experience_bottle": true}

// ownEquip is the unit's own hotbar, off hand and selection, from which its
// hands are worked out: the server never says which slot the unit holds.
type ownEquip struct {
	hotbar  [hotbarSize]handItem
	offhand handItem
	held    int
	hands   handSlots
	mods    armourMods
}

// enchanted reports whether the stack carries enchantments, which Bedrock
// keeps in an "ench" list of {id, lvl} compounds.
func enchanted(it protocol.ItemStack) bool {
	switch l := it.NBTData["ench"].(type) {
	case []any:
		return len(l) > 0
	case []map[string]any:
		return len(l) > 0
	}
	return false
}

// trimOf reads an armour trim, kept in a "Trim" compound of "Pattern" and
// "Material" strings, with their namespace removed.
func trimOf(it protocol.ItemStack) (pattern, material string) {
	t, ok := it.NBTData["Trim"].(map[string]any)
	if !ok {
		return "", ""
	}
	p, _ := t["Pattern"].(string)
	m, _ := t["Material"].(string)
	return trimNamespace(p), trimNamespace(m)
}

func trimNamespace(s string) string {
	if _, rest, ok := strings.Cut(s, ":"); ok {
		return rest
	}
	return s
}

func (h *Hub) handItemLocked(it protocol.ItemInstance) handItem {
	name := h.armourNameLocked(it)
	if name == "" {
		return handItem{}
	}
	return handItem{Name: name, Glint: enchanted(it.Stack) || alwaysGlint[name]}
}

func (h *Hub) armourModLocked(it protocol.ItemInstance) armourMod {
	if h.armourNameLocked(it) == "" {
		return armourMod{}
	}
	p, m := trimOf(it.Stack)
	return armourMod{Glint: enchanted(it.Stack), Pattern: p, Material: m}
}

func (h *Hub) armourModsLocked(items [4]protocol.ItemInstance) armourMods {
	var out armourMods
	for i, it := range items {
		out[i] = h.armourModLocked(it)
	}
	return out
}

// setHeld records what an entity holds: window is the packet's, the main
// inventory for the main hand and the off-hand window for the other.
func (h *Hub) setHeld(runtimeID uint64, window, slot byte, it protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item := h.handItemLocked(it)
	offhand := window == protocol.WindowIDOffHand
	if h.runtime != 0 && runtimeID == h.runtime {
		if offhand {
			h.own.offhand = item
		} else if slot < hotbarSize {
			h.own.hotbar[slot] = item
			h.own.held = int(slot)
		}
		h.syncOwnHandsLocked()
		return
	}
	e, ok := h.ents[runtimeID]
	if !ok {
		return
	}
	i := 0
	if offhand {
		i = 1
	}
	if e.Hands[i] == item {
		return
	}
	e.Hands[i] = item
	h.pushOrderedLocked(encodeEquipment(map[uint64]handSlots{e.ID: e.Hands}))
}

// setArmourMods records the glint and trim of what another entity wears.
func (h *Hub) setArmourMods(runtimeID uint64, items [4]protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	mods := h.armourModsLocked(items)
	if h.runtime != 0 && runtimeID == h.runtime {
		h.setOwnModsLocked(mods)
		return
	}
	e, ok := h.ents[runtimeID]
	if !ok || e.Mods == mods {
		return
	}
	e.Mods = mods
	h.pushOrderedLocked(encodeArmourExtra(map[uint64]armourMods{e.ID: mods}))
}

func (h *Hub) setOwnMods(content []protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var mods armourMods
	for i := 0; i < len(content) && i < len(mods); i++ {
		mods[i] = h.armourModLocked(content[i])
	}
	h.setOwnModsLocked(mods)
}

func (h *Hub) setOwnModSlot(slot int, it protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if slot < 0 || slot >= len(h.own.mods) {
		return
	}
	mods := h.own.mods
	mods[slot] = h.armourModLocked(it)
	h.setOwnModsLocked(mods)
}

func (h *Hub) setOwnModsLocked(mods armourMods) {
	if h.own.mods == mods {
		return
	}
	h.own.mods = mods
	h.pushOrderedLocked(encodeArmourExtra(map[uint64]armourMods{0: mods}))
}

// setOwnInventory reads the unit's main inventory window, whose first nine
// slots are the hotbar, or its off-hand window, whose only slot is the shield
// slot.
func (h *Hub) setOwnInventory(window byte, content []protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch window {
	case protocol.WindowIDInventory:
		for i := range h.own.hotbar {
			h.own.hotbar[i] = handItem{}
			if i < len(content) {
				h.own.hotbar[i] = h.handItemLocked(content[i])
			}
		}
	case protocol.WindowIDOffHand:
		h.own.offhand = handItem{}
		if len(content) > 0 {
			h.own.offhand = h.handItemLocked(content[0])
		}
	default:
		return
	}
	h.syncOwnHandsLocked()
}

func (h *Hub) setOwnInventorySlot(window byte, slot int, it protocol.ItemInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case window == protocol.WindowIDInventory && slot >= 0 && slot < hotbarSize:
		h.own.hotbar[slot] = h.handItemLocked(it)
	case window == protocol.WindowIDOffHand && slot == 0:
		h.own.offhand = h.handItemLocked(it)
	default:
		return
	}
	h.syncOwnHandsLocked()
}

// SetOwnHeld gives the hotbar slot the unit selected. The server never echoes
// a client's own selection back, so the caller that sends it reports it here.
func (h *Hub) SetOwnHeld(slot int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setOwnHeldLocked(slot)
}

func (h *Hub) setOwnHeldLocked(slot int) {
	if slot < 0 || slot >= hotbarSize {
		return
	}
	h.own.held = slot
	h.syncOwnHandsLocked()
}

func (h *Hub) syncOwnHandsLocked() {
	hands := handSlots{h.own.hotbar[h.own.held], h.own.offhand}
	if hands == h.own.hands {
		return
	}
	h.own.hands = hands
	h.pushOrderedLocked(encodeEquipment(map[uint64]handSlots{0: hands}))
}

// pushOwnEquipLocked replays the unit's own hands and trims after a reset
// has cleared the viewers.
func (h *Hub) pushOwnEquipLocked() {
	if !h.own.hands.zero() {
		h.pushOrderedLocked(encodeEquipment(map[uint64]handSlots{0: h.own.hands}))
	}
	if !h.own.mods.zero() {
		h.pushOrderedLocked(encodeArmourExtra(map[uint64]armourMods{0: h.own.mods}))
	}
}
