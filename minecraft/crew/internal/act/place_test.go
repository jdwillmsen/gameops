package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestPlaceOnTopClicksTheUpperFace(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 1, 2}})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:dirt", RuntimeID: 3}})
	if err := a.PlaceOnTop(protocol.BlockPos{1, 0, 2}, 2, beliefItem(2, "dirt", 5, 9), 12345); err != nil {
		t.Fatal(err)
	}
	u := s.last(t).(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemTransactionData)
	if u.ActionType != protocol.UseItemActionClickBlock || u.BlockPosition != (protocol.BlockPos{1, 0, 2}) ||
		u.BlockFace != 1 || u.HotBarSlot != 2 || u.BlockRuntimeID != 12345 ||
		u.ClickedPosition != (mgl32.Vec3{0.5, 1, 0.5}) || u.HeldItem.Stack.NetworkID != 3 {
		t.Errorf("use = %+v", u)
	}
	if err := a.PlaceOnTop(protocol.BlockPos{1, 0, 2}, 2, beliefItem(2, "unknown", 1, 1), 1); err == nil {
		t.Error("an item outside the server's table was sent")
	}
}
