package belief

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const ironHelmetID = 600

func pct(n int) *int { return &n }

func withDamage(id int32, damage any) protocol.ItemInstance {
	it := stack(id, 1, 40)
	it.Stack.NBTData = map[string]any{"Damage": damage}
	return it
}

func pctOf(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestDurabilityFromTheDamageInTheStack(t *testing.T) {
	for _, tc := range []struct {
		name string
		item protocol.ItemInstance
		want *int
	}{
		{"pristine", stack(woodenPick, 1, 1), pct(100)},
		{"half used", withDamage(woodenPick, int32(30)), pct(49)},
		{"nearly gone", withDamage(woodenPick, int32(58)), pct(1)},
		{"fully worn", withDamage(woodenPick, int32(59)), pct(0)},
		{"another integer width", withDamage(woodenPick, int16(30)), pct(49)},
		{"damage past the maximum is not trusted", withDamage(woodenPick, int32(60)), nil},
		{"negative damage", withDamage(woodenPick, int32(-1)), nil},
		{"damage that is not a number", withDamage(woodenPick, "lots"), nil},
		{"fractional damage", withDamage(woodenPick, float32(3.5)), nil},
		{"item that cannot wear out", withDamage(cobblestone, int32(3)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := withItems(t)
			tr.Apply(slotPacket(0, 0, tc.item))
			got := tr.Snapshot().Inventory
			if len(got) != 1 {
				t.Fatalf("inventory = %+v", got)
			}
			if pctOf(got[0].DurabilityPct) != pctOf(tc.want) {
				t.Errorf("durability = %v, want %v", pctOf(got[0].DurabilityPct), pctOf(tc.want))
			}
		})
	}
}

func TestAStackWithoutItsWireHasNoDurability(t *testing.T) {
	if got := durabilityPct("wooden_pickaxe", nil); got != nil {
		t.Errorf("durability = %d, want unknown", *got)
	}
}

func TestEveryArmourSlotIsTrackedWithItsWear(t *testing.T) {
	tr := spawned(t)
	tr.ItemNames(append([]protocol.ItemEntry{{Name: "minecraft:iron_helmet", RuntimeID: ironHelmetID}}, labItems...))
	content := make([]protocol.ItemInstance, 4)
	content[0] = withDamage(ironHelmetID, int32(33))
	tr.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDArmour, Content: content})

	got := tr.Snapshot().Armour
	if len(got) != 1 || got[0].Slot != "helmet" || got[0].Name != "iron_helmet" || pctOf(got[0].DurabilityPct) != 80 {
		t.Fatalf("armour = %+v", got)
	}

	tr.Apply(slotPacket(protocol.WindowIDArmour, 3, withDamage(ironHelmetID, int32(165))))
	got = tr.Snapshot().Armour
	if len(got) != 2 || got[1].Slot != "boots" || pctOf(got[1].DurabilityPct) != 0 {
		t.Fatalf("armour after a boots update = %+v", got)
	}

	tr.Apply(slotPacket(protocol.WindowIDArmour, 0, protocol.ItemInstance{}))
	if got = tr.Snapshot().Armour; len(got) != 1 || got[0].Slot != "boots" {
		t.Errorf("armour after the helmet came off = %+v", got)
	}
}

func TestAnEndedSessionForgetsTheArmour(t *testing.T) {
	tr := spawned(t)
	tr.ItemNames(append([]protocol.ItemEntry{{Name: "minecraft:iron_helmet", RuntimeID: ironHelmetID}}, labItems...))
	tr.Apply(slotPacket(protocol.WindowIDArmour, 0, stack(ironHelmetID, 1, 5)))
	tr.Reset()
	if got := tr.Snapshot().Armour; len(got) != 0 {
		t.Errorf("armour = %+v after a reset", got)
	}
}
