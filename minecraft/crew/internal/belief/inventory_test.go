package belief

import (
	"bytes"
	"encoding/binary"
	"os"
	"reflect"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Item IDs of the lab server that recorded the fixtures.
const (
	cobblestone = 4
	woodenPick  = 312
	shieldID    = 358
)

var labItems = []protocol.ItemEntry{
	{Name: "minecraft:cobblestone", RuntimeID: cobblestone},
	{Name: "minecraft:wooden_pickaxe", RuntimeID: woodenPick},
	{Name: "minecraft:stick", RuntimeID: 5},
}

func stack(id int32, count uint16, stackID int32) protocol.ItemInstance {
	return protocol.ItemInstance{StackNetworkID: stackID, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: count}}
}

func withItems(t *testing.T) *Tracker {
	t.Helper()
	tr := spawned(t)
	tr.ItemNames(labItems)
	return tr
}

func slotPacket(window, slot uint32, it protocol.ItemInstance) *packet.InventorySlot {
	return &packet.InventorySlot{WindowID: window, Slot: slot, NewItem: it}
}

func TestInventoryContentFillsTheSnapshot(t *testing.T) {
	tr := withItems(t)
	content := make([]protocol.ItemInstance, 36)
	content[0] = stack(woodenPick, 1, 10)
	content[2] = stack(cobblestone, 41, 11)
	content[35] = stack(5, 3, 12)
	tr.Apply(&packet.InventoryContent{WindowID: 0, Content: content})

	want := []Item{
		{Slot: 0, Name: "wooden_pickaxe", Count: 1, StackNetworkID: 10},
		{Slot: 2, Name: "cobblestone", Count: 41, StackNetworkID: 11},
		{Slot: 35, Name: "stick", Count: 3, StackNetworkID: 12},
	}
	if got := tr.Snapshot().Inventory; !reflect.DeepEqual(got, want) {
		t.Errorf("inventory = %+v, want %+v", got, want)
	}
}

func TestInventoryContentReplacesWhatWasThere(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 7, stack(cobblestone, 9, 1)))
	tr.Apply(&packet.InventoryContent{WindowID: 0, Content: make([]protocol.ItemInstance, 36)})
	if got := tr.Snapshot().Inventory; len(got) != 0 {
		t.Errorf("inventory = %+v, want empty", got)
	}
}

func TestInventorySlotReplacesOneSlot(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(cobblestone, 5, 1)))
	tr.Apply(slotPacket(0, 1, stack(woodenPick, 1, 2)))
	tr.Apply(slotPacket(0, 0, stack(cobblestone, 6, 3)))
	want := []Item{
		{Slot: 0, Name: "cobblestone", Count: 6, StackNetworkID: 3},
		{Slot: 1, Name: "wooden_pickaxe", Count: 1, StackNetworkID: 2},
	}
	if got := tr.Snapshot().Inventory; !reflect.DeepEqual(got, want) {
		t.Errorf("inventory = %+v, want %+v", got, want)
	}
	tr.Apply(slotPacket(0, 0, protocol.ItemInstance{}))
	if got := tr.Snapshot().Inventory; len(got) != 1 || got[0].Slot != 1 {
		t.Errorf("after emptying slot 0: %+v", got)
	}
}

func TestOtherWindowsDoNotTouchTheInventory(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(cobblestone, 5, 1)))
	tr.Apply(slotPacket(2, 0, stack(woodenPick, 1, 2)))
	tr.Apply(&packet.InventoryContent{WindowID: 2, Content: make([]protocol.ItemInstance, 27)})
	got := tr.Snapshot().Inventory
	if len(got) != 1 || got[0].Name != "cobblestone" {
		t.Errorf("inventory = %+v", got)
	}
}

func TestSlotsPastTheInventoryAreIgnored(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 36, stack(cobblestone, 5, 1)))
	if got := tr.Snapshot().Inventory; len(got) != 0 {
		t.Errorf("inventory = %+v", got)
	}
}

func TestResetForgetsTheInventory(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(cobblestone, 5, 1)))
	tr.Reset()
	tr.Spawn(self, [3]float32{}, 0, 0, 0)
	if got := tr.Snapshot().Inventory; len(got) != 0 {
		t.Errorf("inventory = %+v", got)
	}
}

func TestSpawnForgetsTheLastSessionsInventory(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(cobblestone, 5, 1)))
	tr.Spawn(self, [3]float32{}, 0, 0, 0)
	if got := tr.Snapshot().Inventory; len(got) != 0 {
		t.Errorf("inventory = %+v", got)
	}
}

func TestUnknownRuntimeIDsAreNamedByID(t *testing.T) {
	tr := spawned(t) // no ItemNames call at all
	tr.Apply(slotPacket(0, 0, stack(999, 2, 1)))
	got := tr.Snapshot().Inventory
	if len(got) != 1 || got[0].Name != "item#999" {
		t.Errorf("inventory = %+v", got)
	}
}

func TestSnapshotInventoryIsACopy(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(cobblestone, 5, 1)))
	tr.Snapshot().Inventory[0].Count = 99
	if got := tr.Snapshot().Inventory[0].Count; got != 5 {
		t.Errorf("count = %d after a reader changed its copy", got)
	}
}

// labPackets reads a recorded fixture: records of a little-endian uint32
// length, then the packet header and payload as the server sent them.
func labPackets(t *testing.T, name string) []packet.Packet {
	t.Helper()
	b, err := os.ReadFile("../act/testdata/" + name + ".bin")
	if err != nil {
		t.Fatal(err)
	}
	pool := packet.NewServerPool()
	var out []packet.Packet
	for len(b) > 0 {
		n := binary.LittleEndian.Uint32(b)
		// bytes.Buffer, not Reader: a Reader fails on every empty item.
		body := bytes.NewBuffer(b[4 : 4+n])
		b = b[4+n:]
		var h packet.Header
		if err := h.Read(body); err != nil {
			t.Fatal(err)
		}
		pk := pool[h.PacketID]()
		pk.Marshal(protocol.NewReader(body, shieldID, false))
		out = append(out, pk)
	}
	return out
}

func find(items []Item, slot int) (Item, bool) {
	for _, it := range items {
		if it.Slot == slot {
			return it, true
		}
	}
	return Item{}, false
}

func applyAll(tr *Tracker, pks []packet.Packet) {
	for _, pk := range pks {
		tr.Apply(pk)
	}
}

func TestToolWearKeepsThePickaxeInItsSlot(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(woodenPick, 1, 121)))
	applyAll(tr, labPackets(t, "break_stone"))
	it, ok := find(tr.Snapshot().Inventory, 0)
	if !ok || it.Name != "wooden_pickaxe" || it.Count != 1 || it.StackNetworkID != 121 {
		t.Errorf("slot 0 = %+v, %v", it, ok)
	}
}

func TestPickupTransactionRaisesTheStack(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 0, stack(woodenPick, 1, 121)))
	tr.Apply(slotPacket(0, 1, stack(cobblestone, 41, 120)))
	applyAll(tr, labPackets(t, "pickup"))

	inv := tr.Snapshot().Inventory
	if it, _ := find(inv, 1); it.Name != "cobblestone" || it.Count != 42 || it.StackNetworkID != 125 {
		t.Errorf("slot 1 = %+v", it)
	}
	if len(inv) != 2 {
		t.Errorf("the world half of the transaction reached the inventory: %+v", inv)
	}
}

func TestPickupIntoAnEmptySlotCreatesTheStack(t *testing.T) {
	tr := withItems(t)
	tr.Apply(&packet.InventoryTransaction{
		TransactionData: &protocol.NormalTransactionData{},
		Actions: []protocol.InventoryAction{{
			SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(0)),
			InventorySlot: 4, NewItem: stack(cobblestone, 1, 77),
		}},
	})
	if it, ok := find(tr.Snapshot().Inventory, 4); !ok || it.Name != "cobblestone" || it.Count != 1 {
		t.Errorf("slot 4 = %+v, %v", it, ok)
	}
}

func TestTransactionsOnOtherWindowsAreIgnored(t *testing.T) {
	tr := withItems(t)
	tr.Apply(&packet.InventoryTransaction{
		TransactionData: &protocol.NormalTransactionData{},
		Actions: []protocol.InventoryAction{{
			SourceType: protocol.InventoryActionSourceContainer, WindowID: protocol.Option(int8(2)),
			InventorySlot: 4, NewItem: stack(cobblestone, 1, 77),
		}},
	})
	if got := tr.Snapshot().Inventory; len(got) != 0 {
		t.Errorf("inventory = %+v", got)
	}
}

func TestItemStackResponseUpdatesTheDepositedSlot(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 1, stack(cobblestone, 42, 125)))
	applyAll(tr, labPackets(t, "chest_deposit"))
	if it, ok := find(tr.Snapshot().Inventory, 1); !ok || it.Name != "cobblestone" || it.Count != 26 || it.StackNetworkID != 125 {
		t.Errorf("slot 1 = %+v, %v", it, ok)
	}
}

func response(status uint8, container byte, slot, count byte, id int32) *packet.ItemStackResponse {
	r := protocol.ItemStackResponse{Status: status}
	if status == 0 {
		r.ContainerInfo = []protocol.StackResponseContainerInfo{{
			Container: protocol.FullContainerName{ContainerID: container},
			SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: slot, HotbarSlot: slot, Count: count, StackNetworkID: id}},
		}}
	}
	return &packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{r}}
}

func TestItemStackResponseWithZeroCountEmptiesTheSlot(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 3, stack(cobblestone, 16, 9)))
	tr.Apply(response(0, protocol.ContainerCombinedHotBarAndInventory, 3, 0, 0))
	if got := tr.Snapshot().Inventory; len(got) != 0 {
		t.Errorf("inventory = %+v", got)
	}
}

func TestItemStackResponseForOtherContainersAndRejectionsIsIgnored(t *testing.T) {
	tr := withItems(t)
	tr.Apply(slotPacket(0, 3, stack(cobblestone, 16, 9)))
	tr.Apply(response(0, protocol.ContainerLevelEntity, 3, 0, 0))
	tr.Apply(response(49, 0, 3, 0, 0))
	if it, ok := find(tr.Snapshot().Inventory, 3); !ok || it.Count != 16 {
		t.Errorf("slot 3 = %+v, %v", it, ok)
	}
}
