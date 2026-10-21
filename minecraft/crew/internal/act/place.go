package act

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

// topFace is the face index of a block's upper side.
const topFace = 1

// placeReach is how far the target's centre may lie from the eye, matching
// the server's placement reach.
const placeReach = 4.5

// stackLimit is what a merged stack is assumed to hold; the server answers
// StatusOccupied past it, which the caller reads as a refusal.
const stackLimit = 64

var (
	// ErrOccupied says the target is not air or a block a placement may
	// overwrite. An unknown target counts: the unit never places into what
	// it has not been told about.
	ErrOccupied = errors.New("the target is not air or replaceable")
	// ErrOutOfReach says the target's centre is more than placeReach from
	// the eye.
	ErrOutOfReach = errors.New("the target is out of reach")
	// ErrNoSupport says no known solid block adjoins the target to click.
	ErrNoSupport = errors.New("no solid neighbour to place against")
	// ErrNoItem says the item is neither on the hotbar nor anywhere else in
	// the inventory.
	ErrNoItem = errors.New("no such item in the inventory")
	// ErrItemMoving says a stack is on its way from the inventory to the
	// hotbar; the caller simply tries again until it lands.
	ErrItemMoving = errors.New("the item is moving to the hotbar")
)

// BlockWorld is what a Solid offers when it also knows block names and the
// network hash of each: a placement must click a known neighbour and quote
// the hash the server has for the block it clicked.
type BlockWorld interface {
	Block(protocol.BlockPos) (name string, known bool)
	BlockHash(protocol.BlockPos) (hash uint32, known bool)
}

// sideFaces lists the side neighbours of a target with the face of each the
// target sits against and the hit point on that face, in the order ties
// break. The face indices are the protocol's: 2 north (-Z), 3 south (+Z),
// 4 west (-X), 5 east (+X).
var sideFaces = [...]struct {
	off  [3]int32
	face int32
	hit  mgl32.Vec3
}{
	{[3]int32{0, 0, -1}, 3, mgl32.Vec3{0.5, 0.5, 1}},
	{[3]int32{0, 0, 1}, 2, mgl32.Vec3{0.5, 0.5, 0}},
	{[3]int32{-1, 0, 0}, 5, mgl32.Vec3{1, 0.5, 0.5}},
	{[3]int32{1, 0, 0}, 4, mgl32.Vec3{0, 0.5, 0.5}},
}

// a.move holds the request ID carrying an item to the hotbar, whose answer
// the placement waits for; 0 means none is in flight.

// PlaceOnTop places the held block on the upper face of the block at against,
// which is how a unit pillars up under its own feet. hash is the clicked
// block's network hash from its UpdateBlock.
func (a *Actuator) PlaceOnTop(against protocol.BlockPos, hotbar int, held belief.Item, hash uint32) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, err := a.instance(held)
	if err != nil {
		return err
	}
	return a.useOnBlock(against, topFace, mgl32.Vec3{0.5, 1, 0.5}, hotbar, inst, hash)
}

// PlaceBlock places one of item into the target cell, clicking the block
// below it on its top face or, when that is not a known solid, the side face
// nearest the eye. It selects a hotbar slot holding the item, first moving a
// stack there from the main inventory; while that request travels it reports
// ErrItemMoving and the caller tries again.
func (a *Actuator) PlaceBlock(target protocol.BlockPos, item string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.solid.(BlockWorld)
	if !ok {
		return errors.New("the solid names no blocks")
	}
	if name, known := w.Block(target); !known || !replaceable(name) {
		return ErrOccupied
	}
	eye := a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0})
	if centre(target).Sub(eye).Len() > placeReach {
		return ErrOutOfReach
	}
	against, face, hit, hash, ok := a.support(target, eye, w)
	if !ok {
		return ErrNoSupport
	}
	slot, held, err := a.holdItem(item)
	if err != nil {
		return err
	}
	// The transaction carries no rotation of its own; the look rides the
	// next input the caller sends, so the pose is aimed here.
	a.pose.Yaw, a.pose.Pitch = lookAt(eye, origin(against).Add(hit))
	if err := a.w.WritePacket(&packet.MobEquipment{
		EntityRuntimeID: a.runtimeID,
		NewItem:         held,
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
	}); err != nil {
		return err
	}
	return a.useOnBlock(against, face, hit, slot, held, hash)
}

// useOnBlock sends the click on a block face that places the held stack.
// Both placers share it so their packets stay identical.
func (a *Actuator) useOnBlock(against protocol.BlockPos, face int32, hit mgl32.Vec3, hotbar int, held protocol.ItemInstance, hash uint32) error {
	return a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.UseItemTransactionData{
		ActionType:       protocol.UseItemActionClickBlock,
		TriggerType:      protocol.TriggerTypePlayerInput,
		BlockPosition:    against,
		BlockFace:        face,
		HotBarSlot:       int32(hotbar),
		Hand:             protocol.HandSlotMainHand,
		HeldItem:         held,
		Position:         a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClickedPosition:  hit,
		BlockRuntimeID:   hash,
		ClientPrediction: protocol.ClientPredictionSuccess,
	}})
}

// support finds the block the placement clicks: the one below the target on
// its top face, else the side neighbour whose face the eye is nearest. An
// interactive block is never clicked, because the server would open it
// instead of placing.
func (a *Actuator) support(target protocol.BlockPos, eye mgl32.Vec3, w BlockWorld) (against protocol.BlockPos, face int32, hit mgl32.Vec3, hash uint32, ok bool) {
	below := protocol.BlockPos{target.X(), target.Y() - 1, target.Z()}
	if h, clickable := a.clickable(below, w); clickable {
		return below, topFace, mgl32.Vec3{0.5, 1, 0.5}, h, true
	}
	best := float32(math.MaxFloat32)
	for _, s := range sideFaces {
		n := protocol.BlockPos{target.X() + s.off[0], target.Y() + s.off[1], target.Z() + s.off[2]}
		h, clickable := a.clickable(n, w)
		if !clickable {
			continue
		}
		if d := origin(n).Add(s.hit).Sub(eye).Len(); d < best {
			against, face, hit, hash, ok, best = n, s.face, s.hit, h, true, d
		}
	}
	return against, face, hit, hash, ok
}

// clickable says the block is known, solid and safe to click, and quotes the
// hash the server must be given for it. An interactive block is not
// clickable: clicking it opens its interface instead of placing.
func (a *Actuator) clickable(p protocol.BlockPos, w BlockWorld) (uint32, bool) {
	name, known := w.Block(p)
	if !known || interactive(name) || !a.solid.Solid(p) {
		return 0, false
	}
	return w.BlockHash(p)
}

// interactive names the blocks the server answers a click on by opening a
// container or interface instead of placing: chests, the furnace family,
// doors and gates, buttons and levers, shulker boxes and the craft and
// storage blocks that take a use before a build does. Beds and anvils come
// in named variants ("teal_bed", "chipped_anvil"), so the families match by
// suffix; a plain substring would also take "bedrock".
func interactive(name string) bool {
	for _, part := range []string{"chest", "door", "gate", "button", "shulker", "furnace"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	if strings.HasSuffix(name, "_bed") || strings.HasSuffix(name, "_anvil") {
		return true
	}
	return name == "crafting_table" || name == "barrel" || name == "smoker" ||
		name == "lever" || name == "bed" || name == "anvil" ||
		name == "enchanting_table" || name == "hopper" || name == "dropper" ||
		name == "dispenser" || name == "note_block"
}

// replaceable names the blocks a placement may overwrite; everything else,
// known or not, is treated as occupied.
func replaceable(name string) bool {
	return shape.Empty(name) || name == "water" || name == "flowing_water"
}

// holdItem resolves the hotbar slot holding item. A stack found only in the
// main inventory is moved onto the hotbar first, and the move's answer is
// awaited across calls.
func (a *Actuator) holdItem(item string) (slot int, held protocol.ItemInstance, err error) {
	id, ok := a.itemIDs[item]
	if !ok {
		return 0, held, ErrNoItem
	}
	if slot, held, ok := a.hotbarHolding(id); ok {
		return slot, held, nil
	}
	if a.move != 0 {
		status, done := a.responses[a.move]
		if !done {
			return 0, held, ErrItemMoving
		}
		delete(a.responses, a.move)
		a.move = 0
		if status != StatusOK {
			return 0, held, fmt.Errorf("server refused the move to the hotbar with status %d", status)
		}
		if slot, held, ok := a.hotbarHolding(id); ok {
			return slot, held, nil
		}
	}
	for from := hotbarSlots; from < inventorySlots; from++ {
		if s := a.inv[from]; s.Stack.NetworkID == id && s.Stack.Count > 0 {
			return 0, held, a.moveToHotbar(from, s)
		}
	}
	return 0, held, ErrNoItem
}

// hotbarHolding finds the slot whose stack is the item.
func (a *Actuator) hotbarHolding(id int32) (slot int, held protocol.ItemInstance, ok bool) {
	for slot := 0; slot < hotbarSlots; slot++ {
		if s := a.inv[slot]; s.Stack.NetworkID == id && s.Stack.Count > 0 {
			return slot, s, true
		}
	}
	return 0, protocol.ItemInstance{}, false
}

// moveToHotbar asks the server to move a whole stack into a hotbar slot so a
// later attempt can hold it, and reports ErrItemMoving for this one.
func (a *Actuator) moveToHotbar(from int, s protocol.ItemInstance) error {
	to, dst, ok := a.hotbarDest(s.Stack.NetworkID, int(s.Stack.Count))
	if !ok {
		return errors.New("no hotbar slot free for the item")
	}
	move := &protocol.PlaceStackRequestAction{}
	move.Count = byte(s.Stack.Count)
	move.Source = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(from),
		StackNetworkID: s.StackNetworkID,
	}
	move.Destination = protocol.StackRequestSlotInfo{
		Container:      protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
		Slot:           byte(to),
		StackNetworkID: dst,
	}
	req := a.nextRequest
	if err := a.w.WritePacket(&packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
		RequestID: req,
		Actions:   []protocol.StackRequestAction{move},
	}}}); err != nil {
		return err
	}
	// The server accepts the negative odd IDs a client counts down through.
	a.nextRequest -= 2
	a.reqItem[req] = s.Stack.NetworkID
	a.move = req
	return ErrItemMoving
}

// hotbarDest is where a stack of n of the item lands: the first empty hotbar
// slot, else a partial stack of the same item with room.
func (a *Actuator) hotbarDest(id int32, n int) (slot int, dst int32, ok bool) {
	for s := 0; s < hotbarSlots; s++ {
		if a.inv[s].Stack.Count == 0 {
			return s, 0, true
		}
	}
	for s := 0; s < hotbarSlots; s++ {
		if t := a.inv[s]; t.Stack.NetworkID == id && int(t.Stack.Count)+n <= stackLimit {
			return s, t.StackNetworkID, true
		}
	}
	return 0, 0, false
}

// applyWindow0Locked folds an accepted answer's own-inventory slot changes
// into a.inv. The answer names counts and stack IDs but never items, so a
// slot that held a stack keeps its item, an emptied one is cleared, and one
// a take filled gets the item the request carried.
func (a *Actuator) applyWindow0Locked(r protocol.ItemStackResponse, takenIn int32) {
	for _, c := range r.ContainerInfo {
		if c.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory {
			continue
		}
		for _, s := range c.SlotInfo {
			if int(s.Slot) >= inventorySlots {
				continue
			}
			cur := a.inv[s.Slot]
			switch {
			case s.Count == 0:
				a.inv[s.Slot] = protocol.ItemInstance{}
			case cur.Stack.NetworkID != 0 && cur.Stack.Count > 0:
				a.inv[s.Slot] = protocol.ItemInstance{
					StackNetworkID: s.StackNetworkID,
					Stack:          protocol.ItemStack{ItemType: cur.Stack.ItemType, Count: uint16(s.Count)},
				}
			case takenIn != 0:
				a.inv[s.Slot] = protocol.ItemInstance{
					StackNetworkID: s.StackNetworkID,
					Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: takenIn}, Count: uint16(s.Count)},
				}
			}
		}
	}
}

func origin(b protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(b.X()), float32(b.Y()), float32(b.Z())}
}

func centre(b protocol.BlockPos) mgl32.Vec3 {
	return origin(b).Add(mgl32.Vec3{0.5, 0.5, 0.5})
}

// lookAt gives the yaw and pitch, in degrees, that face a point from the eye.
func lookAt(eye, to mgl32.Vec3) (yaw, pitch float32) {
	d := to.Sub(eye)
	yaw = float32(math.Atan2(float64(-d.X()), float64(d.Z())) * 180 / math.Pi)
	pitch = float32(math.Atan2(float64(-d.Y()), math.Hypot(float64(d.X()), float64(d.Z()))) * 180 / math.Pi)
	return yaw, pitch
}
