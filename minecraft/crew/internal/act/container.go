package act

import (
	"errors"
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// Status codes of an ItemStackResponse that callers are expected to meet.
const (
	StatusOK             uint8 = 0
	StatusOccupied       uint8 = 1  // destination holds another item or a full stack
	StatusBadSource      uint8 = 49 // wrong source stack ID, or the container is already closed
	StatusSlotOutOfRange uint8 = 55
)

// hotbarSlots is the number of slots MobEquipment can select.
const hotbarSlots = 9

// inventorySlots is the main inventory with the hotbar: window 0, slots
// 0-35.
const inventorySlots = 36

// closeContainerType is what the client puts in a ContainerClose it initiates.
const closeContainerType = 0xff

// ItemNames gives the actuator the server's item table, once per session. Item
// packets carry runtime IDs, while belief names its stacks.
func (a *Actuator) ItemNames(entries []protocol.ItemEntry) {
	ids := make(map[string]int32, len(entries))
	for _, e := range entries {
		ids[trimNamespace(e.Name)] = int32(e.RuntimeID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.itemIDs = ids
}

// ItemTable copies the name-to-ID table. A skill compares a container's stacks
// with the item it moves, and stacks carry only IDs.
func (a *Actuator) ItemTable() map[string]int32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int32, len(a.itemIDs))
	for n, id := range a.itemIDs {
		out[n] = id
	}
	return out
}

func trimNamespace(n string) string {
	const prefix = "minecraft:"
	if len(n) > len(prefix) && n[:len(prefix)] == prefix {
		return n[len(prefix):]
	}
	return n
}

// instance names a stack for a packet. A zero Item is an empty hand, which is
// what an empty hotbar slot holds.
func (a *Actuator) instance(it belief.Item) (protocol.ItemInstance, error) {
	if it.Name == "" && it.Count == 0 {
		return protocol.ItemInstance{}, nil
	}
	id, ok := a.itemIDs[it.Name]
	if !ok {
		return protocol.ItemInstance{}, fmt.Errorf("item %q is not in the server's item table", it.Name)
	}
	return protocol.ItemInstance{
		StackNetworkID: it.StackNetworkID,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: uint16(it.Count)},
	}, nil
}

// SelectHotbar holds the stack in a hotbar slot. Without the right tool in
// hand a break is slow and drops nothing.
func (a *Actuator) SelectHotbar(slot int, item belief.Item) error {
	if slot < 0 || slot >= hotbarSlots {
		return fmt.Errorf("slot %d is not on the hotbar", slot)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.instance(item)
	if err != nil {
		return err
	}
	return a.w.WritePacket(&packet.MobEquipment{
		EntityRuntimeID: a.runtimeID,
		NewItem:         inst,
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
	})
}

// Attack hits an entity with the held stack and swings the arm, which the
// server expects alongside the hit. slot is the hotbar slot the stack is in.
func (a *Actuator) Attack(target uint64, slot int, held belief.Item) error {
	if slot < 0 || slot >= hotbarSlots {
		return fmt.Errorf("slot %d is not on the hotbar", slot)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.instance(held)
	if err != nil {
		return err
	}
	if err := a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.UseItemOnEntityTransactionData{
		TargetEntityRuntimeID: target,
		ActionType:            protocol.UseItemOnEntityActionAttack,
		HotBarSlot:            int32(slot),
		HeldItem:              inst,
		Position:              a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClickedPosition:       mgl32.Vec3{0, 1, 0},
	}}); err != nil {
		return err
	}
	return a.w.WritePacket(&packet.Animate{ActionType: packet.AnimateActionSwingArm, EntityRuntimeID: a.runtimeID})
}

// UseItem presses use with the held stack, which starts eating food. The
// server runs the use itself and finishes it when the duration has passed, so
// the unit must keep sending inputs meanwhile.
func (a *Actuator) UseItem(slot int, held belief.Item) error {
	if slot < 0 || slot >= hotbarSlots {
		return fmt.Errorf("slot %d is not on the hotbar", slot)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.instance(held)
	if err != nil {
		return err
	}
	eye := a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0})
	return a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.UseItemTransactionData{
		ActionType:       protocol.UseItemActionClickAir,
		TriggerType:      protocol.TriggerTypePlayerInput,
		HotBarSlot:       int32(slot),
		Hand:             protocol.HandSlotMainHand,
		HeldItem:         inst,
		Position:         eye,
		ClickedPosition:  mgl32.Vec3{},
		ClientPrediction: protocol.ClientPredictionSuccess,
	}})
}

// ReleaseItem lets go of the use key. After a finished meal it ends the use;
// before one it cancels the meal.
func (a *Actuator) ReleaseItem(slot int, held belief.Item) error {
	if slot < 0 || slot >= hotbarSlots {
		return fmt.Errorf("slot %d is not on the hotbar", slot)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.instance(held)
	if err != nil {
		return err
	}
	return a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.ReleaseItemTransactionData{
		HotBarSlot:   int32(slot),
		HeldItem:     inst,
		HeadPosition: a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
	}})
}

// OpenChest clicks the block at pos from the unit's current position. face is
// the side toward the unit, and blockRuntimeID the block's hash from its
// UpdateBlock; the server compares both with its own world.
func (a *Actuator) OpenChest(pos protocol.BlockPos, face int32, hotbar int, held belief.Item, blockRuntimeID uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.instance(held)
	if err != nil {
		return err
	}
	return a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.UseItemTransactionData{
		ActionType:       protocol.UseItemActionClickBlock,
		TriggerType:      protocol.TriggerTypePlayerInput,
		BlockPosition:    pos,
		BlockFace:        face,
		HotBarSlot:       int32(hotbar),
		Hand:             protocol.HandSlotMainHand,
		HeldItem:         inst,
		Position:         a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClickedPosition:  mgl32.Vec3{0.5, 0.5, 0.5},
		BlockRuntimeID:   blockRuntimeID,
		ClientPrediction: protocol.ClientPredictionSuccess,
	}})
}

// Container reports the window opened last. The window ID differs on every
// open, so it must come from here rather than be assumed.
func (a *Actuator) Container() (open bool, windowID byte, pos protocol.BlockPos) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cont.open, a.cont.window, a.cont.pos
}

// ContainerContent copies the open container's slots, indexed by slot. A
// skill decides a chest is full from this, since the server never says so.
func (a *Actuator) ContainerContent() []protocol.ItemInstance {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]protocol.ItemInstance(nil), a.cont.slots...)
}

// Deposit moves count items from an inventory stack into a slot of the open
// container, returning the request ID to poll with Response.
func (a *Actuator) Deposit(from belief.Item, count int, toSlot byte) (requestID int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cont.open {
		return 0, errors.New("no container is open")
	}
	if count < 1 || count > from.Count || count > 255 {
		return 0, fmt.Errorf("cannot move %d of a stack of %d", count, from.Count)
	}
	if from.Slot < 0 || from.Slot > 35 {
		return 0, fmt.Errorf("slot %d is not in the inventory", from.Slot)
	}
	var destStack int32
	if int(toSlot) < len(a.cont.slots) {
		destStack = a.cont.slots[toSlot].StackNetworkID
	}

	place := &protocol.PlaceStackRequestAction{}
	place.Count = byte(count)
	place.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(from.Slot),
		StackNetworkID: from.StackNetworkID,
	}
	place.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerLevelEntity},
		Slot:           toSlot,
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
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	if item, ok := a.itemIDs[from.Name]; ok {
		a.reqItem[id] = item
	}
	return id, nil
}

// Response reports the server's answer to a request. done is false until one
// arrives; the status is StatusOK or the server's rejection code.
func (a *Actuator) Response(requestID int32) (status uint8, done bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	status, done = a.responses[requestID]
	return status, done
}

// CloseContainer closes the window the server opened.
func (a *Actuator) CloseContainer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cont.open {
		return errors.New("no container is open")
	}
	err := a.w.WritePacket(&packet.ContainerClose{WindowID: a.cont.window, ContainerType: closeContainerType})
	if err != nil {
		return err
	}
	a.cont = container{}
	return nil
}

type container struct {
	open   bool
	window byte
	pos    protocol.BlockPos
	slots  []protocol.ItemInstance
}

// Died forgets the open window and any pending stack responses: death closes
// the window on the server without a ContainerClose, and a stale open window
// would let a later deposit believe a chest it never clicked is open. A move
// to the hotbar in flight dies with the same inventory.
func (a *Actuator) Died() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cont = container{}
	a.responses = map[int32]uint8{}
	a.reqItem = map[int32]int32{}
	a.abandoned = map[int32]bool{}
	a.move = 0
}

func (a *Actuator) applyContainerLocked(pk packet.Packet) {
	switch p := pk.(type) {
	case *packet.ContainerOpen:
		a.cont = container{open: true, window: p.WindowID, pos: p.ContainerPosition}
	case *packet.ContainerClose:
		if a.cont.open && p.WindowID == a.cont.window {
			a.cont = container{}
		}
	case *packet.InventoryContent:
		switch {
		case p.WindowID == 0:
			a.inv = [inventorySlots]protocol.ItemInstance{}
			for i := 0; i < len(p.Content) && i < inventorySlots; i++ {
				a.inv[i] = p.Content[i]
			}
		case a.cont.open && p.WindowID == uint32(a.cont.window):
			a.cont.slots = append([]protocol.ItemInstance(nil), p.Content...)
		}
	case *packet.InventorySlot:
		switch {
		case p.WindowID == 0 && p.Slot < inventorySlots:
			a.inv[p.Slot] = p.NewItem
		case a.cont.open && p.WindowID == uint32(a.cont.window) && int(p.Slot) < len(a.cont.slots):
			a.cont.slots[p.Slot] = p.NewItem
		}
	case *packet.InventoryTransaction:
		// A pickup arrives as a transaction on window 0, not as a slot
		// update.
		if _, normal := p.TransactionData.(*protocol.NormalTransactionData); p.TransactionData != nil && !normal {
			return
		}
		for _, ac := range p.Actions {
			if w, ok := ac.WindowID.Value(); ac.SourceType == protocol.InventoryActionSourceContainer && ok && w == 0 && ac.InventorySlot < inventorySlots {
				a.inv[ac.InventorySlot] = ac.NewItem
			}
		}
	case *packet.ItemStackResponse:
		// Whichever request an answer settles — a hotbar move, a deposit,
		// a withdraw — its own-inventory slots are folded into a.inv: no
		// slot packet follows the requests the unit issues itself.
		for _, r := range p.Responses {
			if a.abandoned[r.RequestID] {
				// The move was given up on before its answer came. No caller
				// waits on it any more, but a late success did move the stack,
				// so the inventory still follows it.
				delete(a.abandoned, r.RequestID)
			} else {
				a.responses[r.RequestID] = r.Status
			}
			item := a.reqItem[r.RequestID]
			delete(a.reqItem, r.RequestID)
			if r.Status == StatusOK {
				a.applyWindow0Locked(r, item)
			}
		}
	}
}

// Withdraw moves count items from a slot of the open container into slot
// toSlot of the unit's inventory, returning the request ID to poll with
// Response. to is the stack already in toSlot, or the zero Item when it is
// empty; the server must be quoted the destination's ID to merge onto it.
func (a *Actuator) Withdraw(fromSlot byte, count int, to belief.Item, toSlot int) (requestID int32, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cont.open {
		return 0, errors.New("no container is open")
	}
	if int(fromSlot) >= len(a.cont.slots) {
		return 0, fmt.Errorf("slot %d is not in the container", fromSlot)
	}
	src := a.cont.slots[fromSlot]
	held := int(src.Stack.Count)
	if src.Stack.NetworkID == 0 || held == 0 {
		return 0, fmt.Errorf("container slot %d is empty", fromSlot)
	}
	if count < 1 || count > held || count > 255 {
		return 0, fmt.Errorf("cannot take %d from a stack of %d", count, held)
	}
	if toSlot < 0 || toSlot > 35 {
		return 0, fmt.Errorf("slot %d is not in the inventory", toSlot)
	}

	take := &protocol.TakeStackRequestAction{}
	take.Count = byte(count)
	take.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerLevelEntity},
		Slot:           fromSlot,
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
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	a.reqItem[id] = src.Stack.NetworkID
	return id, nil
}
