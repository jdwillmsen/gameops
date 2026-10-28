package act

import (
	"errors"
	"fmt"
	"math/bits"
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
	// StatusBadAmount is what the server says to a take of more than the
	// item stacks to: signs, banners and eggs stack to 16, tools to 1.
	StatusBadAmount uint8 = 51
)

// creativeTakeDeadline bounds a take from opening the screen to its answer;
// the unit sends no input meanwhile, as in a craft.
const creativeTakeDeadline = 5 * time.Second

// creativeStacks are the sizes a take asks for, largest first. Creative stock
// is unlimited, so a take is needed once per block type, not once per block;
// an item that does not stack to 64 is refused the first size and given the
// next.
var creativeStacks = []byte{64, 16, 1}

// abandonHorizon is how long the books on a take nobody got an answer to are
// kept: an answer later than this is not coming.
const abandonHorizon = time.Minute

var (
	// ErrNotCreative says the unit is not in creative mode, as known to the
	// client or as the server answered.
	ErrNotCreative = errors.New("the unit is not in creative mode")
	// ErrScreenNotOpen says the creative request needs the unit's inventory
	// screen, which is not open.
	ErrScreenNotOpen = errors.New("the inventory screen is not open")
	// ErrNoCreativeItem says the creative inventory has no entry for the item.
	ErrNoCreativeItem = errors.New("the item is not in the creative inventory")
	// ErrHotbarFull says no hotbar slot is empty to take the item into, and
	// none holds a creative take that may be replaced.
	ErrHotbarFull = errors.New("no empty hotbar slot for the item")
	// ErrBadAmount says the server refused the size of the stack asked for.
	ErrBadAmount = errors.New("the server refused the stack size")
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
// empty hotbar slot, or over the least recently used slot of an earlier take
// when none is empty, and waits for the server's answer, so it blocks the step
// for the round trips it takes. The server only honours the request with the
// unit's own inventory screen open.
func (a *Actuator) TakeCreative(item string) error {
	var err error
	// A refused request may or may not have applied its destroy first, and
	// the refusal does not say. The slot it evicted is therefore not quoted
	// again: the retry goes to another slot, or to this one without a destroy
	// when there is none.
	var unsure uint16
	for _, n := range creativeStacks {
		var evicted int
		if evicted, err = a.takeCreative(item, n, unsure); !errors.Is(err, ErrBadAmount) {
			return err
		}
		if evicted >= 0 {
			unsure |= 1 << evicted
		}
	}
	return err
}

// takeCreative makes one request for count items, never planning into a slot
// of the avoid set unless nothing else will do (it then takes without a destroy).
// evicted is the slot the request destroyed a stack in, or -1.
func (a *Actuator) takeCreative(item string, count byte, avoid uint16) (evicted int, err error) {
	a.mu.Lock()
	a.forgetStaleTakesLocked()
	_, _, _, _, err = a.planTakeAvoidingLocked(item, avoid)
	a.mu.Unlock()
	if err != nil {
		return -1, err
	}
	deadline := time.Now().Add(creativeTakeDeadline)
	if a.openInventory(deadline) {
		defer a.CloseContainer()
	}
	a.mu.Lock()
	if !a.cont.open || a.cont.kind != inventoryWindow {
		a.mu.Unlock()
		return -1, ErrScreenNotOpen
	}
	// The lock was released while the screen opened, and a slot update may
	// have landed meanwhile: plan again so the destroy quotes what is there.
	creativeID, itemID, slot, evict, err := a.planTakeAvoidingLocked(item, avoid)
	if err != nil {
		a.mu.Unlock()
		return -1, err
	}
	req := a.nextRequest
	// The server only takes the created output named by the request that
	// made it.
	take := &protocol.TakeStackRequestAction{}
	take.Count = count
	take.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCreatedOutput},
		Slot:           createdOutputSlot,
		StackNetworkID: req,
	}
	take.Destination = protocol.StackRequestSlotInfo{
		Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:      byte(slot),
	}
	actions := []protocol.StackRequestAction{
		&protocol.CraftCreativeStackRequestAction{CreativeItemNetworkID: creativeID, NumberOfCrafts: 1},
		take,
	}
	if evict {
		// The server refuses a take onto an occupied slot, but accepts it in
		// the same request that destroys the stack there.
		held := a.inv[slot]
		destroy := &protocol.DestroyStackRequestAction{}
		destroy.Count = byte(held.Stack.Count)
		destroy.Source = protocol.StackRequestSlotInfo{
			Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
			Slot:           byte(slot),
			StackNetworkID: held.StackNetworkID,
		}
		actions = append([]protocol.StackRequestAction{destroy}, actions...)
	}
	err = a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: req,
		Actions:   actions,
	}}})
	if err != nil {
		a.mu.Unlock()
		return -1, err
	}
	evicted = -1
	if evict {
		evicted = slot
		a.replacing[req] = slot
	}
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	// The answer never names the item.
	a.reqItem[req] = itemID
	a.takes[req] = creativeTake{slot: slot, item: itemID, at: a.now()}
	a.mu.Unlock()

	status, err := a.awaitStatus(req, deadline)
	if err != nil {
		// A late success still fills the slot; nobody reads its answer.
		a.mu.Lock()
		a.abandoned[req] = true
		a.mu.Unlock()
		return evicted, err
	}
	switch status {
	case StatusOK:
		a.craftLog("creative_take", map[string]any{"item": item})
		return evicted, nil
	case StatusInvalidCraft:
		return evicted, fmt.Errorf("%w: status %d", ErrScreenNotOpen, status)
	case StatusNotCreative:
		return evicted, fmt.Errorf("%w: status %d", ErrNotCreative, status)
	case StatusBadAmount:
		return evicted, fmt.Errorf("%w: status %d", ErrBadAmount, status)
	case StatusBadSource:
		return evicted, fmt.Errorf("%w: status %d", ErrBadSource, status)
	}
	return evicted, fmt.Errorf("%w: status %d", ErrRejected, status)
}

// forgetStaleTakesLocked drops the books on takes that went unanswered past
// the horizon, which would otherwise stay until the unit died.
func (a *Actuator) forgetStaleTakesLocked() {
	for req, tk := range a.takes {
		if a.now().Sub(tk.at) > abandonHorizon {
			delete(a.takes, req)
			delete(a.reqItem, req)
			delete(a.replacing, req)
			delete(a.abandoned, req)
		}
	}
}

// touchSlot notes that the unit just took or held the item in a hotbar slot.
func (a *Actuator) touchSlot(slot int) {
	a.useClock++
	a.slotUse[slot] = a.useClock
}

// planTakeLocked resolves what a take names: the creative ID to quote, the
// item's runtime ID and the hotbar slot to take it into. With no empty slot it
// picks the least recently used one still holding a stack an earlier creative take
// put there, and evict says that stack must be destroyed first. A slot holding
// the requested item is never picked, and neither is one filled any other way:
// those are the unit's own tools and food.
func (a *Actuator) planTakeLocked(item string) (creativeID uint32, itemID int32, slot int, evict bool, err error) {
	return a.planTakeAvoidingLocked(item, 0)
}

// planTakeAvoidingLocked is planTakeLocked that leaves the slots of the avoid
// set alone, their content being unknown; with no other slot it takes into
// the lowest of them without a destroy.
func (a *Actuator) planTakeAvoidingLocked(item string, avoid uint16) (creativeID uint32, itemID int32, slot int, evict bool, err error) {
	if !a.creativeLocked() {
		return 0, 0, 0, false, ErrNotCreative
	}
	itemID, known := a.itemIDs[item]
	if !known {
		return 0, 0, 0, false, fmt.Errorf("%w: %q is not in the server's item table", ErrNoCreativeItem, item)
	}
	creativeID, known = a.creativeIDs[itemID]
	if !known {
		return 0, 0, 0, false, fmt.Errorf("%w: %q", ErrNoCreativeItem, item)
	}
	for slot = 0; slot < hotbarSlots; slot++ {
		if avoid&(1<<slot) == 0 && a.inv[slot].Stack.Count == 0 {
			return creativeID, itemID, slot, false, nil
		}
	}
	best := -1
	for s := 0; s < hotbarSlots; s++ {
		held := a.inv[s]
		if avoid&(1<<s) != 0 || held.Stack.NetworkID == itemID || a.takenStack[s] == 0 || a.takenStack[s] != held.StackNetworkID {
			continue
		}
		if best < 0 || a.slotUse[s] < a.slotUse[best] {
			best = s
		}
	}
	if best < 0 && avoid != 0 {
		return creativeID, itemID, bits.TrailingZeros16(avoid), false, nil
	}
	if best < 0 {
		return 0, 0, 0, false, ErrHotbarFull
	}
	return creativeID, itemID, best, true, nil
}
