package life

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// witchRushSpy wraps the rush tactic and counts what the profile offered it
// and what it took, so a decline behind several gates is a number, not
// silence.
type witchRushSpy struct {
	witchRush
	offered, handled int
}

func (s *witchRushSpy) Engage(ctx context.Context, f *Fight) (bool, error) {
	s.offered++
	handled, err := s.witchRush.Engage(ctx, f)
	if handled {
		s.handled++
	}
	return handled, err
}

// witchNamed is a profile that fights witches with the named variant.
func witchNamed(name string) tactics.Profile {
	pr := tactics.NewProfile(name)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyWitch: {name}}
	return pr
}

func TestWitchRushClosesInZigZagLegsThenSwingsWithTheHandFree(t *testing.T) {
	withTactics(t, &witchRush{}, &witchMilk{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.give(1, 360, 1) // the shield sits in the kit; the rush leaves it there
	// Ten blocks out: past melee reach, inside the focus range, so the
	// rush owns the whole walk in.
	witch := mgl32.Vec3{30.5, 1, 20.5}
	r.spawn(80, "minecraft:witch", witch)

	// The walk is leg for leg: every tick before the first swing sends one
	// of the two leg headings off the current direct line, and the floor it
	// crossed drifts that leg's way. Carried momentum smears a tick at each
	// swap, so the drift is read over the whole approach, not tick by tick.
	drift, moved := map[int8]float64{}, map[int8]int{}
	sawSwing := false
	for i := 1; i <= 120; i++ {
		pose, swung := r.a.Pose(), len(r.out.attacks())
		r.step()
		if len(r.out.attacks()) > swung {
			sawSwing = true
			continue
		}
		if sawSwing {
			continue
		}
		in := r.out.inputs()[len(r.out.inputs())-1]
		if !in.InputData.Load(packet.InputFlagUp) {
			t.Fatalf("tick %d: the walk stalled without a swing", i)
		}
		direct := mgl32.Vec2{witch.X() - pose.Feet.X(), witch.Z() - pose.Feet.Z()}.Normalize()
		legs := [2]float64{yawOf(witchLeg(direct, 1)), yawOf(witchLeg(direct, -1))}
		var side int8
		switch {
		case math.Abs(float64(in.Yaw)-legs[0]) < 0.01:
			side = 1
		case math.Abs(float64(in.Yaw)-legs[1]) < 0.01:
			side = -1
		default:
			t.Fatalf("tick %d: walked yaw %.2f, want one of the leg headings %.2f or %.2f", i, in.Yaw, legs[0], legs[1])
		}
		drift[side] += float64(r.a.Pose().Feet.Z() - pose.Feet.Z())
		moved[side]++
	}
	if !sawSwing {
		t.Fatalf("never reached the witch across 120 ticks; feet %v", r.feet())
	}
	for _, side := range []int8{1, -1} {
		if moved[side] < 2 {
			t.Fatalf("%d walking ticks on side %+d: the march never took both lanes", moved[side], side)
		}
		want := 0.8
		if side < 0 {
			want = -0.8
		}
		if d := drift[side]; (side > 0 && d < want) || (side < 0 && d > want) {
			t.Fatalf("side %+d drifted %.2f along z in %d ticks, want real carving on its lane", side, d, moved[side])
		}
	}
	if n := len(r.out.attacks()); n < 5 {
		t.Errorf("%d attacks in 120 ticks once closed, want the cadence kept up", n)
	}
	if n := r.out.uses(1); n != 0 {
		t.Errorf("%d shield raises during the rush, want the hand free", n)
	}
	if n := r.out.releases(1); n != 0 {
		t.Errorf("%d shield releases during the rush, want the hand free", n)
	}
}

func TestWitchRushSwingsOnTheAttackCadenceWithTheKitLeftAlone(t *testing.T) {
	withTactics(t, &witchRush{}, &witchMilk{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.give(1, 360, 1) // the shield sits in the kit; the rush leaves it there
	// Two blocks: in reach from the first tick, so the whole fight is
	// swings and the cadence shows.
	r.spawn(80, "minecraft:witch", mgl32.Vec3{22.5, 1, 20.5})
	for i := 0; i < 60; i++ {
		if !r.step() {
			t.Fatalf("tick %d: not engaged", i)
		}
	}
	if n := len(r.out.attacks()); n != 6 {
		t.Fatalf("%d attacks in 60 ticks, want one every 10 starting the first", n)
	}
	if n := r.out.uses(1); n != 0 {
		t.Errorf("%d shield raises during the swings, want the hand free", n)
	}
	if n := r.out.releases(1); n != 0 {
		t.Errorf("%d shield releases during the swings, want the hand free", n)
	}
	if n := r.out.startUsingInputs(); n != 0 {
		t.Errorf("%d item uses started during the swings, want the hand free", n)
	}
}

func TestWitchRushDeclinesAHoldOrderAndLetsTheRoleFightStandStill(t *testing.T) {
	spy := &witchRushSpy{}
	withTactics(t, spy, &witchMilk{})
	r := newWarrior(t, "striker", start)
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderHold}})
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.spawn(80, "minecraft:witch", mgl32.Vec3{26.5, 1, 20.5})
	for i := 0; i < 30; i++ {
		r.step()
		if d := dist(r.feet(), start); d > 1.0 {
			t.Fatalf("tick %d: the rush walked %.1f blocks under a hold order", i, d)
		}
	}
	if spy.offered == 0 {
		t.Fatal("the rush was never offered the tick")
	}
	if spy.handled != 0 {
		t.Errorf("the rush took %d ticks under a hold order", spy.handled)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks under a hold order", n)
	}
}

func TestWitchRushDeclinesARetreatOrderAndLetsTheRoleFightBackOff(t *testing.T) {
	spy := &witchRushSpy{}
	withTactics(t, spy, &witchMilk{})
	r := newWarrior(t, "striker", start)
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderRetreat}})
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	witch := mgl32.Vec3{22.5, 1, 20.5}
	r.spawn(80, "minecraft:witch", witch)
	for i := 0; i < 40; i++ {
		r.step()
	}
	if spy.offered == 0 {
		t.Fatal("the rush was never offered the tick")
	}
	if spy.handled != 0 {
		t.Errorf("the rush took %d ticks under a retreat order", spy.handled)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks under a retreat order", n)
	}
	if d := dist(r.feet(), witch); d < 5 {
		t.Errorf("%.1f blocks from the witch after retreating, want ground gained", d)
	}
}

func TestWitchRushDeclinesAWitchABandAboveTheWalk(t *testing.T) {
	spy := &witchRushSpy{}
	withTactics(t, spy, &witchMilk{})
	r := newWarrior(t, "striker", start)
	// The leader stands on the witch's own floor, so the threat stays in
	// the squad's focus; the unit below it refuses the march through the
	// walking band.
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{25.5, 6, 20.5}, leaderOK: true})
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.spawn(80, "minecraft:witch", mgl32.Vec3{26.5, 6, 20.5})
	for i := 0; i < 30; i++ {
		r.step()
		if f := r.feet(); math.Abs(float64(f.Y())-1) > 0.05 {
			t.Fatalf("tick %d: the unit left the ground at %v", i, f)
		}
	}
	if spy.offered == 0 {
		t.Fatal("the rush was never offered the tick")
	}
	if spy.handled != 0 {
		t.Errorf("the rush took %d ticks at a witch five blocks up", spy.handled)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks at a witch five blocks up", n)
	}
}

func TestWitchRushMarchesStraightWhenTheLegLanesRunWater(t *testing.T) {
	withTactics(t, &witchRush{}, &witchMilk{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	// Water on both ±45° lanes the whole way, only the straight lane dry:
	// each leg's step is refused and the march holds its lane.
	for x := int32(19); x <= 36; x++ {
		r.w.over[protocol.BlockPos{x, 0, 21}] = "water"
		r.w.over[protocol.BlockPos{x, 0, 19}] = "water"
	}
	witch := mgl32.Vec3{28.5, 1, 20.5}
	r.spawn(80, "minecraft:witch", witch)
	for i := 0; i < 120; i++ {
		r.step()
		f := r.feet()
		if z := math.Abs(float64(f.Z()) - 20.5); z > 0.15 {
			t.Fatalf("tick %d: the march drifted to z=%.2f, want its straight lane", i, f.Z())
		}
		if f.Y() != 1 {
			t.Fatalf("tick %d: feet at y=%.2f, want dry ground", i, f.Y())
		}
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Errorf("never closed the %v blocks the straight way; feet %v", witch, r.feet())
	}
}

func TestTheDefaultProfileNeverHandsTheWitchTacticsTheTick(t *testing.T) {
	rush, milk := &witchRushSpy{}, (&witchMilkSpy{})
	scene := func(r *rig) {
		r.give(0, 303, 1)
		r.spawn(80, "minecraft:witch", mgl32.Vec3{26.5, 1, 20.5})
		r.run(40)
	}
	withTactics(t)
	base := replay(t, "striker", scene, func(*Life) {})

	withTactics(t, rush, milk)
	r := newWarrior(t, "striker", start)
	scene(r)
	if rush.offered != 0 || milk.offered != 0 {
		t.Errorf("the witch tactics were offered %d and %d ticks under the default profile", rush.offered, milk.offered)
	}
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	if !reflect.DeepEqual(got, base) {
		t.Errorf("registering the witch tactics changed the default fight: %d packets, want %d", len(got), len(base))
	}
}

func TestWitchRushLetsAHurtUnitStartItsMeal(t *testing.T) {
	withTactics(t, &witchRush{}, &witchMilk{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.give(3, 363, 2)
	r.setHealth(8)
	// Far enough that the role heals and the rush would otherwise be marching.
	r.spawn(80, "minecraft:witch", mgl32.Vec3{29.5, 1, 20.5})
	r.run(15)

	if r.out.uses(3) == 0 {
		t.Error("never started the meal a hurt unit wanted")
	}
}

func TestWitchRushDoesNotMarchOverAOneBlockWaterStrip(t *testing.T) {
	withTactics(t, &witchRush{}, &witchMilk{})
	r := newWarrior(t, "striker", mgl32.Vec3{20.8, 1, 20.5})
	if err := r.l.SetProfile(witchNamed("witch/rush")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.waterStripX(21)
	r.spawn(80, "minecraft:witch", mgl32.Vec3{30.5, 1, 20.5})
	for i := 0; i < 40; i++ {
		r.step()
		if f := r.feet(); (f.X() >= 21 && f.X() < 22) || f.Y() != 1 || r.inWater() {
			t.Fatalf("tick %d: feet at %v, marched into the water strip", i, f)
		}
	}
}

func TestWitchRushLetsGoOfAHalfDrawnBow(t *testing.T) {
	withTactics(t, &witchRush{}, &witchMilk{})
	letsGoOfTheBow(t, "minecraft:witch", mgl32.Vec3{28.5, 1, 20.5}, witchNamed("witch/rush"), func(*rig) {})
}
