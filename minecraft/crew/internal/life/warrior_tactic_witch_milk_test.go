package life

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	idMilk  = 390
	idHoney = 391

	effectSlowness    = 2
	effectPoison      = 19
	effectFatalPoison = 25
)

// witchMilkSpy wraps the milk tactic and counts what the profile offered it
// and what it took, so a decline behind several gates is a number, not
// silence.
type witchMilkSpy struct {
	witchMilk
	offered, handled int
}

func (s *witchMilkSpy) Engage(ctx context.Context, f *Fight) (bool, error) {
	s.offered++
	handled, err := s.witchMilk.Engage(ctx, f)
	if handled {
		s.handled++
	}
	return handled, err
}

// witchBar puts the cure and its look-alike on the item tables, which the
// rig's base table does not carry.
func (r *rig) witchBar() {
	table := append(slices.Clone(itemTable),
		protocol.ItemEntry{Name: "minecraft:milk_bucket", RuntimeID: idMilk},
		protocol.ItemEntry{Name: "minecraft:honey_bottle", RuntimeID: idHoney},
	)
	r.tracker.ItemNames(table)
	r.a.ItemNames(table)
}

// splashed gives the tracker an effect, as the server's packet does when one
// of the witch's potions lands.
func (r *rig) splashed(effect int32) {
	r.tracker.Apply(&packet.MobEffect{EntityRuntimeID: selfID, Operation: packet.MobEffectAdd, EffectType: effect, Amplifier: 0, Duration: 400})
}

func milkRig(t *testing.T, kit func(*rig), witchAt mgl32.Vec3, effect int32) *rig {
	t.Helper()
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(witchNamed("witch/milk")); err != nil {
		t.Fatal(err)
	}
	r.witchBar()
	if kit != nil {
		kit(r)
	}
	r.spawn(80, "minecraft:witch", witchAt)
	if effect != 0 {
		r.splashed(effect)
	}
	return r
}

func TestWitchMilkDrinksTheCupWhileTheFightIsOn(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.give(4, idMilk, 1) }, mgl32.Vec3{26.5, 1, 20.5}, effectPoison)
	r.run(4)
	if n := r.out.uses(4); n != 1 {
		t.Fatalf("uses = %d, want the cup clicked once to start the drink", n)
	}
	if d := dist(r.feet(), start); d > 0.1 {
		t.Errorf("walked %.2f blocks off the spot while drinking", d)
	}
	r.give(4, idMilk, 0) // the server's half of the meal: the bucket empties
	r.run(3)
	if n := r.out.releases(4); n != 1 {
		t.Fatalf("releases = %d, want the finished drink let go", n)
	}
	if !r.logged("eat") || r.logged("eat_failed") {
		t.Fatalf("log = %+v", r.log.lines)
	}
	r.tracker.Apply(&packet.MobEffect{EntityRuntimeID: selfID, Operation: packet.MobEffectRemove, EffectType: effectPoison})
	r.run(50)
	if n := r.out.uses(4); n != 1 {
		t.Errorf("uses = %d once cured, want one meal per splash and no more", n)
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Errorf("never swung again after the cure; log %+v", r.log.lines)
	}
}

func TestWitchMilkDrinksUnderEachEffectThePotionsCarry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effect int32
	}{
		{"poison", effectPoison},
		{"slowness", effectSlowness},
		{"fatal_poison", effectFatalPoison},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := milkRig(t, func(r *rig) { r.give(4, idMilk, 1) }, mgl32.Vec3{26.5, 1, 20.5}, tc.effect)
			r.run(3)
			if n := r.out.uses(4); n != 1 {
				t.Fatalf("uses = %d under %v, want the drink started", n, tc.name)
			}
		})
	}
}

func TestAWitchMilkMealTheServerNeverCountsFailsIntoABackoff(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.give(4, idMilk, 1) }, mgl32.Vec3{26.5, 1, 20.5}, effectPoison)
	r.run(60)
	if !r.logged("eat_failed") {
		t.Fatalf("no eat_failed in %+v", r.log.lines)
	}
	if n := r.out.uses(4); n != 1 {
		t.Errorf("uses = %d, want one drink for the meal that never landed", n)
	}
	if n := r.out.releases(4); n < 1 {
		t.Errorf("releases = %d, want the run-out meal released", n)
	}
	uses := r.out.uses(4)
	r.run(100)
	if n := r.out.uses(4); n != uses {
		t.Errorf("drank again within the backoff: %d uses, had %d", n, uses)
	}
}

func TestWitchMilkDoesNothingWhenNothingIsSplashed(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.give(4, idMilk, 1) }, mgl32.Vec3{26.5, 1, 20.5}, 0)
	r.run(30)
	if n := r.out.uses(4); n != 0 {
		t.Errorf("drank %d times on a clean frame", n)
	}
	for _, pk := range r.out.got {
		if m, ok := pk.(*packet.MobEquipment); ok && int(m.HotBarSlot) == 4 {
			t.Fatal("reached for the poison cure with no poison on")
		}
	}
}

func TestAUnitWithoutACupFightsTheWitchThroughItsPotions(t *testing.T) {
	r := milkRig(t, nil, mgl32.Vec3{26.5, 1, 20.5}, effectPoison)
	r.give(0, 303, 1)
	r.run(40)
	if n := r.uses(); n != 0 {
		t.Errorf("%d item uses with no cure on the belt", n)
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Errorf("never swung through the fight while poisoned")
	}
	if r.logged("eat") || r.logged("eat_failed") {
		t.Errorf("log = %+v", r.log.lines)
	}
}

func TestWitchMilkWaitsForClearanceBeforeItChoosesTheCup(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.give(4, idMilk, 1) }, mgl32.Vec3{22.5, 1, 20.5}, effectPoison)
	r.give(0, 303, 1)
	r.run(40)
	if n := r.out.uses(4); n != 0 {
		t.Errorf("drank with the witch %v blocks in the meal's clearance", dist(r.feet(), mgl32.Vec3{22.5, 1, 20.5}))
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Errorf("never answered the close witch with the fight")
	}
}

func TestWitchMilkDeclinesARetreatOrderAndLetsTheRoleFightCarryIt(t *testing.T) {
	spy := &witchMilkSpy{}
	withTactics(t, spy)
	r := newWarrior(t, "striker", start)
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderRetreat}})
	if err := r.l.SetProfile(witchNamed("witch/milk")); err != nil {
		t.Fatal(err)
	}
	r.witchBar()
	r.give(4, idMilk, 1)
	witch := mgl32.Vec3{22.5, 1, 20.5}
	r.spawn(80, "minecraft:witch", witch)
	r.splashed(effectPoison)
	r.run(40)
	if spy.offered == 0 {
		t.Fatal("the cure was never offered the tick")
	}
	if spy.handled != 0 {
		t.Errorf("the cure took %d ticks under a retreat order", spy.handled)
	}
	if n := r.out.uses(4); n != 0 {
		t.Errorf("drank under a retreat order")
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks under a retreat order", n)
	}
	if d := dist(r.feet(), witch); d < 5 {
		t.Errorf("%.1f blocks from the witch under retreat, want ground gained", d)
	}
}

func TestWitchMilkLeavesACureOffTheHotbarAlone(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.give(20, idMilk, 1) }, mgl32.Vec3{26.5, 1, 20.5}, effectPoison)
	r.run(30)
	if n := r.uses(); n != 0 {
		t.Errorf("%d item uses from a shelf slot", n)
	}
}

func TestWitchMilkLeavesAHoneyBottleAlone(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.give(4, idHoney, 1) }, mgl32.Vec3{26.5, 1, 20.5}, effectPoison)
	r.run(30)
	if n := r.out.uses(4); n != 0 {
		t.Errorf("drank %d times from the honey", n)
	}
}

func TestAWitchMilkCupAlreadyHeldSkipsTheSelection(t *testing.T) {
	r := milkRig(t, func(r *rig) { r.tracker.SetHeld(4); r.give(4, idMilk, 1) }, mgl32.Vec3{26.5, 1, 20.5}, effectPoison)
	r.run(3)
	if n := r.out.uses(4); n != 1 {
		t.Fatalf("uses = %d, want the drink straight away", n)
	}
	for _, pk := range r.out.got {
		if _, ok := pk.(*packet.MobEquipment); ok {
			t.Fatal("selected a slot with the cup already in hand")
		}
	}
}

func TestWitchTacticNamesAreTheTwoRegisteredVariants(t *testing.T) {
	names := TacticNames(tactics.FamilyWitch)
	if !reflect.DeepEqual(names, []string{"witch/milk", "witch/rush"}) {
		t.Errorf("TacticNames = %v, want the two registered variants", names)
	}
}
