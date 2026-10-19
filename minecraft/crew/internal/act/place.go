package act

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// topFace is the face index of a block's upper side.
const topFace = 1

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
	return a.w.WritePacket(&packet.InventoryTransaction{TransactionData: &protocol.UseItemTransactionData{
		ActionType:       protocol.UseItemActionClickBlock,
		TriggerType:      protocol.TriggerTypePlayerInput,
		BlockPosition:    against,
		BlockFace:        topFace,
		HotBarSlot:       int32(hotbar),
		Hand:             protocol.HandSlotMainHand,
		HeldItem:         inst,
		Position:         a.pose.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClickedPosition:  mgl32.Vec3{0.5, 1, 0.5},
		BlockRuntimeID:   hash,
		ClientPrediction: protocol.ClientPredictionSuccess,
	}})
}
