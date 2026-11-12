package act

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	rawIron  = 60
	ironIng  = 64
	coalItem = 7
)

// furnaceOpen opens a furnace window, 3 slots: ingredient, fuel, result.
func furnaceOpen(t *testing.T, result protocol.ItemInstance) (*Actuator, *sink) {
	t.Helper()
	a, s := newAct(flat(), Pose{})
	a.Apply(&packet.ContainerOpen{WindowID: 4, ContainerType: 2, ContainerPosition: protocol.BlockPos{1, 0, 2}})
	a.Apply(&packet.InventoryContent{WindowID: 4, Content: []protocol.ItemInstance{{}, {}, result}})
	return a, s
}

func stack(id, item int32, n uint16) protocol.ItemInstance {
	return protocol.ItemInstance{StackNetworkID: id, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: item}, Count: n}}
}

func lastRequest(t *testing.T, s *sink) protocol.StackRequestAction {
	t.Helper()
	req, ok := s.last(t).(*packet.ItemStackRequest)
	if !ok || len(req.Requests) != 1 || len(req.Requests[0].Actions) != 1 {
		t.Fatalf("sent %#v", s.last(t))
	}
	return req.Requests[0].Actions[0]
}

func TestFurnaceWindowNeedsAllThreeSlots(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	if a.FurnaceWindow() {
		t.Error("no window is a furnace window")
	}
	a.Apply(&packet.ContainerOpen{WindowID: 4, ContainerType: 2})
	if a.FurnaceWindow() {
		t.Error("a window with no content is not ready")
	}
	a.Apply(&packet.InventoryContent{WindowID: 4, Content: make([]protocol.ItemInstance, 3)})
	if !a.FurnaceWindow() {
		t.Error("a window with three slots is ready")
	}
}

func TestFurnaceLoadNamesTheFurnaceContainerAndSlot(t *testing.T) {
	for _, c := range []struct {
		slot      FurnaceSlot
		container byte
	}{
		{FurnaceIngredient, protocol.ContainerFurnaceIngredient},
		{FurnaceFuel, protocol.ContainerFurnaceFuel},
	} {
		a, s := furnaceOpen(t, protocol.ItemInstance{})
		id, err := a.FurnaceLoad(beliefItem(2, "raw_iron", 10, 31), 8, c.slot)
		if err != nil {
			t.Fatal(err)
		}
		if id != -3 {
			t.Errorf("request id = %d, want -3", id)
		}
		pl, ok := lastRequest(t, s).(*protocol.PlaceStackRequestAction)
		if !ok {
			t.Fatalf("action %T", lastRequest(t, s))
		}
		if pl.Count != 8 ||
			pl.Source.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory || pl.Source.Slot != 2 || pl.Source.StackNetworkID != 31 ||
			pl.Destination.Container.ContainerID != c.container || pl.Destination.Slot != byte(c.slot) || pl.Destination.StackNetworkID != 0 {
			t.Errorf("slot %d: place = %+v", c.slot, pl)
		}
	}
}

func TestFurnaceLoadQuotesAStackAlreadyInTheSlot(t *testing.T) {
	a, s := furnaceOpen(t, protocol.ItemInstance{})
	a.Apply(&packet.InventorySlot{WindowID: 4, Slot: 1, NewItem: stack(77, coalItem, 3)})
	if _, err := a.FurnaceLoad(beliefItem(2, "coal", 10, 31), 2, FurnaceFuel); err != nil {
		t.Fatal(err)
	}
	pl := lastRequest(t, s).(*protocol.PlaceStackRequestAction)
	if pl.Destination.StackNetworkID != 77 {
		t.Errorf("merge destination stack id = %d, want 77", pl.Destination.StackNetworkID)
	}
}

func TestFurnaceLoadRefusesWhatCannotBeDone(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	if _, err := a.FurnaceLoad(beliefItem(2, "coal", 10, 31), 1, FurnaceFuel); err == nil {
		t.Error("loaded with no container open")
	}
	a, _ = furnaceOpen(t, protocol.ItemInstance{})
	for name, f := range map[string]func() error{
		"into the result":   func() error { _, err := a.FurnaceLoad(beliefItem(2, "coal", 10, 31), 1, FurnaceResult); return err },
		"zero":              func() error { _, err := a.FurnaceLoad(beliefItem(2, "coal", 10, 31), 0, FurnaceFuel); return err },
		"more than a stack": func() error { _, err := a.FurnaceLoad(beliefItem(2, "coal", 10, 31), 11, FurnaceFuel); return err },
		"from off the grid": func() error { _, err := a.FurnaceLoad(beliefItem(36, "coal", 10, 31), 1, FurnaceFuel); return err },
	} {
		if f() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestFurnaceTakeQuotesTheResultStack(t *testing.T) {
	a, s := furnaceOpen(t, stack(90, ironIng, 5))
	id, err := a.FurnaceTake(FurnaceResult, 5, beliefItem(9, "iron_ingot", 3, 55), 9)
	if err != nil {
		t.Fatal(err)
	}
	tk, ok := lastRequest(t, s).(*protocol.TakeStackRequestAction)
	if !ok {
		t.Fatalf("action %T", lastRequest(t, s))
	}
	if tk.Count != 5 ||
		tk.Source.Container.ContainerID != protocol.ContainerFurnaceResult || tk.Source.Slot != byte(FurnaceResult) || tk.Source.StackNetworkID != 90 ||
		tk.Destination.Container.ContainerID != protocol.ContainerCombinedHotBarAndInventory || tk.Destination.Slot != 9 || tk.Destination.StackNetworkID != 55 {
		t.Errorf("take = %+v", tk)
	}
	a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{RequestID: id, Status: StatusOK}}})
	if st, done := a.Response(id); !done || st != StatusOK {
		t.Errorf("response = %d done=%v", st, done)
	}
}

func TestFurnaceTakeRefusesAnEmptySlotAndBadArguments(t *testing.T) {
	a, _ := furnaceOpen(t, protocol.ItemInstance{})
	if _, err := a.FurnaceTake(FurnaceResult, 1, beliefItem(0, "", 0, 0), 9); err == nil {
		t.Error("took from an empty result slot")
	}
	a, _ = furnaceOpen(t, stack(90, ironIng, 5))
	for name, f := range map[string]func() error{
		"more than there is": func() error { _, err := a.FurnaceTake(FurnaceResult, 6, beliefItem(0, "", 0, 0), 9); return err },
		"zero":               func() error { _, err := a.FurnaceTake(FurnaceResult, 0, beliefItem(0, "", 0, 0), 9); return err },
		"to off the grid":    func() error { _, err := a.FurnaceTake(FurnaceResult, 1, beliefItem(0, "", 0, 0), 36); return err },
		"from a bad slot":    func() error { _, err := a.FurnaceTake(FurnaceSlot(7), 1, beliefItem(0, "", 0, 0), 9); return err },
	} {
		if f() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	closed, _ := newAct(flat(), Pose{})
	if _, err := closed.FurnaceTake(FurnaceResult, 1, beliefItem(0, "", 0, 0), 9); err == nil {
		t.Error("took with no container open")
	}
}
