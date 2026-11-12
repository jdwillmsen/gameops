package act

import (
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// armourSlots are the worn slots a stack request names, in the order the
// server lists them: helmet, chest, legs, boots.
const armourSlots = 4

// Equip moves one inventory stack onto an empty armour slot, returning the
// request ID to poll with Response. A stack request is the only way a client
// moves a worn piece: the server answers it and sends no slot update, which
// is why belief reads the answer.
func (a *Actuator) Equip(from belief.Item, armourSlot int) (requestID int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if armourSlot < 0 || armourSlot >= armourSlots {
		return 0, fmt.Errorf("slot %d is not an armour slot", armourSlot)
	}
	if from.Slot < 0 || from.Slot >= inventorySlots || from.Count < 1 {
		return 0, fmt.Errorf("slot %d holds no piece to wear", from.Slot)
	}

	place := &protocol.PlaceStackRequestAction{}
	place.Count = 1
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(from.Slot),
		StackNetworkID: from.StackNetworkID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerArmor},
		Slot:      byte(armourSlot),
	}
	id := a.nextRequest
	err = a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   []protocol.StackRequestAction{place},
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
