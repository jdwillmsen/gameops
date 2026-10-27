package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestFamilyOfClassifiesEveryFamily(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		player bool
		want   tactics.Family
	}{
		{typ: "minecraft:zombie", want: tactics.FamilyZombie},
		{typ: "minecraft:husk", want: tactics.FamilyZombie},
		{typ: "minecraft:drowned", want: tactics.FamilyZombie},
		{typ: "minecraft:zombie_villager_v2", want: tactics.FamilyZombie},
		{typ: "minecraft:zombie_pigman", want: tactics.FamilyZombie},
		{typ: "minecraft:skeleton", want: tactics.FamilySkeleton},
		{typ: "minecraft:stray", want: tactics.FamilySkeleton},
		{typ: "minecraft:bogged", want: tactics.FamilySkeleton},
		{typ: "minecraft:wither_skeleton", want: tactics.FamilySkeleton},
		{typ: "minecraft:phantom", want: tactics.FamilyZombie},
		{typ: "minecraft:creeper", want: tactics.FamilyCreeper},
		{typ: "minecraft:spider", want: tactics.FamilySpider},
		{typ: "minecraft:cave_spider", want: tactics.FamilySpider},
		{typ: "minecraft:blaze", want: tactics.FamilyBlaze},
		{typ: "minecraft:breeze", want: tactics.FamilyBlaze},
		{typ: "minecraft:enderman", want: tactics.FamilyEnderman},
		{typ: "minecraft:witch", want: tactics.FamilyWitch},
		{typ: "minecraft:pillager", want: tactics.FamilyIllager},
		{typ: "minecraft:vindicator", want: tactics.FamilyIllager},
		{typ: "minecraft:evocation_illager", want: tactics.FamilyIllager},
		{typ: "minecraft:vex", want: tactics.FamilyIllager},
		{typ: "minecraft:piglin_brute", want: tactics.FamilyBrute},
		{typ: "minecraft:ravager", want: tactics.FamilyBrute},
		{typ: "minecraft:hoglin", want: tactics.FamilyBrute},
		{typ: "minecraft:warden", want: tactics.FamilyBoss},
		{typ: "minecraft:wither", want: tactics.FamilyBoss},
		{typ: "minecraft:elder_guardian", want: tactics.FamilyBoss},
		{typ: "player", player: true, want: tactics.FamilyPlayer},
		{typ: "minecraft:slime", want: tactics.FamilyDefault},
		{typ: "minecraft:cow", want: tactics.FamilyDefault},
		{typ: "", want: tactics.FamilyDefault},
	} {
		if got := familyOf(belief.Entity{ID: 9, Type: tc.typ, Player: tc.player}); got != tc.want {
			t.Errorf("familyOf(%q, player=%v) = %q, want %q", tc.typ, tc.player, got, tc.want)
		}
	}
}

// The engine's constants and the contract's defaults are two copies of the
// same numbers; a unit with no profile must fight exactly as it shipped.
func TestTacticsDefaultsAreTheShippedConstants(t *testing.T) {
	want := tactics.Params{
		FocusRange: focusRange, HealBelow: healBelow, HealSafe: healSafe,
		PressRange: pressRange, RegroupWithin: regroupWithin, KiteBackoff: kiteBackoff,
		ShotEvery: shotEvery, ArrowNear: arrowNear, LineWidth: lineWidth,
		OrbitMin: orbitMin, CritWindow: critWindow, MeleeShieldTick: meleeShieldTicks,
	}
	if got := tactics.Defaults(); got != want {
		t.Fatalf("tactics.Defaults() = %+v, want the engine constants %+v", got, want)
	}
	if got := tactics.Defaults().Clamp(); got != want {
		t.Fatalf("the shipped constants fall outside tactics.Bounds: clamped to %+v", got)
	}
}

func TestParamsForResolvesTheTargetsFamily(t *testing.T) {
	l := New(Config{Role: "ranger"}, nil, nil, nil, nil)
	zombie := belief.Entity{ID: 50, Type: "minecraft:zombie"}
	skeleton := belief.Entity{ID: 51, Type: "minecraft:skeleton"}

	if got := l.paramsFor(zombie); got != tactics.Defaults() {
		t.Fatalf("no profile set: params %+v, want the defaults", got)
	}

	pr := tactics.NewProfile("lab")
	kite := tactics.Defaults()
	kite.KiteBackoff = 8
	pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyZombie: kite}
	if err := l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	if got := l.paramsFor(zombie).KiteBackoff; got != 8 {
		t.Errorf("zombie kiteBackoff = %v, want the override's 8", got)
	}
	if got := l.paramsFor(skeleton).KiteBackoff; got != kiteBackoff {
		t.Errorf("skeleton kiteBackoff = %v, want the default %v", got, float64(kiteBackoff))
	}
	if got := l.paramsFor(belief.Entity{}); got != pr.Default {
		t.Errorf("no target: params %+v, want the profile default", got)
	}
}

func TestSetProfileClampsCopiesAndRefusesAnUnreadableProfile(t *testing.T) {
	l := New(Config{Role: "striker"}, nil, nil, nil, nil)
	zombie := belief.Entity{ID: 50, Type: "minecraft:zombie"}

	wild := tactics.Defaults()
	wild.ShotEvery = 0
	overrides := map[tactics.Family]tactics.Params{tactics.FamilyZombie: wild}
	pr := tactics.NewProfile("wild")
	pr.Overrides = overrides
	if err := l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	if got := l.paramsFor(zombie).ShotEvery; got != tactics.Bounds()["shotEvery"].Min {
		t.Errorf("shotEvery 0 loaded as %v, want it clamped to the bound", got)
	}
	if overrides[tactics.FamilyZombie].ShotEvery != 0 {
		t.Error("SetProfile wrote to the caller's overrides")
	}
	overrides[tactics.FamilyZombie] = tactics.Defaults()
	if got := l.paramsFor(zombie).ShotEvery; got == shotEvery {
		t.Error("a later write to the caller's map reached the profile in force")
	}

	stale := tactics.NewProfile("stale")
	stale.Version = tactics.Version + 1
	if err := l.SetProfile(stale); err == nil {
		t.Fatal("a profile of another version was accepted")
	}
	if got := l.Profile().Name; got != "wild" {
		t.Errorf("profile in force = %q after a refused swap, want the old one kept", got)
	}
}

// fightScenarios script fights that between them reach every tunable: the
// kite, the cadence, the crit jump, circling, the shooter's line, a landed
// blow, arrows, the heal and the regroup.
var fightScenarios = map[string]func(r *rig){
	"ranger kiting a walking zombie": func(r *rig) {
		r.give(0, 303, 1)
		r.give(1, 361, 1)
		r.give(20, 362, 32)
		z := mgl32.Vec3{26.5, 1, 20.5}
		r.spawn(50, "minecraft:zombie", z)
		for i := 0; i < 120; i++ {
			z = z.Sub(mgl32.Vec3{0.1, 0, 0})
			r.moveTo(50, z)
			r.step()
		}
	},
	"striker on a zombie under a skeleton's line, arrows and blows": func(r *rig) {
		r.setSquad(r.t, &fakeSquad{leaderID: "lead", leader: mgl32.Vec3{12.5, 1, 20.5}, leaderOK: true})
		r.give(0, 303, 1)
		r.give(2, 360, 1)
		r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
		r.spawn(51, "minecraft:skeleton", mgl32.Vec3{28.5, 1, 20.5})
		r.spawn(70, "minecraft:arrow", mgl32.Vec3{30, 30, 30})
		var arrow mgl32.Vec3
		for i := 0; i < 160; i++ {
			if i%60 == 0 {
				r.hurt()
			}
			// An arrow loosed at the unit every second or so, from far
			// enough out that how near counts as near decides when the
			// shield meets it.
			if i%25 == 0 {
				arrow = r.feet().Add(mgl32.Vec3{7, 1, 0})
			}
			arrow = arrow.Sub(mgl32.Vec3{0.5, 0, 0})
			r.moveTo(70, arrow)
			r.step()
		}
	},
	"a zombie walking in from beyond focus range": func(r *rig) {
		r.give(0, 303, 1)
		z := mgl32.Vec3{36.5, 1, 20.5}
		r.spawn(50, "minecraft:zombie", z)
		for i := 0; i < 80; i++ {
			z = z.Sub(mgl32.Vec3{0.05, 0, 0})
			r.moveTo(50, z)
			r.step()
		}
	},
	"hurt with a zombie just out of arm's reach": func(r *rig) {
		r.give(0, 303, 1)
		r.give(3, 363, 2)
		r.setHealth(10)
		r.spawn(50, "minecraft:zombie", mgl32.Vec3{24, 1, 20.5})
		r.run(30)
	},
	"hurt striker pressed, then eating, then regrouping": func(r *rig) {
		squad := &fakeSquad{leaderID: "lead", leader: mgl32.Vec3{10.5, 1, 20.5}, leaderOK: true}
		r.setSquad(r.t, squad)
		r.give(0, 303, 1)
		r.give(3, 363, 2)
		r.setHealth(10)
		r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
		for i := 0; i < 80; i++ {
			r.step()
			r.moveTo(50, r.feet().Add(mgl32.Vec3{1.2, 0, 0}))
		}
		squad.order = Order{Kind: OrderRegroup}
		r.moveTo(50, mgl32.Vec3{30.5, 1, 30.5})
		r.run(60)
	},
}

// replay runs a scenario for a role with the profile set, and returns every
// packet the unit sent.
func replay(t *testing.T, role string, scenario func(*rig), set func(*Life)) []any {
	t.Helper()
	r := newWarrior(t, role, start)
	if set != nil {
		set(r.l)
	}
	scenario(r)
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	return got
}

func TestTheDefaultProfileFightsExactlyAsTheShippedConstants(t *testing.T) {
	everyFamily := tactics.NewProfile("every family at the defaults")
	everyFamily.Overrides = map[tactics.Family]tactics.Params{}
	for _, f := range []tactics.Family{tactics.FamilyDefault, tactics.FamilyZombie, tactics.FamilySkeleton,
		tactics.FamilyCreeper, tactics.FamilySpider, tactics.FamilyBlaze, tactics.FamilyEnderman,
		tactics.FamilyWitch, tactics.FamilyIllager, tactics.FamilyBrute, tactics.FamilyBoss, tactics.FamilyPlayer} {
		everyFamily.Overrides[f] = tactics.Defaults()
	}
	profiles := map[string]tactics.Profile{
		"explicit default":            tactics.NewProfile("explicit"),
		"every family at the default": everyFamily,
	}
	for name, scenario := range fightScenarios {
		for _, role := range []string{"vanguard", "striker", "ranger"} {
			base := replay(t, role, scenario, nil)
			if len(base) == 0 {
				t.Fatalf("%s as %s sent nothing", name, role)
			}
			for pname, pr := range profiles {
				got := replay(t, role, scenario, func(l *Life) {
					if err := l.SetProfile(pr); err != nil {
						t.Fatal(err)
					}
				})
				if !reflect.DeepEqual(got, base) {
					t.Errorf("%s as %s with %s: %d packets differ from the unprofiled %d",
						name, role, pname, len(got), len(base))
				}
			}
		}
	}
}

// The equivalence above means something only if each tunable reaches the
// packets: nudge one at a time and some scenario must fight differently.
func TestFightScenariosReachEveryTunable(t *testing.T) {
	for _, n := range tactics.Names() {
		pr := tactics.NewProfile("nudged " + n)
		v, _ := pr.Default.Get(n)
		nudged := v * 1.4
		if nudged > tactics.Bounds()[n].Max {
			nudged = v * 0.7
		}
		pr.Default.Set(n, nudged)
		reached := false
		for _, scenario := range fightScenarios {
			for _, role := range []string{"vanguard", "striker", "ranger"} {
				base := replay(t, role, scenario, nil)
				got := replay(t, role, scenario, func(l *Life) {
					if err := l.SetProfile(pr); err != nil {
						t.Fatal(err)
					}
				})
				if !reflect.DeepEqual(got, base) {
					reached = true
				}
			}
		}
		if !reached {
			t.Errorf("%s moved from %v to %v and no scenario fought differently", n, v, nudged)
		}
	}
}

// kiteRig is a ranger with a zombie standing 3.5 blocks off: inside the
// default kiting range, outside a short one.
func kiteRig(t *testing.T) *rig {
	r := newWarrior(t, "ranger", start)
	r.give(0, 303, 1)
	r.give(1, 361, 1)
	r.give(20, 362, 32)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{24, 1, 20.5})
	return r
}

// shortKite closes the kiting range against zombies only.
func shortKite() tactics.Profile {
	pr := tactics.NewProfile("short kite")
	p := tactics.Defaults()
	p.KiteBackoff = 2.5
	pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyZombie: p}
	return pr
}

func TestAFamilyOverrideChangesTheFightAgainstThatFamilyOnly(t *testing.T) {
	r := kiteRig(t)
	r.run(30)
	if dx := r.feet().X() - start.X(); dx > -0.5 {
		t.Fatalf("default profile: the Ranger held its ground (%.1f along x) with a zombie inside kiting range", dx)
	}

	r = kiteRig(t)
	if err := r.l.SetProfile(shortKite()); err != nil {
		t.Fatal(err)
	}
	r.run(30)
	if dx := r.feet().X() - start.X(); dx < -0.1 {
		t.Errorf("short-kite profile: the Ranger gave %.1f blocks to a zombie outside its kiting range", -dx)
	}
	if r.out.releases(1) == 0 {
		t.Error("short-kite profile: the Ranger never shot")
	}

	r = newWarrior(t, "ranger", start)
	r.give(1, 361, 1)
	r.give(20, 362, 32)
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{24, 1, 20.5})
	if err := r.l.SetProfile(shortKite()); err != nil {
		t.Fatal(err)
	}
	r.run(30)
	if dx := r.feet().X() - start.X(); dx > -0.5 {
		t.Errorf("a zombie-only override changed the fight against a skeleton: held at %.1f along x", dx)
	}
}

func TestAProfileSwappedMidFightTakesHoldOnTheNextTick(t *testing.T) {
	r := kiteRig(t)
	if err := r.l.SetProfile(shortKite()); err != nil {
		t.Fatal(err)
	}
	r.run(20)
	held := r.feet()
	if r.l.warrior.p.KiteBackoff != 2.5 {
		t.Fatalf("kiteBackoff = %v before the swap, want the override's 2.5", r.l.warrior.p.KiteBackoff)
	}

	// The swap comes from another goroutine while the fight steps on, as a
	// delivery client's would; the race detector is what checks it.
	done := make(chan error)
	go func() { done <- r.l.SetProfile(tactics.NewProfile("default again")) }()
	swapped := false
	for i := 0; i < 1000 && !swapped; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			swapped = true
		default:
			r.step()
		}
	}
	if !swapped {
		t.Fatal("the swap never landed")
	}
	r.step()
	if got := r.l.warrior.p.KiteBackoff; got != kiteBackoff {
		t.Fatalf("kiteBackoff = %v the tick after the swap, want the default %v", got, float64(kiteBackoff))
	}
	r.run(20)
	if d := r.feet().X() - held.X(); d > -0.5 {
		t.Errorf("after the swap the Ranger moved %.1f along x, want it giving ground", d)
	}
	if !r.step() {
		t.Error("the swap ended the fight")
	}
}
