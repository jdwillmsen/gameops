package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func beliefItem(slot int, name string, count int, stackID int32) belief.Item {
	return belief.Item{Slot: slot, Name: name, Count: count, StackNetworkID: stackID}
}

func opened(t *testing.T) (*Actuator, *sink) {
	t.Helper()
	a, s := newAct(flat(), Pose{})
	for _, pk := range labPackets(t, "chest_open") {
		a.Apply(pk)
	}
	return a, s
}

func TestContainerOpenAndCloseAreTracked(t *testing.T) {
	a, s := opened(t)
	open, win, pos := a.Container()
	if !open || win != 2 {
		t.Fatalf("container = %v window %d, want open window 2", open, win)
	}
	if cp := (protocol.BlockPos{}); pos == cp {
		t.Error("container position not recorded")
	}

	// The window ID changes on every open, so a second open replaces it.
	a.Apply(&packet.ContainerOpen{WindowID: 7, ContainerPosition: protocol.BlockPos{1, 2, 3}})
	if _, win, pos = a.Container(); win != 7 || pos != (protocol.BlockPos{1, 2, 3}) {
		t.Errorf("second open = window %d at %v", win, pos)
	}

	// A close for some other window leaves it open.
	a.Apply(&packet.ContainerClose{WindowID: 2})
	if open, _, _ = a.Container(); !open {
		t.Error("a stale close shut the current window")
	}
	if err := a.CloseContainer(); err != nil {
		t.Fatal(err)
	}
	cc, ok := s.last(t).(*packet.ContainerClose)
	if !ok || cc.WindowID != 7 || cc.ContainerType != 0xff || cc.ServerSide {
		t.Fatalf("close = %#v", s.last(t))
	}
	if open, _, _ = a.Container(); open {
		t.Error("still open after CloseContainer")
	}

	a.Apply(&packet.ContainerOpen{WindowID: 9})
	a.Apply(&packet.ContainerClose{WindowID: 9, ContainerType: 247})
	if open, _, _ = a.Container(); open {
		t.Error("the server's close was ignored")
	}
}

func TestCloseContainerWithNothingOpenIsAnError(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	if err := a.CloseContainer(); err == nil {
		t.Error("closing nothing succeeded")
	}
}

func TestOpenChestSendsTheRecordedTransaction(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 0, 2}})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:wooden_pickaxe", RuntimeID: woodenPick}})
	err := a.OpenChest(protocol.BlockPos{1, 0, 5}, 5, 0, beliefItem(0, "wooden_pickaxe", 1, 77), 3162850062)
	if err != nil {
		t.Fatal(err)
	}
	tx, ok := s.last(t).(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("sent %T", s.last(t))
	}
	u, ok := tx.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		t.Fatalf("data %T", tx.TransactionData)
	}
	if u.ActionType != protocol.UseItemActionClickBlock || u.TriggerType != protocol.TriggerTypePlayerInput ||
		u.BlockPosition != (protocol.BlockPos{1, 0, 5}) || u.BlockFace != 5 || u.Hand != protocol.HandSlotMainHand ||
		u.BlockRuntimeID != 3162850062 || u.ClientPrediction != protocol.ClientPredictionSuccess ||
		u.ClickedPosition != (mgl32.Vec3{0.5, 0.5, 0.5}) {
		t.Errorf("use = %+v", u)
	}
	if u.Position != (mgl32.Vec3{1, 1.62, 2}) {
		t.Errorf("position = %v, want the eye", u.Position)
	}
	if u.HeldItem.Stack.NetworkID != woodenPick || u.HeldItem.StackNetworkID != 77 {
		t.Errorf("held = %+v", u.HeldItem)
	}
}

func TestDepositBuildsAPlaceRequestFromTheInventorySlot(t *testing.T) {
	a, s := opened(t)
	id, err := a.Deposit(beliefItem(1, "cobblestone", 42, 31), 16, 0)
	if err != nil {
		t.Fatal(err)
	}
	if id != -3 {
		t.Errorf("first request ID = %d, want -3", id)
	}
	req, ok := s.last(t).(*packet.ItemStackRequest)
	if !ok || len(req.Requests) != 1 {
		t.Fatalf("sent %#v", s.last(t))
	}
	r := req.Requests[0]
	if r.RequestID != id || len(r.Actions) != 1 {
		t.Fatalf("request = %+v", r)
	}
	pl, ok := r.Actions[0].(*protocol.PlaceStackRequestAction)
	if !ok {
		t.Fatalf("action %T", r.Actions[0])
	}
	if pl.Count != 16 ||
		pl.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory || pl.Source.Slot != 1 || pl.Source.StackNetworkID != 31 ||
		pl.Destination.Container.ContainerID != protocol.ContainerLevelEntity || pl.Destination.Slot != 0 || pl.Destination.StackNetworkID != 0 {
		t.Errorf("place = %+v", pl)
	}

	id2, _ := a.Deposit(beliefItem(1, "cobblestone", 42, 31), 1, 0)
	if id2 != -5 {
		t.Errorf("second request ID = %d, want -5", id2)
	}
}

func TestDepositMergesOntoAKnownChestStack(t *testing.T) {
	a, s := opened(t)
	a.Apply(&packet.InventorySlot{WindowID: 2, Slot: 4, NewItem: protocol.ItemInstance{StackNetworkID: 88, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: cobblestone}, Count: 5}}})
	if _, err := a.Deposit(beliefItem(1, "cobblestone", 42, 31), 8, 4); err != nil {
		t.Fatal(err)
	}
	pl := s.last(t).(*packet.ItemStackRequest).Requests[0].Actions[0].(*protocol.PlaceStackRequestAction)
	if pl.Destination.StackNetworkID != 88 {
		t.Errorf("destination stack ID = %d, want 88", pl.Destination.StackNetworkID)
	}
}

func TestDepositNeedsAnOpenContainerAndSaneArguments(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	if _, err := a.Deposit(beliefItem(1, "cobblestone", 42, 31), 1, 0); err == nil {
		t.Error("deposit with no chest open succeeded")
	}
	b, _ := opened(t)
	for _, c := range []struct {
		name  string
		it    belief.Item
		count int
	}{{"zero count", beliefItem(1, "cobblestone", 42, 31), 0}, {"over stack", beliefItem(1, "cobblestone", 42, 31), 43}, {"bad slot", beliefItem(40, "cobblestone", 42, 31), 1}} {
		if _, err := b.Deposit(c.it, c.count, 0); err == nil {
			t.Errorf("%s accepted", c.name)
		}
	}
}

func TestContainerContentFollowsTheOpenWindow(t *testing.T) {
	a, _ := opened(t)
	c := a.ContainerContent()
	if len(c) != 27 {
		t.Fatalf("chest has %d slots, want 27", len(c))
	}
	a.Apply(&packet.InventorySlot{WindowID: 2, Slot: 3, NewItem: protocol.ItemInstance{StackNetworkID: 9, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: cobblestone}, Count: 16}}})
	if got := a.ContainerContent()[3]; got.Stack.Count != 16 {
		t.Errorf("slot 3 = %+v", got)
	}
	// A different window is not the chest.
	a.Apply(&packet.InventorySlot{WindowID: 0, Slot: 3, NewItem: protocol.ItemInstance{StackNetworkID: 1, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 5}, Count: 1}}})
	if got := a.ContainerContent()[3]; got.Stack.NetworkID != cobblestone {
		t.Error("the inventory window overwrote the chest")
	}
}

func TestResponseReportsTheRecordedSuccess(t *testing.T) {
	a, _ := opened(t)
	if _, done := a.Response(-3); done {
		t.Fatal("answered before any response")
	}
	for _, pk := range labPackets(t, "chest_deposit") {
		a.Apply(pk)
	}
	st, done := a.Response(-3)
	if !done || st != StatusOK {
		t.Errorf("response = %d done=%v, want ok", st, done)
	}
}

func TestResponseReportsAFailedRequest(t *testing.T) {
	a, _ := opened(t)
	for id, status := range map[int32]uint8{-5: StatusOccupied, -7: StatusBadSource, -9: StatusSlotOutOfRange} {
		a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{Status: status, RequestID: id}}})
		if st, done := a.Response(id); !done || st != status {
			t.Errorf("request %d = %d done=%v, want %d", id, st, done, status)
		}
	}
	if StatusOccupied != 1 || StatusBadSource != 49 || StatusSlotOutOfRange != 55 {
		t.Error("status constants do not match the recorded server")
	}
}

func TestItemTableCopiesTheServerTable(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:cobblestone", RuntimeID: cobblestone}})
	got := a.ItemTable()
	if got["cobblestone"] != cobblestone || len(got) != 1 {
		t.Fatalf("table = %v", got)
	}
	got["dirt"] = 1
	if _, leaked := a.ItemTable()["dirt"]; leaked {
		t.Error("the table was shared, not copied")
	}
}
