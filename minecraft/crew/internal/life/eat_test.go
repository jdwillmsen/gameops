package life

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	idBread     = 340
	idBeef      = 341
	idRotten    = 342
	idStoneSwrd = 343
)

func (r *rig) setHunger(h float32) {
	pk := &packet.UpdateAttributes{EntityRuntimeID: selfID, Attributes: []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.hunger", Value: h}}}}
	r.tracker.Apply(pk)
	r.l.Apply(pk)
}

func eatingRig(t *testing.T, hunger, health float32) *rig {
	t.Helper()
	r := newRig(t, defaults, start)
	r.give(1, idBread, 3)
	r.setHunger(hunger)
	r.setHealth(health)
	return r
}

func (r *rig) uses() (n int) {
	for _, pk := range r.out.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemTransactionData); ok && d.ActionType == protocol.UseItemActionClickAir {
				n++
			}
		}
	}
	return n
}

func (r *rig) releases() (n int) {
	for _, pk := range r.out.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if _, ok := tx.TransactionData.(*protocol.ReleaseItemTransactionData); ok {
				n++
			}
		}
	}
	return n
}

// finishMeal is what the server does once the eating duration has passed.
func (r *rig) finishMeal(slot int, item int32, left int, hunger float32) {
	r.give(slot, item, left)
	r.setHunger(hunger)
}

func (r *rig) logged(event string) bool {
	for _, l := range r.log.lines {
		if l.event == event {
			return true
		}
	}
	return false
}

func TestAHungryUnitSelectsUsesHoldsThenReleases(t *testing.T) {
	r := eatingRig(t, 12, 20)
	r.run(1)
	if r.uses() != 0 {
		t.Fatal("used the item on the same tick it selected it")
	}
	var sawSelect bool
	for _, pk := range r.out.got {
		if m, ok := pk.(*packet.MobEquipment); ok && m.HotBarSlot == 1 && m.NewItem.Stack.NetworkID == idBread {
			sawSelect = true
		}
	}
	if !sawSelect {
		t.Fatal("did not select the bread's hotbar slot first")
	}
	r.run(1)
	if r.uses() != 1 {
		t.Fatalf("uses = %d, want 1", r.uses())
	}
	ins := r.out.inputs()
	if !ins[len(ins)-1].InputData.Load(packet.InputFlagStartUsingItem) {
		t.Error("the input after the click does not start using the item")
	}
	moved := r.feet()
	r.run(20)
	if r.releases() != 0 || r.uses() != 1 {
		t.Errorf("released after %d ticks of a 32-tick meal (uses %d, releases %d)", 22, r.uses(), r.releases())
	}
	for _, in := range r.out.inputs()[2:] {
		if in.InputData.Load(packet.InputFlagStartUsingItem) {
			t.Fatal("the start flag was sent again while holding")
		}
	}
	r.run(11)
	r.finishMeal(1, idBread, 2, 17)
	r.run(2)
	if r.releases() != 1 {
		t.Errorf("releases = %d, want 1 after the meal finished", r.releases())
	}
	if !r.logged("eat") || r.logged("eat_failed") {
		t.Errorf("log = %+v", r.log.lines)
	}
	if dist(r.feet(), moved) > 0.01 {
		t.Error("the unit walked while eating")
	}
	r.run(200)
	if r.uses() != 1 {
		t.Errorf("uses = %d after eating: hunger 17 and full health is not hungry", r.uses())
	}
}

func TestEarlyCompletionReleasesAtOnce(t *testing.T) {
	r := eatingRig(t, 12, 20)
	r.run(5)
	r.finishMeal(1, idBread, 2, 17)
	r.run(1)
	if r.releases() != 1 || !r.logged("eat") {
		t.Errorf("releases %d, log %+v", r.releases(), r.log.lines)
	}
}

func TestEatingThresholdDependsOnBeingHurt(t *testing.T) {
	for _, tc := range []struct {
		hunger, health float32
		eats           bool
	}{
		{20, 20, false},
		{15, 20, false},
		{14, 20, true},
		{18, 12, false},
		{17, 12, true},
		{17, 20, false},
	} {
		r := eatingRig(t, tc.hunger, tc.health)
		r.run(4)
		if got := r.uses() > 0; got != tc.eats {
			t.Errorf("hunger %v health %v: eats=%v, want %v", tc.hunger, tc.health, got, tc.eats)
		}
	}
}

func TestUnknownHungerNeverEats(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(1, idBread, 3)
	r.run(5)
	if r.uses() != 0 {
		t.Error("ate without knowing the hunger level")
	}
}

func TestOnlyGoodFoodOnTheHotbarIsEaten(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(0, idRotten, 5)
	r.give(20, idBread, 5)
	r.setHunger(4)
	r.run(10)
	if r.uses() != 0 || len(r.out.got) != 10 {
		t.Errorf("uses %d, packets %d: rotten flesh and off-hotbar food are not meals", r.uses(), len(r.out.got))
	}
}

func TestTheBestFoodIsChosen(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(1, idBread, 3)
	r.give(2, idBeef, 1)
	r.setHunger(6)
	r.run(2)
	for _, pk := range r.out.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemTransactionData); ok && d.HeldItem.Stack.NetworkID != idBeef {
				t.Errorf("ate item %d, want the beef", d.HeldItem.Stack.NetworkID)
			}
		}
	}
	if r.uses() != 1 {
		t.Errorf("uses = %d", r.uses())
	}
}

func TestEatConfigOffNeverEats(t *testing.T) {
	cfg := defaults
	cfg.Eat = false
	r := newRig(t, cfg, start)
	r.give(1, idBread, 3)
	r.setHunger(2)
	r.run(10)
	if r.uses() != 0 {
		t.Error("ate with EAT off")
	}
}

func TestAMealThatNeverFinishesIsReportedAndBackedOff(t *testing.T) {
	r := eatingRig(t, 6, 20)
	r.run(200)
	if !r.logged("eat_failed") {
		t.Fatalf("no eat_failed in %+v", r.log.lines)
	}
	if r.releases() < 1 {
		t.Error("never released a meal that did not finish")
	}
	uses := r.uses()
	r.run(300)
	if r.uses() != uses {
		t.Errorf("retried within the back-off: %d uses, had %d", r.uses(), uses)
	}
	r.run(1500)
	if r.uses() == uses {
		t.Error("never tried again after the back-off")
	}
}

func TestAFightCutsAMealShort(t *testing.T) {
	r := eatingRig(t, 6, 20)
	r.run(10)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{start.X() + 3, 1, start.Z()})
	if !r.step() {
		t.Fatal("not engaged by a zombie 3 blocks off")
	}
	if r.releases() != 1 {
		t.Errorf("releases = %d, want the meal let go as the fight began", r.releases())
	}
	r.run(60)
	if r.logged("eat") {
		t.Error("counted an abandoned meal as eaten")
	}
}

func TestEatLogsCarryNoCoordinates(t *testing.T) {
	r := eatingRig(t, 6, 20)
	r.run(5)
	r.finishMeal(1, idBread, 2, 11)
	r.run(2)
	for _, l := range r.log.lines {
		for k, v := range l.fields {
			if strings.Contains(strings.ToLower(k), "pos") || k == "x" || k == "y" || k == "z" {
				t.Errorf("%s logged %s=%v", l.event, k, v)
			}
		}
	}
}
