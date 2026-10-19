package belief

import (
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// inventorySize is the main inventory with the hotbar: window 0, slots 0-35.
// Armor, the offhand and containers arrive in other windows and are not kept.
const inventorySize = 36

// Item is one non-empty inventory stack.
type Item struct {
	Slot int    `json:"slot"`
	Name string `json:"name"` // no minecraft: prefix
	// Count is a belief like the rest: a pickup the server never announced
	// leaves it low.
	Count int `json:"count"`
	// StackNetworkID is what a later item request must quote for the stack.
	StackNetworkID int32 `json:"stack_network_id"`
}

type stackRef struct {
	itemID  int32
	count   int
	stackID int32
}

func (s stackRef) empty() bool { return s.itemID == 0 || s.count == 0 }

func refOf(it protocol.ItemInstance) stackRef {
	return stackRef{itemID: it.Stack.NetworkID, count: int(it.Stack.Count), stackID: it.StackNetworkID}
}

// ItemNames gives the tracker the server's item table, once per session. Item
// packets carry only runtime IDs, so without it stacks are named by ID.
func (t *Tracker) ItemNames(entries []protocol.ItemEntry) {
	names := make(map[int32]string, len(entries))
	for _, e := range entries {
		names[int32(e.RuntimeID)] = strings.TrimPrefix(e.Name, "minecraft:")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.itemNames = names
}

// applyInventoryLocked reports whether the packet was one the inventory reads.
func (t *Tracker) applyInventoryLocked(pk packet.Packet) bool {
	switch p := pk.(type) {
	case *packet.InventoryContent:
		if p.WindowID != 0 {
			return true
		}
		t.inv = [inventorySize]stackRef{}
		for i := 0; i < len(p.Content) && i < inventorySize; i++ {
			t.inv[i] = refOf(p.Content[i])
		}
	case *packet.InventorySlot:
		if p.WindowID == 0 && p.Slot < inventorySize {
			t.inv[p.Slot] = refOf(p.NewItem)
		}
	case *packet.InventoryTransaction:
		// Pickups arrive as a server transaction, not as slot updates. Its
		// world-sourced actions describe the item entity and are skipped.
		if _, normal := p.TransactionData.(*protocol.NormalTransactionData); !normal && p.TransactionData != nil {
			return true
		}
		for _, a := range p.Actions {
			if w, ok := a.WindowID.Value(); a.SourceType == protocol.InventoryActionSourceContainer && ok && w == 0 && a.InventorySlot < inventorySize {
				t.inv[a.InventorySlot] = refOf(a.NewItem)
			}
		}
	case *packet.ItemStackResponse:
		// A deposit changes the unit's own stack only here; no slot packet
		// follows it.
		for _, r := range p.Responses {
			if r.Status != 0 {
				continue
			}
			for _, c := range r.ContainerInfo {
				if c.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory {
					continue
				}
				for _, s := range c.SlotInfo {
					if int(s.Slot) >= inventorySize {
						continue
					}
					cur := &t.inv[s.Slot]
					switch {
					case s.Count == 0:
						*cur = stackRef{}
					case !cur.empty():
						// The response does not name the item, so a slot
						// belief has lost cannot be rebuilt from it.
						cur.count, cur.stackID = int(s.Count), s.StackNetworkID
					}
				}
			}
		}
	default:
		return false
	}
	return true
}

func (t *Tracker) inventoryLocked() []Item {
	out := make([]Item, 0, inventorySize)
	for slot, s := range t.inv {
		if s.empty() {
			continue
		}
		name, ok := t.itemNames[s.itemID]
		if !ok {
			name = fmt.Sprintf("item#%d", s.itemID)
		}
		out = append(out, Item{Slot: slot, Name: name, Count: s.count, StackNetworkID: s.stackID})
	}
	return out
}
