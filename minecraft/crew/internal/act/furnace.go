package act

import (
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// FurnaceSlot is one of the three slots of a furnace window.
type FurnaceSlot int

const (
	FurnaceIngredient FurnaceSlot = iota
	FurnaceFuel
	FurnaceResult
)

// furnaceSlots is the number of slots a furnace window holds.
const furnaceSlots = 3

// furnaceContainer is the container name an item stack request gives a
// furnace slot. The slot number in the request, and the index of the slot in
// the window's content, follow the order of FurnaceSlot.
func furnaceContainer(s FurnaceSlot) (byte, error) {
	switch s {
	case FurnaceIngredient:
		return protocol.ContainerFurnaceIngredient, nil
	case FurnaceFuel:
		return protocol.ContainerFurnaceFuel, nil
	case FurnaceResult:
		return protocol.ContainerFurnaceResult, nil
	}
	return 0, fmt.Errorf("%d is not a furnace slot", int(s))
}

// FurnaceLoad moves count items from an inventory stack into the ingredient or
// fuel slot of the open furnace, returning the request ID to poll with
// Response. The result slot only gives, so loading it is an error.
func (a *Actuator) FurnaceLoad(from belief.Item, count int, slot FurnaceSlot) (requestID int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cont.open {
		return 0, errors.New("no container is open")
	}
	if slot != FurnaceIngredient && slot != FurnaceFuel {
		return 0, errors.New("a furnace takes its ingredient and its fuel only")
	}
	if count < 1 || count > from.Count || count > 255 {
		return 0, fmt.Errorf("cannot move %d of a stack of %d", count, from.Count)
	}
	if from.Slot < 0 || from.Slot >= inventorySlots {
		return 0, fmt.Errorf("slot %d is not in the inventory", from.Slot)
	}
	container, _ := furnaceContainer(slot)
	var destStack int32
	if int(slot) < len(a.cont.slots) {
		destStack = a.cont.slots[slot].StackNetworkID
	}
	place := &protocol.PlaceStackRequestAction{}
	place.Count = byte(count)
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(from.Slot),
		StackNetworkID: from.StackNetworkID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: container},
		Slot:           byte(slot),
		StackNetworkID: destStack,
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

// FurnaceTake moves count items out of a slot of the open furnace into slot
// toSlot of the unit's inventory, returning the request ID to poll with
// Response. to is the stack already in toSlot, or the zero Item when empty.
func (a *Actuator) FurnaceTake(slot FurnaceSlot, count int, to belief.Item, toSlot int) (requestID int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cont.open {
		return 0, errors.New("no container is open")
	}
	container, err := furnaceContainer(slot)
	if err != nil {
		return 0, err
	}
	if int(slot) >= len(a.cont.slots) {
		return 0, fmt.Errorf("slot %d is not in the furnace", int(slot))
	}
	src := a.cont.slots[slot]
	held := int(src.Stack.Count)
	if src.Stack.NetworkID == 0 || held == 0 {
		return 0, fmt.Errorf("furnace slot %d is empty", int(slot))
	}
	if count < 1 || count > held || count > 255 {
		return 0, fmt.Errorf("cannot take %d from a stack of %d", count, held)
	}
	if toSlot < 0 || toSlot >= inventorySlots {
		return 0, fmt.Errorf("slot %d is not in the inventory", toSlot)
	}
	take := &protocol.TakeStackRequestAction{}
	take.Count = byte(count)
	take.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: container},
		Slot:           byte(slot),
		StackNetworkID: src.StackNetworkID,
	}
	take.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(toSlot),
		StackNetworkID: to.StackNetworkID,
	}
	id := a.nextRequest
	err = a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: id,
		Actions:   []protocol.StackRequestAction{take},
	}}})
	if err != nil {
		return 0, err
	}
	a.nextRequest -= 2
	a.reqItem[id] = src.Stack.NetworkID
	return id, nil
}

// FurnaceWindow reports whether the open container is a furnace window whose
// three slots have arrived, the signal that it is safe to load or take.
func (a *Actuator) FurnaceWindow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cont.open && len(a.cont.slots) >= furnaceSlots
}

// furnaceSlotOf names the furnace slot a response's container stands for.
func furnaceSlotOf(container byte) (FurnaceSlot, bool) {
	switch container {
	case protocol.ContainerFurnaceIngredient:
		return FurnaceIngredient, true
	case protocol.ContainerFurnaceFuel:
		return FurnaceFuel, true
	case protocol.ContainerFurnaceResult:
		return FurnaceResult, true
	}
	return 0, false
}

// applyFurnaceLocked folds an accepted answer's furnace slot changes into the
// open window's cache. The server answers a request with the new counts and
// stack IDs and sends no slot update, so without this the next take quotes the
// stack ID from before the last one and is refused. As for the unit's own
// inventory, a slot that held a stack keeps its item, an emptied one is
// cleared, and one the request filled gets the item the request carried.
func (a *Actuator) applyFurnaceLocked(r protocol.ItemStackResponse, item int32) {
	if !a.cont.open || len(a.cont.slots) < furnaceSlots {
		return
	}
	for _, c := range r.ContainerInfo {
		slot, ok := furnaceSlotOf(c.Container.ContainerID)
		if !ok {
			continue
		}
		for _, s := range c.SlotInfo {
			cur := a.cont.slots[slot]
			switch {
			case s.Count == 0:
				a.cont.slots[slot] = protocol.ItemInstance{}
			case cur.Stack.NetworkID != 0 && cur.Stack.Count > 0:
				a.cont.slots[slot] = protocol.ItemInstance{
					StackNetworkID: s.StackNetworkID,
					Stack:          protocol.ItemStack{ItemType: cur.Stack.ItemType, Count: uint16(s.Count)},
				}
			case item != 0:
				a.cont.slots[slot] = protocol.ItemInstance{
					StackNetworkID: s.StackNetworkID,
					Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: item}, Count: uint16(s.Count)},
				}
			}
		}
	}
}
