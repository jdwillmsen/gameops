package camera

import (
	"encoding/binary"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func equipmentFrames(ms [][]byte) map[uint64]handSlots {
	out := map[uint64]handSlots{}
	for _, f := range ofType(ms, frameEquipment) {
		n, k := binary.Uvarint(f[1:])
		p := 1 + k
		for ; n > 0; n-- {
			id, k := binary.Uvarint(f[p:])
			p += k
			var s handSlots
			for i := range s {
				flags, l := f[p], int(f[p+1])
				s[i] = handItem{Name: string(f[p+2 : p+2+l]), Glint: flags&itemGlint != 0}
				p += 2 + l
			}
			out[id] = s
		}
	}
	return out
}

func armourExtraFrames(ms [][]byte) map[uint64]armourMods {
	out := map[uint64]armourMods{}
	for _, f := range ofType(ms, frameArmourExtra) {
		n, k := binary.Uvarint(f[1:])
		p := 1 + k
		for ; n > 0; n-- {
			id, k := binary.Uvarint(f[p:])
			p += k
			var mods armourMods
			for i := range mods {
				flags := f[p]
				p++
				mods[i].Glint = flags&itemGlint != 0
				if flags&itemTrim != 0 {
					l := int(f[p])
					mods[i].Pattern = string(f[p+1 : p+1+l])
					p += 1 + l
					l = int(f[p])
					mods[i].Material = string(f[p+1 : p+1+l])
					p += 1 + l
				}
			}
			out[id] = mods
		}
	}
	return out
}

func stack(id int32, nbt map[string]any) protocol.ItemInstance {
	it := item(id)
	it.Stack.NBTData = nbt
	return it
}

func TestOtherEntitiesHandsAreSent(t *testing.T) {
	h, _ := newTestHub(t, 4)
	h.SetItems([]protocol.ItemEntry{{Name: "minecraft:iron_sword", RuntimeID: 1}, {Name: "minecraft:shield", RuntimeID: 2}})
	live := attach(t, h)
	mustDrain(t, live)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Basalt"}, &sink{})
	h.Apply(&packet.MobEquipment{EntityRuntimeID: 8, NewItem: stack(1, map[string]any{"ench": []any{map[string]any{"id": int16(9), "lvl": int16(1)}}}), WindowID: protocol.WindowIDInventory}, &sink{})
	h.Apply(&packet.MobEquipment{EntityRuntimeID: 8, NewItem: item(2), WindowID: protocol.WindowIDOffHand}, &sink{})
	want := handSlots{{"iron_sword", true}, {"shield", false}}
	if got := equipmentFrames(mustDrain(t, live)); got[8] != want {
		t.Fatalf("hands = %v", got)
	}
	// An unchanged item is not sent again.
	h.Apply(&packet.MobEquipment{EntityRuntimeID: 8, NewItem: item(2), WindowID: protocol.WindowIDOffHand}, &sink{})
	if got := equipmentFrames(mustDrain(t, live)); len(got) != 0 {
		t.Errorf("resent = %v", got)
	}
	if late := equipmentFrames(mustDrain(t, attach(t, h))); late[8] != want {
		t.Errorf("late hands = %v", late)
	}
	h.Reset()
	if late := equipmentFrames(mustDrain(t, attach(t, h))); len(late) != 0 {
		t.Errorf("hands survived a reset: %v", late)
	}
}

func TestUnitsOwnHandsFollowHotbarAndSelection(t *testing.T) {
	h, _ := newTestHub(t, 4)
	h.SetItems([]protocol.ItemEntry{{Name: "minecraft:iron_sword", RuntimeID: 1}, {Name: "minecraft:bread", RuntimeID: 2}, {Name: "minecraft:shield", RuntimeID: 3}})
	h.Spawn(5)
	live := attach(t, h)
	mustDrain(t, live)
	hot := make([]protocol.ItemInstance, 36)
	hot[0], hot[3] = item(1), item(2)
	h.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDInventory, Content: hot}, &sink{})
	h.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDOffHand, Content: []protocol.ItemInstance{item(3)}}, &sink{})
	if got := equipmentFrames(mustDrain(t, live)); got[0] != (handSlots{{"iron_sword", false}, {"shield", false}}) {
		t.Fatalf("own hands = %v", got)
	}
	h.SetOwnHeld(3)
	if got := equipmentFrames(mustDrain(t, live)); got[0].Main().Name != "bread" {
		t.Errorf("after selecting slot 3 = %v", got)
	}
	// The server moving the selection counts too, and so does a stack
	// replaced under it.
	h.Apply(&packet.PlayerHotBar{SelectedHotBarSlot: 0, SelectHotBarSlot: true}, &sink{})
	h.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: 0, NewItem: protocol.ItemInstance{}}, &sink{})
	got := equipmentFrames(mustDrain(t, live))
	if got[0].Main().Name != "" || got[0][1].Name != "shield" {
		t.Errorf("after emptying the held slot = %v", got)
	}
	if late := equipmentFrames(mustDrain(t, attach(t, h))); late[0] != got[0] {
		t.Errorf("late own hands = %v", late)
	}
}

func TestArmourGlintAndTrimAreSent(t *testing.T) {
	h, _ := newTestHub(t, 4)
	h.SetItems([]protocol.ItemEntry{{Name: "minecraft:netherite_helmet", RuntimeID: 1}, {Name: "minecraft:netherite_boots", RuntimeID: 2}})
	live := attach(t, h)
	mustDrain(t, live)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Basalt"}, &sink{})
	trim := map[string]any{"Trim": map[string]any{"Material": "minecraft:quartz", "Pattern": "coast"}}
	h.Apply(&packet.MobArmourEquipment{EntityRuntimeID: 8, Helmet: stack(1, trim), Boots: stack(2, map[string]any{"ench": []any{map[string]any{}}})}, &sink{})
	want := armourMods{{Pattern: "coast", Material: "quartz"}, {}, {}, {Glint: true}}
	if got := armourExtraFrames(mustDrain(t, live)); got[8] != want {
		t.Fatalf("mods = %v", got)
	}
	// The unit's own comes from its armour window.
	h.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDArmour, Content: []protocol.ItemInstance{stack(1, trim), {}, {}, {}}}, &sink{})
	if got := armourExtraFrames(mustDrain(t, live)); got[0][0].Pattern != "coast" {
		t.Errorf("own mods = %v", got)
	}
	late := armourExtraFrames(mustDrain(t, attach(t, h)))
	if late[8] != want || late[0][0].Material != "quartz" {
		t.Errorf("late mods = %v", late)
	}
}

func TestPlainArmourSendsNoDetail(t *testing.T) {
	h, _ := newTestHub(t, 4)
	h.SetItems([]protocol.ItemEntry{{Name: "minecraft:iron_helmet", RuntimeID: 1}})
	live := attach(t, h)
	mustDrain(t, live)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8, Username: "Basalt"}, &sink{})
	h.Apply(&packet.MobArmourEquipment{EntityRuntimeID: 8, Helmet: item(1)}, &sink{})
	if got := armourExtraFrames(mustDrain(t, live)); len(got) != 0 {
		t.Errorf("mods = %v", got)
	}
}
