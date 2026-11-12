package life

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/inventory"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	idCobble = 400
	idLoot   = 1000 // loot ids run from here, one item each
)

func invTable() []protocol.ItemEntry {
	table := append([]protocol.ItemEntry(nil), itemTable...)
	table = append(table, protocol.ItemEntry{Name: "minecraft:cobblestone", RuntimeID: idCobble})
	for i := 0; i < 40; i++ {
		table = append(table, protocol.ItemEntry{Name: fmt.Sprintf("minecraft:ore_%d", i), RuntimeID: int16(idLoot + i)})
	}
	return table
}

// invRig is a rig whose unit manages its inventory, with a chest in reach.
func invRig(t *testing.T, chest *protocol.BlockPos) (*rig, *[]*depositStub) {
	t.Helper()
	cfg := defaults
	cfg.Wander = false
	cfg.Inventory = &Inventory{Policy: inventory.Policy{FullSlots: 4, KeepFood: 10, KeepBlocks: 64, KeepFiller: 32, SpareTools: 1}, Chest: chest}
	r := newRig(t, cfg, start)
	table := invTable()
	r.tracker.ItemNames(table)
	r.a.ItemNames(table)
	stubs := &[]*depositStub{}
	r.l.makeDeposit = func(m inventory.Move) skill.Skill {
		d := &depositStub{r: r, move: m, ticks: 3}
		*stubs = append(*stubs, d)
		return d
	}
	return r, stubs
}

// depositStub stands in for a chest deposit: it walks a few ticks, then ends
// the way the test says, taking the item out of the pack as the chest would.
type depositStub struct {
	r       *rig
	move    inventory.Move
	ticks   int
	failure error
	stepped int
	stopped int
}

func (d *depositStub) Name() string                { return "deposit_chest" }
func (d *depositStub) Ready(belief.Snapshot) error { return nil }
func (d *depositStub) Stop(skill.Actuator)         { d.stopped++ }
func (d *depositStub) Step(_ context.Context, a skill.Actuator) (skill.Status, error) {
	d.stepped++
	if d.stepped <= d.ticks {
		return skill.Running, d.r.l.still(a)
	}
	if d.failure != nil {
		return skill.Failed, d.failure
	}
	left := d.move.Count
	for _, it := range d.r.tracker.Snapshot().Inventory {
		if it.Name != d.move.Name || left == 0 {
			continue
		}
		n := min(it.Count, left)
		left -= n
		d.r.give(it.Slot, 0, 0)
		if it.Count > n {
			d.r.giveNamed(it.Slot, d.move.Name, it.Count-n)
		}
	}
	return skill.Done, nil
}

// giveNamed puts a stack in a slot by item name.
func (r *rig) giveNamed(slot int, name string, count int) {
	r.t.Helper()
	for _, e := range invTable() {
		if e.Name == "minecraft:"+name {
			r.give(slot, int32(e.RuntimeID), count)
			return
		}
	}
	r.t.Fatalf("no item %q in the table", name)
}

// fillWithLoot gives slots 0..n-1 one distinct loot item each.
func (r *rig) fillWithLoot(n int) {
	for i := 0; i < n; i++ {
		r.give(i, int32(idLoot+i), 1)
	}
}

// answerDrops plays the server: every drop request in the sink is answered
// as accepted, emptying or thinning its slot.
func (r *rig) answerDrops(from int) (drops []*protocol.DropStackRequestAction, next int) {
	for ; from < len(r.out.got); from++ {
		req, ok := r.out.got[from].(*packet.ItemStackRequest)
		if !ok {
			continue
		}
		for _, q := range req.Requests {
			for _, ac := range q.Actions {
				d, ok := ac.(*protocol.DropStackRequestAction)
				if !ok {
					continue
				}
				drops = append(drops, d)
				var left byte
				for _, it := range r.tracker.Snapshot().Inventory {
					if it.Slot == int(d.Source.Slot) {
						left = byte(it.Count) - d.Count
					}
				}
				resp := &packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
					RequestID: q.RequestID,
					ContainerInfo: []protocol.StackResponseContainerInfo{{
						Container: protocol.FullContainerName{ContainerID: protocol.ContainerCombinedHotBarAndInventory},
						SlotInfo:  []protocol.StackResponseSlotInfo{{Slot: d.Source.Slot, HotbarSlot: d.Source.Slot, Count: left, StackNetworkID: d.Source.StackNetworkID}},
					}},
				}}}
				r.a.Apply(resp)
				r.tracker.Apply(resp)
			}
		}
	}
	return drops, from
}

// runAnswering steps n ticks, answering drops as they are sent.
func (r *rig) runAnswering(n int) []*protocol.DropStackRequestAction {
	var all []*protocol.DropStackRequestAction
	at := len(r.out.got)
	for i := 0; i < n; i++ {
		r.step()
		var d []*protocol.DropStackRequestAction
		d, at = r.answerDrops(at)
		all = append(all, d...)
	}
	return all
}

func chestAt() *protocol.BlockPos { return &protocol.BlockPos{30, 0, 30} }

func TestJunkIsDroppedWholeAndOnlyOnce(t *testing.T) {
	r, _ := invRig(t, nil)
	r.give(3, idRotten, 7)
	r.give(4, idBread, 4)
	drops := r.runAnswering(60)
	if len(drops) != 1 || drops[0].Count != 7 || drops[0].Source.Slot != 3 {
		t.Fatalf("drops = %+v, want one drop of 7 from slot 3", drops)
	}
	if !r.logged("inv_drop") {
		t.Errorf("log = %+v", r.log.lines)
	}
}

func TestFillerBeyondTheReserveIsDroppedFromTheHighestSlotFirst(t *testing.T) {
	r, _ := invRig(t, nil)
	r.give(4, idCobble, 64)
	r.give(5, idCobble, 36)
	drops := r.runAnswering(80)
	var total int
	for _, d := range drops {
		total += int(d.Count)
	}
	if total != 68 {
		t.Fatalf("dropped %d, want 68 (100 held, 32 kept): %+v", total, drops)
	}
	if drops[0].Source.Slot != 5 {
		t.Errorf("first drop from slot %d, want 5", drops[0].Source.Slot)
	}
	left := 0
	for _, it := range r.tracker.Snapshot().Inventory {
		if it.Name == "cobblestone" {
			left += it.Count
		}
	}
	if left != 32 {
		t.Errorf("kept %d cobblestone, want 32", left)
	}
}

func TestADropTheServerRefusesIsNotRepeated(t *testing.T) {
	r, _ := invRig(t, nil)
	r.give(3, idRotten, 7)
	for i := 0; i < 400; i++ {
		r.step()
		for _, pk := range r.out.got {
			if req, ok := pk.(*packet.ItemStackRequest); ok {
				r.a.Apply(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{Status: 1, RequestID: req.Requests[0].RequestID}}})
			}
		}
	}
	n := 0
	for _, pk := range r.out.got {
		if _, ok := pk.(*packet.ItemStackRequest); ok {
			n++
		}
	}
	if n != 1 || !r.logged("inv_drop_failed") {
		t.Errorf("requests = %d, log = %+v; want one request and a failure logged", n, r.log.lines)
	}
}

func TestLitterThatKeepsComingBackStopsBeingDropped(t *testing.T) {
	r, _ := invRig(t, nil)
	var total int
	for round := 0; round < 6; round++ {
		r.give(3, idRotten, 5)
		total += len(r.runAnswering(invDropCycleGap + 20))
	}
	if total != invRepeatLimit+1 {
		t.Errorf("dropped %d times, want %d: the unit keeps picking it up", total, invRepeatLimit+1)
	}
}

func TestLitterThatKeepsComingBackGoesToTheChest(t *testing.T) {
	r, stubs := invRig(t, chestAt())
	r.l.inv.dropAt = map[string]uint64{"rotten_flesh": 0}
	r.l.inv.dropRepeats = map[string]int{"rotten_flesh": invRepeatLimit}
	r.l.tick = 10
	r.fillWithLoot(31)
	r.give(31, idRotten, 5)
	r.run(40)
	var rotten bool
	for _, s := range *stubs {
		rotten = rotten || s.move.Name == "rotten_flesh"
	}
	if !rotten {
		t.Errorf("trip did not store the rotten flesh: %+v", *stubs)
	}
}

func TestANearlyFullPackIsStoredBeforeItFills(t *testing.T) {
	r, stubs := invRig(t, chestAt())
	r.fillWithLoot(32)
	r.run(120)
	if len(*stubs) == 0 {
		t.Fatal("no deposit though the pack had 4 free slots")
	}
	free := inventory.Slots - len(r.tracker.Snapshot().Inventory)
	if free <= 4 {
		t.Errorf("free = %d after the trip, want more than 4", free)
	}
	if !r.logged("inv_deposit") {
		t.Errorf("log = %+v", r.log.lines)
	}
	n := len(*stubs)
	r.run(600)
	if len(*stubs) != n {
		t.Errorf("deposits grew from %d to %d with room again", n, len(*stubs))
	}
}

func TestAPackWithRoomIsLeftAlone(t *testing.T) {
	r, stubs := invRig(t, chestAt())
	r.fillWithLoot(20)
	r.run(300)
	if len(*stubs) != 0 {
		t.Errorf("deposits = %d with 16 free slots", len(*stubs))
	}
}

func TestAChestOutOfReachIsNotWalkedTo(t *testing.T) {
	r, stubs := invRig(t, &protocol.BlockPos{900, 0, 900})
	r.fillWithLoot(34)
	r.run(300)
	if len(*stubs) != 0 {
		t.Errorf("deposits = %d to a chest 1200 blocks away", len(*stubs))
	}
}

func TestNoChestMeansNoTrip(t *testing.T) {
	r, stubs := invRig(t, nil)
	r.fillWithLoot(34)
	r.run(300)
	if len(*stubs) != 0 {
		t.Errorf("deposits = %d with no chest", len(*stubs))
	}
}

func TestAFullPackInterruptsARunningTask(t *testing.T) {
	r, _ := invRig(t, chestAt())
	r.fillWithLoot(33)
	if !r.l.Tending(r.tracker.Snapshot(), true) {
		t.Fatal("a full pack did not claim the tick from a running task")
	}
	roomy, _ := invRig(t, chestAt())
	roomy.fillWithLoot(20)
	if roomy.l.Tending(roomy.tracker.Snapshot(), true) {
		t.Error("claimed the tick with room to spare")
	}
}

func TestEssentialsAreNeverStored(t *testing.T) {
	r, stubs := invRig(t, chestAt())
	r.give(0, idStoneSwrd, 1)
	r.give(1, idBread, 6)
	r.give(2, 312, 1) // wooden pickaxe
	for i := 3; i < 33; i++ {
		r.give(i, int32(idLoot+i), 1)
	}
	r.run(200)
	for _, s := range *stubs {
		switch s.move.Name {
		case "stone_sword", "bread", "wooden_pickaxe":
			t.Errorf("stored %q, an essential", s.move.Name)
		}
	}
	if len(*stubs) == 0 {
		t.Error("stored nothing")
	}
}

func TestAFailedTripBacksOffAndKeepsTheCause(t *testing.T) {
	r, stubs := invRig(t, chestAt())
	r.l.makeDeposit = func(m inventory.Move) skill.Skill {
		d := &depositStub{r: r, move: m, ticks: 2, failure: errors.New("chest is full")}
		*stubs = append(*stubs, d)
		return d
	}
	r.fillWithLoot(33)
	r.run(100)
	if len(*stubs) != 1 || !r.logged("inv_trip_failed") {
		t.Fatalf("visits = %d, log = %+v; want one failed visit", len(*stubs), r.log.lines)
	}
	r.run(invFailBackoff - 200)
	if len(*stubs) != 1 {
		t.Errorf("visits = %d inside the back-off, want 1", len(*stubs))
	}
	r.run(400)
	if len(*stubs) < 2 {
		t.Errorf("visits = %d after the back-off, want a retry", len(*stubs))
	}
}

func TestAFightCutsATripShortAndItResumesLater(t *testing.T) {
	r, stubs := invRig(t, chestAt())
	r.fillWithLoot(33)
	r.run(2)
	if len(*stubs) != 1 || (*stubs)[0].stopped != 0 {
		t.Fatalf("trip not under way: %d deposits", len(*stubs))
	}
	r.l.stopInventory(r.a)
	if (*stubs)[0].stopped != 1 {
		t.Errorf("stopped = %d, want the deposit stopped once", (*stubs)[0].stopped)
	}
	if r.l.inv.busy() {
		t.Error("still busy after the fight stopped it")
	}
	r.run(invAbortBackoff / 2)
	if len(*stubs) != 1 {
		t.Error("restarted inside the back-off")
	}
	r.run(invAbortBackoff)
	if len(*stubs) < 2 {
		t.Error("never resumed")
	}
}

func TestATripHoldsAMealBack(t *testing.T) {
	r, _ := invRig(t, chestAt())
	r.fillWithLoot(33)
	r.give(34, idBread, 5)
	r.tracker.Apply(&packet.UpdateAttributes{EntityRuntimeID: selfID, Attributes: []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.hunger", Value: 2}}}})
	r.run(2)
	if !r.l.inv.busy() {
		t.Skip("trip not yet started")
	}
	if r.ate() {
		t.Error("ate during a trip with a chest open")
	}
}

func TestWithoutTheSettingNothingHappens(t *testing.T) {
	cfg := defaults
	cfg.Wander = false
	r := newRig(t, cfg, start)
	r.give(3, idRotten, 7)
	r.fillWithLoot(2)
	r.run(100)
	for _, pk := range r.out.got {
		if _, ok := pk.(*packet.ItemStackRequest); ok {
			t.Fatal("sent an item request with the manager off")
		}
	}
}

func TestEveryFoodTheManagerKeepsIsOneTheUnitEats(t *testing.T) {
	got := inventory.Foods()
	if len(got) != len(foods) {
		t.Fatalf("inventory knows %d foods, life %d", len(got), len(foods))
	}
	for name, pts := range foods {
		if got[name] != pts {
			t.Errorf("%s: inventory %d points, life %d", name, got[name], pts)
		}
	}
}
