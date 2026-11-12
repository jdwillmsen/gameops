package life

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	idHelmet = 400
	idChest  = 401
	idLegs   = 402
	idBoots  = 403
)

var gearTable = append(append([]protocol.ItemEntry{}, itemTable...),
	protocol.ItemEntry{Name: "minecraft:iron_helmet", RuntimeID: idHelmet},
	protocol.ItemEntry{Name: "minecraft:iron_chestplate", RuntimeID: idChest},
	protocol.ItemEntry{Name: "minecraft:iron_leggings", RuntimeID: idLegs},
	protocol.ItemEntry{Name: "minecraft:iron_boots", RuntimeID: idBoots},
)

func gearRig(t *testing.T) (*rig, *[]*visit) {
	t.Helper()
	r, visits := kitRig(t)
	r.l.cfg.Kit.Gear = true
	r.tracker.ItemNames(gearTable)
	return r, visits
}

// wear puts pieces on the armour slots by id; zero leaves a slot empty.
func (r *rig) wear(ids ...int32) {
	content := make([]protocol.ItemInstance, 4)
	for i, id := range ids {
		if id != 0 {
			content[i] = protocol.ItemInstance{StackNetworkID: int32(900 + i), Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: 1}}
		}
	}
	r.tracker.Apply(&packet.InventoryContent{WindowID: protocol.WindowIDArmour, Content: content})
}

// equipped arms and feeds the unit and gives it both tools.
func (r *rig) equipped() {
	r.give(0, idStoneSwrd, 1)
	r.give(1, idBread, 6)
	r.give(2, 312, 1) // wooden pickaxe
	r.give(3, 302, 1) // iron axe
}

func TestAUnitMissingOnlyAHelmetVisitsTheKitForIt(t *testing.T) {
	r, visits := gearRig(t)
	r.equipped()
	r.wear(0, idChest, idLegs, idBoots)
	r.run(kitSettleTicks + 20)
	if len(*visits) != 1 {
		t.Fatalf("visits = %d, want one for the helmet", len(*visits))
	}
	want := kitMissing{armour: [4]bool{true, false, false, false}}
	if got := r.l.kit.asked; got != want {
		t.Errorf("asked for %+v, want %+v", got, want)
	}
}

func TestAFullyEquippedUnitStaysAwayFromAGearKit(t *testing.T) {
	r, visits := gearRig(t)
	r.equipped()
	r.wear(idHelmet, idChest, idLegs, idBoots)
	r.run(500)
	if len(*visits) != 0 {
		t.Errorf("visits = %d with nothing missing", len(*visits))
	}
}

func TestWithoutGearOnTheKitAnUnarmouredUnitIsNotMissingAnything(t *testing.T) {
	r, visits := kitRig(t)
	r.equipped()
	r.run(500)
	if len(*visits) != 0 {
		t.Errorf("visits = %d for armour and tools a plain kit never fetches", len(*visits))
	}
}

func TestAGearVisitAsksForTheToolsTheUnitLacks(t *testing.T) {
	r, visits := gearRig(t)
	r.give(0, idStoneSwrd, 1)
	r.give(1, idBread, 6)
	r.give(2, 302, 1) // an axe, but no pickaxe
	r.wear(idHelmet, idChest, idLegs, idBoots)
	r.run(kitSettleTicks + 20)
	if len(*visits) != 1 {
		t.Fatalf("visits = %d", len(*visits))
	}
	if got := r.l.kit.asked; got != (kitMissing{pickaxe: true}) {
		t.Errorf("asked for %+v, want a pickaxe only", got)
	}
}

func TestAFullHotbarStillGoesForGearButNotForAWeaponOrFood(t *testing.T) {
	r, visits := gearRig(t)
	for slot := 0; slot < 9; slot++ {
		r.give(slot, 3, 64)
	}
	r.run(kitSettleTicks + 20)
	if len(*visits) != 1 {
		t.Fatalf("visits = %d, want one for the gear", len(*visits))
	}
	got := r.l.kit.asked
	if got.weapon || got.food || !got.gear() {
		t.Errorf("asked for %+v, want gear only", got)
	}
}

func TestTheGearVisitBuildsAWithdrawThatWearsWhatItTakes(t *testing.T) {
	r, _ := gearRig(t)
	r.l.makeKit = nil
	r.l.cfg.Kit.World = nil
	m := kitMissing{armour: [4]bool{true, false, false, true}, pickaxe: true}
	if s := r.l.newKit(m, 0); s == nil || s.Name() != "withdraw_chest" {
		t.Fatalf("newKit = %v", s)
	}
}

func TestKitReachCanBeSetAndDefaultsToNinetySix(t *testing.T) {
	r, _ := kitRig(t)
	snap := r.tracker.Snapshot()
	snap.Position = [3]float32{20, 1, 20}
	chest := func(dx int32) { r.l.cfg.Kit.Chest = protocol.BlockPos{20 + dx, 0, 20} }

	chest(kitReach + 10)
	if r.l.kitInReach(snap) {
		t.Error("the default reach covered a chest 106 blocks off")
	}
	chest(kitReach - 10)
	if !r.l.kitInReach(snap) {
		t.Error("the default reach missed a chest 86 blocks off")
	}
	r.l.cfg.Kit.Reach = 300
	chest(250)
	if !r.l.kitInReach(snap) {
		t.Error("a reach of 300 missed a chest 250 blocks off")
	}
	r.l.cfg.Kit.Reach = 30
	chest(50)
	if r.l.kitInReach(snap) {
		t.Error("a reach of 30 covered a chest 50 blocks off")
	}
}
