package belief

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const elytraID = 574

func withElytraTable(t *testing.T) *Tracker {
	t.Helper()
	tr := spawned(t)
	tr.ItemNames(append([]protocol.ItemEntry{{Name: "minecraft:elytra", RuntimeID: elytraID}}, labItems...))
	return tr
}

func TestTheChestSlotOfTheArmourWindowIsWhatTheUnitWears(t *testing.T) {
	tr := withElytraTable(t)
	if got := tr.Snapshot().Chest; got != "" {
		t.Fatalf("chest = %q before any armour packet", got)
	}
	content := make([]protocol.ItemInstance, 4)
	content[1] = stack(elytraID, 1, 5)
	tr.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDArmour, Content: content})
	if got := tr.Snapshot().Chest; got != "elytra" {
		t.Errorf("chest = %q, want elytra", got)
	}

	tr.Apply(slotPacket(protocol.WindowIDArmour, 1, protocol.ItemInstance{}))
	if got := tr.Snapshot().Chest; got != "" {
		t.Errorf("chest = %q after the slot was emptied", got)
	}
	tr.Apply(slotPacket(protocol.WindowIDArmour, 1, stack(elytraID, 1, 6)))
	if got := tr.Snapshot().Chest; got != "elytra" {
		t.Errorf("chest = %q after a slot update, want elytra", got)
	}
}

func TestOtherArmourSlotsAreNotTheChest(t *testing.T) {
	tr := withElytraTable(t)
	tr.Apply(slotPacket(protocol.WindowIDArmour, 0, stack(elytraID, 1, 5)))
	if got := tr.Snapshot().Chest; got != "" {
		t.Errorf("chest = %q from the helmet slot", got)
	}
}

func TestAnEndedSessionForgetsTheChest(t *testing.T) {
	tr := withElytraTable(t)
	tr.Apply(slotPacket(protocol.WindowIDArmour, 1, stack(elytraID, 1, 5)))
	tr.Reset()
	if got := tr.Snapshot().Chest; got != "" {
		t.Errorf("chest = %q after a reset", got)
	}
}
