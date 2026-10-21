package act

import (
	"errors"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
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

// stoneBelow names the cell below the target stone and the target air: the
// simplest placement, with a hash unlike any item ID.
func stoneBelow(target protocol.BlockPos) world {
	w := flat()
	w.floor = 0
	w.hash = 4321
	w.names = map[protocol.BlockPos]string{
		{target.X(), target.Y() - 1, target.Z()}: "stone",
		target:                                   "air",
	}
	return w
}

func cobbleInstance(count int, stackID int32) protocol.ItemInstance {
	return protocol.ItemInstance{
		StackNetworkID: stackID,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: cobblestone}, Count: uint16(count)},
	}
}

func newPlacer(w world, feet mgl32.Vec3, slot uint32, inst protocol.ItemInstance) (*Actuator, *sink) {
	a, s := newAct(w, Pose{Feet: feet})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:cobblestone", RuntimeID: cobblestone}})
	a.Apply(&packet.InventorySlot{WindowID: 0, Slot: slot, NewItem: inst})
	return a, s
}

func TestPlaceBlockClicksTheBlockBelowOnItsTopFace(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newPlacer(stoneBelow(target), mgl32.Vec3{1.5, 1, 2.5}, 2, cobbleInstance(16, 9))
	if err := a.PlaceBlock(target, "cobblestone"); err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 2 {
		t.Fatalf("sent %d packets, want the selection and the transaction", len(s.got))
	}
	me, ok := s.got[0].(*packet.MobEquipment)
	if !ok || me.EntityRuntimeID != 7 || me.HotBarSlot != 2 || me.InventorySlot != 2 ||
		me.NewItem.Stack.NetworkID != cobblestone || me.NewItem.StackNetworkID != 9 {
		t.Errorf("equipment = %+v", me)
	}
	tx, ok := s.got[1].(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("second packet is %T", s.got[1])
	}
	u, ok := tx.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		t.Fatalf("transaction data %T", tx.TransactionData)
	}
	if u.ActionType != protocol.UseItemActionClickBlock || u.TriggerType != protocol.TriggerTypePlayerInput ||
		u.BlockPosition != (protocol.BlockPos{1, 0, 2}) || u.BlockFace != 1 ||
		u.HotBarSlot != 2 || u.Hand != protocol.HandSlotMainHand ||
		u.Position != (mgl32.Vec3{1.5, 1 + eyeHeight, 2.5}) || u.ClickedPosition != (mgl32.Vec3{0.5, 1, 0.5}) ||
		u.BlockRuntimeID != 4321 || u.ClientPrediction != protocol.ClientPredictionSuccess ||
		u.HeldItem.Stack.NetworkID != cobblestone || u.HeldItem.StackNetworkID != 9 || u.HeldItem.Stack.Count != 16 {
		t.Errorf("use = %+v", u)
	}
	// The pose now faces the hit point, so the next input carries the look.
	p := a.Pose()
	near(t, "yaw", float64(p.Yaw), 0, 1e-4)
	near(t, "pitch", float64(p.Pitch), 90, 1e-4)
}

func TestPlaceBlockFallsBackToASideFace(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	w := flat()
	w.hash = 4321
	w.extra = map[protocol.BlockPos]bool{{2, 1, 2}: true}
	w.names = map[protocol.BlockPos]string{{2, 1, 2}: "stone", {1, 1, 2}: "air"}
	a, s := newPlacer(w, mgl32.Vec3{1.5, 1, 2.5}, 0, cobbleInstance(16, 9))
	if err := a.PlaceBlock(target, "cobblestone"); err != nil {
		t.Fatal(err)
	}
	tx, ok := s.last(t).(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("sent %T", s.last(t))
	}
	u, ok := tx.TransactionData.(*protocol.UseItemTransactionData)
	if !ok || u.BlockPosition != (protocol.BlockPos{2, 1, 2}) || u.BlockFace != 4 ||
		u.ClickedPosition != (mgl32.Vec3{0, 0.5, 0.5}) || u.BlockRuntimeID != 4321 ||
		u.HotBarSlot != 0 || u.HeldItem.Stack.NetworkID != cobblestone {
		t.Errorf("use = %+v", u)
	}
}

func TestPlaceBlockRefusesImpossibleTargets(t *testing.T) {
	cases := []struct {
		name   string
		target protocol.BlockPos
		names  map[protocol.BlockPos]string
		extra  map[protocol.BlockPos]bool
		want   error
	}{
		{
			"occupied", protocol.BlockPos{1, 1, 2},
			map[protocol.BlockPos]string{{1, 1, 2}: "stone"}, nil, ErrOccupied,
		},
		{
			"unknown target", protocol.BlockPos{1, 1, 2},
			nil, nil, ErrOccupied,
		},
		{
			"out of reach", protocol.BlockPos{9, 1, 2},
			map[protocol.BlockPos]string{{9, 1, 2}: "air"}, nil, ErrOutOfReach,
		},
		{
			"no support", protocol.BlockPos{2, 1, 4},
			map[protocol.BlockPos]string{{2, 1, 4}: "air"}, nil, ErrNoSupport,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := flat()
			w.floor = 0
			w.hash = 4321
			w.names, w.extra = c.names, c.extra
			a, s := newPlacer(w, mgl32.Vec3{1.5, 1, 2.5}, 2, cobbleInstance(16, 9))
			err := a.PlaceBlock(c.target, "cobblestone")
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if len(s.got) != 0 {
				t.Errorf("sent %d packets", len(s.got))
			}
		})
	}
}

func TestPlaceBlockWithoutTheItemSendsNothing(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newPlacer(stoneBelow(target), mgl32.Vec3{1.5, 1, 2.5}, 2, protocol.ItemInstance{})
	if err := a.PlaceBlock(target, "cobblestone"); !errors.Is(err, ErrNoItem) {
		t.Fatalf("err = %v, want ErrNoItem", err)
	}
	if err := a.PlaceBlock(target, "mystery"); !errors.Is(err, ErrNoItem) {
		t.Fatalf("err = %v, want ErrNoItem", err)
	}
	if len(s.got) != 0 {
		t.Errorf("sent %d packets", len(s.got))
	}
}

func TestPlaceBlockMovesTheStackToTheHotbarFirst(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newPlacer(stoneBelow(target), mgl32.Vec3{1.5, 1, 2.5}, 20, cobbleInstance(16, 9))
	if err := a.PlaceBlock(target, "cobblestone"); !errors.Is(err, ErrItemMoving) {
		t.Fatalf("err = %v, want ErrItemMoving", err)
	}
	req, ok := s.last(t).(*packet.ItemStackRequest)
	if !ok || len(req.Requests) != 1 {
		t.Fatalf("sent %T", s.last(t))
	}
	move, ok := req.Requests[0].Actions[0].(*protocol.PlaceStackRequestAction)
	if !ok || move.Source.Slot != 20 || move.Destination.Slot != 0 || move.Count != 16 ||
		move.Source.StackNetworkID != 9 ||
		move.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory ||
		move.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory {
		t.Errorf("move = %+v", move)
	}
	a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
		RequestID: req.Requests[0].RequestID,
		ContainerInfo: []protocol.StackResponseContainerInfo{
			{Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
				SlotInfo: []protocol.StackResponseSlotInfo{
					{Slot: 20, Count: 0, StackNetworkID: 9},
					{Slot: 0, Count: 16, StackNetworkID: 9},
				}},
		},
	}}})
	if err := a.PlaceBlock(target, "cobblestone"); err != nil {
		t.Fatal(err)
	}
	u := s.last(t).(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemTransactionData)
	if u.HotBarSlot != 0 || u.HeldItem.StackNetworkID != 9 || u.HeldItem.Stack.Count != 16 ||
		u.HeldItem.Stack.NetworkID != cobblestone {
		t.Errorf("use = %+v", u)
	}
}

// A withdraw's answer is all the server ever says about the unit's own
// slots, so a placement afterward must find the withdrawn stack: it holds it
// from the slot the answer named, without another move.
func TestPlaceBlockFindsAStackAWithdrawFilledFrom(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	a, s := newAct(stoneBelow(target), Pose{})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:cobblestone", RuntimeID: cobblestone}})
	for _, pk := range labPackets(t, "chest_open") {
		a.Apply(pk)
	}
	// The recording carried the inventory of its session; an empty window 0
	// stands in for the cold inventory the withdraw is to fill.
	a.Apply(&packet.InventoryContent{WindowID: 0})
	a.Apply(&packet.InventorySlot{WindowID: 2, Slot: 0, NewItem: cobbleInstance(16, 9)})
	id, err := a.Withdraw(0, 16, belief.Item{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
		RequestID: id,
		ContainerInfo: []protocol.StackResponseContainerInfo{
			{Container: protocol.FullContainerName{ContainerID: protocol.ContainerLevelEntity},
				SlotInfo: []protocol.StackResponseSlotInfo{{Slot: 0, Count: 0, StackNetworkID: 0}}},
			{Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
				SlotInfo: []protocol.StackResponseSlotInfo{{Slot: 3, Count: 16, StackNetworkID: 77}}},
		},
	}}})
	if err := a.PlaceBlock(target, "cobblestone"); err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 3 {
		t.Fatalf("sent %d packets, want the withdraw, one selection and one transaction", len(s.got))
	}
	me, ok := s.got[1].(*packet.MobEquipment)
	if !ok || me.HotBarSlot != 3 || me.InventorySlot != 3 || me.NewItem.Stack.NetworkID != cobblestone ||
		me.NewItem.Stack.Count != 16 || me.NewItem.StackNetworkID != 77 {
		t.Errorf("equipment = %+v", me)
	}
	u := s.last(t).(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemTransactionData)
	if u.HotBarSlot != 3 || u.HeldItem.Stack.NetworkID != cobblestone ||
		u.HeldItem.Stack.Count != 16 || u.HeldItem.StackNetworkID != 77 {
		t.Errorf("use = %+v", u)
	}
}

func TestPlaceBlockNeverClicksAnInteractiveSupport(t *testing.T) {
	target := protocol.BlockPos{1, 1, 2}
	w := flat()
	w.floor = 0
	w.hash = 4321
	w.names = map[protocol.BlockPos]string{{1, 0, 2}: "crafting_table", {1, 1, 2}: "air"}
	a, s := newPlacer(w, mgl32.Vec3{1.5, 1, 2.5}, 2, cobbleInstance(16, 9))
	if err := a.PlaceBlock(target, "cobblestone"); !errors.Is(err, ErrNoSupport) {
		t.Fatalf("err = %v, want ErrNoSupport", err)
	}
	if len(s.got) != 0 {
		t.Errorf("sent %d packets", len(s.got))
	}
}
