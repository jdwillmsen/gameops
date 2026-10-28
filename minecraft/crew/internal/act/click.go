package act

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ClickBlock holds item and clicks the face of the block at against at the
// point hit, given as a fraction of the block along each axis. Unlike
// PlaceBlock it chooses nothing: not the support, not the face, not the look.
// The server derives a placed block's state from the face, the hit point and
// the look and sneak of the tick before, so the caller sets those with Tick
// first. A creative unit holding none of the item takes it first; while a
// stack moves to the hotbar it reports ErrItemMoving and the caller retries.
func (a *Actuator) ClickBlock(against protocol.BlockPos, face int32, hit mgl32.Vec3, item string) error {
	if err := a.stockCreative(item); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.solid.(BlockWorld)
	if !ok {
		return errNoBlockWorld
	}
	hash, known := w.BlockHash(against)
	if !known {
		return ErrNoSupport
	}
	slot, held, err := a.holdItem(item)
	if err != nil {
		return err
	}
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

// ClickAir holds item and uses it on nothing, as a client does when the
// crosshair is on air or a liquid; the server picks the target from the look
// the unit's inputs carry. A bucket places or takes liquid this way.
func (a *Actuator) ClickAir(item string) error {
	if err := a.stockCreative(item); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	slot, held, err := a.holdItem(item)
	if err != nil {
		return err
	}
	if err := a.w.WritePacket(&packet.MobEquipment{
		EntityRuntimeID: a.runtimeID,
		NewItem:         held,
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
	}); err != nil {
		return err
	}
	return a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.UseItemTransactionData{
		ActionType:       protocol.UseItemActionClickAir,
		TriggerType:      protocol.TriggerTypePlayerInput,
		HotBarSlot:       int32(slot),
		Hand:             protocol.HandSlotMainHand,
		HeldItem:         held,
		Position:         a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClientPrediction: protocol.ClientPredictionSuccess,
	}})
}
