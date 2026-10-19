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
// would let a later deposit believe a chest it never clicked is open.
func (a *Actuator) Died() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cont = container{}
	a.responses = map[int32]uint8{}
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
		if a.cont.open && p.WindowID == uint32(a.cont.window) {
			a.cont.slots = append([]protocol.ItemInstance(nil), p.Content...)
		}
	case *packet.InventorySlot:
		if a.cont.open && p.WindowID == uint32(a.cont.window) && int(p.Slot) < len(a.cont.slots) {
			a.cont.slots[p.Slot] = p.NewItem
		}
	case *packet.ItemStackResponse:
		for _, r := range p.Responses {
			a.responses[r.RequestID] = r.Status
		}
	}
}
