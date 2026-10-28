package act

import (
	"errors"
	"fmt"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Statuses an ItemStackResponse gives a creative take that failed.
const (
	// StatusInvalidCraft is what the server says to a creative request made
	// with the inventory screen shut.
	StatusInvalidCraft uint8 = 7
	// StatusNotCreative is what the server says to a creative request from a
	// unit that is not in creative mode.
	StatusNotCreative uint8 = 28
)

// creativeTakeDeadline bounds a take from opening the screen to its answer;
// the unit sends no input meanwhile, as in a craft.
const creativeTakeDeadline = 5 * time.Second

// creativeStack is the size of the stack a take asks for. Creative stock is
// unlimited, so a take is needed once per block type, not once per block.
const creativeStack = 64

var (
	// ErrNotCreative says the unit is not in creative mode, as known to the
	// client or as the server answered.
	ErrNotCreative = errors.New("the unit is not in creative mode")
	// ErrScreenNotOpen says the creative request needs the unit's inventory
	// screen, which is not open.
	ErrScreenNotOpen = errors.New("the inventory screen is not open")
	// ErrNoCreativeItem says the creative inventory has no entry for the item.
	ErrNoCreativeItem = errors.New("the item is not in the creative inventory")
	// ErrHotbarFull says no hotbar slot is empty to take the item into.
	ErrHotbarFull = errors.New("no empty hotbar slot for the item")
	// ErrBadSource says the server did not recognise the created output the
	// take named.
	ErrBadSource = errors.New("the server refused the creative output as a source")
)

// SetCreative gives the actuator a read of whether the unit is in creative
// mode, which the server announces and the actuator cannot see.
func (a *Actuator) SetCreative(creative func() bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creative = creative
}

func (a *Actuator) creativeLocked() bool { return a.creative != nil && a.creative() }

// storeCreativeLocked indexes the creative inventory by item. Variants of one
// item share its ID, and the first is the item's default.
func (a *Actuator) storeCreativeLocked(p *packet.CreativeContent) {
	ids := make(map[int32]uint32, len(p.Items))
	for _, it := range p.Items {
		if _, seen := ids[it.Item.NetworkID]; !seen {
			ids[it.Item.NetworkID] = it.CreativeItemNetworkID
		}
	}
	a.creativeIDs = ids
}

// TakeCreative puts a stack of the item from the creative inventory into an
// empty hotbar slot and waits for the server's answer, so it blocks the step
// for the round trips it takes. The server only honours the request with the
// unit's own inventory screen open.
func (a *Actuator) TakeCreative(item string) error {
	a.mu.Lock()
	creativeID, itemID, slot, err := a.planTakeLocked(item)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(creativeTakeDeadline)
	if a.openInventory(deadline) {
		defer a.CloseContainer()
	}
	a.mu.Lock()
	if !a.cont.open || a.cont.kind != inventoryWindow {
		a.mu.Unlock()
		return ErrScreenNotOpen
	}
	req := a.nextRequest
	// The server only takes the created output named by the request that
	// made it.
	take := &protocol.TakeStackRequestAction{}
	take.Count = creativeStack
	take.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		Slot:           createdOutputSlot,
		StackNetworkID: req,
	}
	take.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:      byte(slot),
	}
	err = a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: req,
		Actions: []protocol.StackRequestAction{
			&protocol.CraftCreativeStackRequestAction{CreativeItemNetworkID: creativeID, NumberOfCrafts: 1},
			take,
		},
	}}})
	if err != nil {
		a.mu.Unlock()
		return err
	}
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	// The answer never names the item.
	a.reqItem[req] = itemID
	a.mu.Unlock()

	status, err := a.awaitStatus(req, deadline)
	if err != nil {
		// A late success still fills the slot; nobody reads its answer.
		a.mu.Lock()
		a.abandoned[req] = true
		a.mu.Unlock()
		return err
	}
	switch status {
	case StatusOK:
		a.craftLog("creative_take", map[string]any{"item": item})
		return nil
	case StatusInvalidCraft:
		return fmt.Errorf("%w: status %d", ErrScreenNotOpen, status)
	case StatusNotCreative:
		return fmt.Errorf("%w: status %d", ErrNotCreative, status)
	case StatusBadSource:
		return fmt.Errorf("%w: status %d", ErrBadSource, status)
	}
	return fmt.Errorf("%w: status %d", ErrRejected, status)
}

// planTakeLocked resolves what a take names: the creative ID to quote, the
// item's runtime ID and the empty hotbar slot to take it into.
func (a *Actuator) planTakeLocked(item string) (creativeID uint32, itemID int32, slot int, err error) {
	if !a.creativeLocked() {
		return 0, 0, 0, ErrNotCreative
	}
	itemID, known := a.itemIDs[item]
	if !known {
		return 0, 0, 0, fmt.Errorf("%w: %q is not in the server's item table", ErrNoCreativeItem, item)
	}
	creativeID, known = a.creativeIDs[itemID]
	if !known {
		return 0, 0, 0, fmt.Errorf("%w: %q", ErrNoCreativeItem, item)
	}
	for slot = 0; slot < hotbarSlots; slot++ {
		if a.inv[slot].Stack.Count == 0 {
			return creativeID, itemID, slot, nil
		}
	}
	return 0, 0, 0, ErrHotbarFull
}
