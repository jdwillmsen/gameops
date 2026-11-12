package act

import (
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// Drop throws count items of an inventory stack onto the ground, returning the
// request ID to poll with Response. The answer names the slot's new count and
// belief reads it from there; no slot update follows.
func (a *Actuator) Drop(from belief.Item, count int) (requestID int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if from.Slot < 0 || from.Slot >= inventorySlots {
		return 0, fmt.Errorf("slot %d is not in the inventory", from.Slot)
	}
	if count < 1 || count > from.Count || count > 255 {
		return 0, fmt.Errorf("cannot drop %d of a stack of %d", count, from.Count)
	}

	drop := &protocol.DropStackRequestAction{}
	drop.Count = byte(count)
	drop.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(from.Slot),
		StackNetworkID: from.StackNetworkID,
	}
	id := a.nextRequest
	err = a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   []protocol.StackRequestAction{drop},
	}}})
	if err != nil {
		return 0, err
	}
	a.nextRequest -= 2
	if item, ok := a.itemIDs[from.Name]; ok {
		a.reqItem[id] = item
	}
	return id, nil
}
