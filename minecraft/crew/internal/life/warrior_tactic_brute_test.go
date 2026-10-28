package life

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// usingBruteTactic is a profile that fights the brute family with the named
// tactic and the shipped tuning.
func usingBruteTactic(name string) tactics.Profile {
	pr := tactics.NewProfile("tactic " + name)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyBrute: {name}}
	return pr
}

func TestBruteTacticsCoverTheFamiliesHeavyHitters(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	want := map[string]tactics.Family{
		"minecraft:piglin_brute": tactics.FamilyBrute,
		"minecraft:hoglin":       tactics.FamilyBrute,
		"minecraft:zoglin":       tactics.FamilyBrute,
		"minecraft:ravager":      tactics.FamilyBrute,
		"minecraft:iron_golem":   tactics.FamilyBrute,
	}
	for typ, fam := range want {
		if got := familyOf(belief.Entity{Type: typ}); got != fam {
			t.Errorf("familyOf(%s) = %s, want %s", typ, got, fam)
		}
	}
	if got := TacticNames(tactics.FamilyBrute); !reflect.DeepEqual(got, []string{"brute/pillar", "brute/sidestep"}) {
		t.Errorf("brute tactics = %v, want both variants", got)
	}
}

// rushFrom closes a brute on the unit by step blocks a tick.
func rushFrom(r *rig, id uint64, from mgl32.Vec3, step float32) {
	for i := 0; i < 10; i++ {
		from = from.Sub(mgl32.Vec3{step, 0, 0})
		r.moveTo(id, from)
		r.step()
	}
}

func TestBruteSidestepLeavesTheLineAndStrikesAfterTheWhiff(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingBruteTactic("brute/sidestep")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1) // diamond sword
	r.spawn(50, "minecraft:hoglin", mgl32.Vec3{26.5, 1, 20.5})
	r.step() // the fight is on and the unit is closing

	// The gore starts six blocks out and crosses the last of them in about
	// five ticks, leaving the unit inside the sidestep window.
	z0 := r.feet().Z()
	off := func() float64 { return math.Abs(float64(r.feet().Z() - z0)) }
	pos := mgl32.Vec3{26.5, 1, 20.5}
	attacks := len(r.out.attacks())
	largest := 0.0
	for i := 0; i < 5; i++ {
		pos = pos.Sub(mgl32.Vec3{0.6, 0, 0})
		r.moveTo(50, pos)
		r.step()
		largest = math.Max(largest, off())
	}
	// The brute has stopped, but the window runs on: still no swing while
	// the gore is live.
	for i := 0; i < 7; i++ {
		r.step()
		largest = math.Max(largest, off())
	}
	if n := len(r.out.attacks()) - attacks; n != 0 {
		t.Fatalf("%d swings during the rush, want none while the gore is live", n)
	}
	if largest < 0.6 {
		t.Fatalf("sidestepped %.2f blocks, want the unit off the gore's line", largest)
	}

	// The whiff is the opening.
	r.run(50)
	if n := len(r.out.attacks()); n <= attacks {
		t.Fatalf("%d swings after the rush, want the opening taken", n-attacks)
	}
}

func TestBruteSidestepDeclinesUnderAHoldOrder(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	r := newWarrior(t, "striker", start)
	r.setSquad(t, &fakeSquad{order: Order{Kind: OrderHold}})
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:hoglin", mgl32.Vec3{23.5, 1, 20.5})
	r.step()

	e, ok := r.l.ents.Get(50)
	if !ok {
		t.Fatal("the hoglin is not in the entity set")
	}
	before := len(r.out.inputs())
	handled, err := bruteSidestep{}.Engage(context.Background(), &Fight{
		Life: r.l, Actuator: r.a, Pose: r.a.Pose(), Target: e,
		Snapshot: r.tracker.Snapshot(), Params: tactics.Defaults(), Family: tactics.FamilyBrute,
	})
	if handled || err != nil {
		t.Fatalf("handled = %v, err = %v under a hold order", handled, err)
	}
	if got := len(r.out.inputs()) - before; got != 0 {
		t.Fatalf("a declined tick sent %d inputs, want none", got)
	}
}

func TestBruteSidestepDeclinesANonBruteFamily(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:hoglin", mgl32.Vec3{23.5, 1, 20.5})
	r.step()
	e, ok := r.l.ents.Get(50)
	if !ok {
		t.Fatal("the hoglin is not in the entity set")
	}
	before := len(r.out.inputs())
	handled, err := bruteSidestep{}.Engage(context.Background(), &Fight{
		Life: r.l, Actuator: r.a, Pose: r.a.Pose(), Target: e,
		Snapshot: r.tracker.Snapshot(), Params: tactics.Defaults(), Family: tactics.FamilyZombie,
	})
	if handled || err != nil || len(r.out.inputs()) != before {
		t.Fatalf("handled = %v, err = %v, inputs +%d for a family it does not own",
			handled, err, len(r.out.inputs())-before)
	}
}

func TestBruteSidestepHoldsTheShieldBetweenSwingsAtARavager(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingBruteTactic("brute/sidestep")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1) // diamond sword
	r.give(1, 360, 1) // shield
	r.spawn(50, "minecraft:ravager", mgl32.Vec3{23.5, 1, 20.5})
	r.run(60)
	if n := len(r.out.attacks()); n < 2 {
		t.Fatalf("%d swings at the ravager, want the cadence kept", n)
	}
	if n := r.out.uses(1); n < 2 {
		t.Errorf("%d shield raises, want one between each pair of swings", n)
	}
	if n := r.out.releases(1); n < 2 {
		t.Errorf("%d shield lowers, want one before each swing", n)
	}
}

func TestBrutePillarGivesGroundBehindAMateAndSwingsNothing(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingBruteTactic("brute/pillar")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.setHealth(10) // below HealBelow, above the run-for-it line
	r.spawn(50, "minecraft:hoglin", mgl32.Vec3{23.5, 1, 20.5})
	r.player(60, mgl32.Vec3{18.5, 1, 20.5})

	r.run(40)
	if x := r.feet().X(); x >= 18.0 {
		t.Fatalf("unit at x = %.1f, want it past the mate at 18.5 and out of the brute's reach", x)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d swings while pillaring, want the hurt unit out of the fight", n)
	}
}

func TestBrutePillarDeclinesHealthyAndAlone(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	cases := []struct {
		name   string
		health float32
		mate   bool
	}{
		{"healthy", 20, true},
		{"no mate", 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newWarrior(t, "striker", start)
			r.give(0, 303, 1)
			r.setHealth(tc.health)
			r.spawn(50, "minecraft:hoglin", mgl32.Vec3{23.5, 1, 20.5})
			if tc.mate {
				r.player(60, mgl32.Vec3{18.5, 1, 20.5})
			}
			r.step() // set the tick's fight mode and heal decision

			e, ok := r.l.ents.Get(50)
			if !ok {
				t.Fatal("the hoglin is not in the entity set")
			}
			before := len(r.out.inputs())
			handled, err := brutePillar{}.Engage(context.Background(), &Fight{
				Life: r.l, Actuator: r.a, Pose: r.a.Pose(), Target: e,
				Snapshot: r.tracker.Snapshot(), Params: r.l.warrior.p, Family: tactics.FamilyBrute,
			})
			if handled || err != nil {
				t.Fatalf("handled = %v, err = %v", handled, err)
			}
			if got := len(r.out.inputs()) - before; got != 0 {
				t.Fatalf("a declined tick sent %d inputs, want none", got)
			}
		})
	}
}

func TestTheDefaultProfileNeverPicksABruteTactic(t *testing.T) {
	charge := func(r *rig) {
		r.give(0, 303, 1)
		r.spawn(50, "minecraft:hoglin", mgl32.Vec3{26.5, 1, 20.5})
		rushFrom(r, 50, mgl32.Vec3{26.5, 1, 20.5}, 0.6)
		r.run(30)
	}
	hurt := func(r *rig) {
		r.give(0, 303, 1)
		r.setHealth(10)
		r.spawn(50, "minecraft:hoglin", mgl32.Vec3{23.5, 1, 20.5})
		r.player(60, mgl32.Vec3{18.5, 1, 20.5})
		r.run(50)
	}
	for name, scenario := range map[string]func(*rig){"charge": charge, "hurt": hurt} {
		t.Run(name, func(t *testing.T) {
			withTactics(t, bruteSidestep{}, brutePillar{})
			got := replay(t, "striker", scenario, nil)
			withTactics(t)
			base := replay(t, "striker", scenario, nil)
			if len(base) == 0 {
				t.Fatal("the unprofiled scenario sent nothing")
			}
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("%d packets differ from the unprofiled %d: a registered tactic changed the default fight",
					len(got), len(base))
			}
		})
	}
}

func TestBruteSidestepLetsGoOfAHalfDrawnBow(t *testing.T) {
	withTactics(t, bruteSidestep{}, brutePillar{})
	letsGoOfTheBow(t, "minecraft:hoglin", mgl32.Vec3{28.5, 1, 20.5}, usingBruteTactic("brute/sidestep"), func(*rig) {})
}

func TestBruteMateIsNeverAnEnemyPlayer(t *testing.T) {
	r := newWarrior(t, "striker", start)
	pol := newPvP("rival")
	pol.live = true
	r.l.SetPvPPolicy(pol)
	r.playerNamed(60, "rival", mgl32.Vec3{18.5, 1, 20.5})
	if _, ok := r.l.bruteMate(r.a.Pose(), 12); ok {
		t.Fatal("an enemy player counted as a mate to pass the brute to")
	}
	r.playerNamed(61, "friend", mgl32.Vec3{16.5, 1, 20.5})
	got, ok := r.l.bruteMate(r.a.Pose(), 12)
	if !ok || got.X() != 16.5 {
		t.Errorf("mate = %v, %v, want the friendly player at 16.5", got, ok)
	}
}
