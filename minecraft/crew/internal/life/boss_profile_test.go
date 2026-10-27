package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// bossScenarios each exercise one tunable the boss fight reads.
var bossScenarios = map[string]struct {
	role string
	from float32
	run  func(*rig, *fakeRoster)
}{
	"ranger shooting from its band": {role: "ranger", from: 24, run: func(r *rig, _ *fakeRoster) { r.run(130) }},
	"hurt striker eating off the boss": {role: "striker", from: 10, run: func(r *rig, _ *fakeRoster) {
		r.give(3, 363, 2)
		r.setHealth(16)
		r.run(60)
	}},
	"striker regrouping to the start": {role: "striker", from: 10, run: func(r *rig, squad *fakeRoster) {
		squad.down, squad.start, squad.startOK = 2, mgl32.Vec3{10.5, 1, 40.5}, true
		r.run(250)
	}},
	"vanguard holding the melee": {role: "vanguard", from: 2, run: func(r *rig, _ *fakeRoster) { r.run(100) }},
}

// replayBoss runs a boss scenario with the profile set and returns every
// packet the unit sent.
func replayBoss(t *testing.T, name string, pr *tactics.Profile) []any {
	t.Helper()
	sc := bossScenarios[name]
	r, squad := newBossWarrior(t, sc.role, arena.Sub(mgl32.Vec3{sc.from, 0, 0}))
	if pr != nil {
		if err := r.l.SetProfile(*pr); err != nil {
			t.Fatal(err)
		}
	}
	sc.run(r, squad)
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	return got
}

func bossProfileWith(f tactics.Family, set func(*tactics.Params)) *tactics.Profile {
	pr := tactics.NewProfile("boss " + string(f))
	p := tactics.Defaults()
	set(&p)
	pr.Overrides = map[tactics.Family]tactics.Params{f: p}
	return &pr
}

func TestTheDefaultProfileFightsTheBossAsShipped(t *testing.T) {
	explicit := tactics.NewProfile("explicit")
	atDefaults := bossProfileWith(tactics.FamilyBoss, func(*tactics.Params) {})
	for name := range bossScenarios {
		base := replayBoss(t, name, nil)
		if len(base) == 0 {
			t.Fatalf("%s: sent nothing", name)
		}
		for pname, pr := range map[string]*tactics.Profile{"explicit default": &explicit, "boss at the defaults": atDefaults} {
			if got := replayBoss(t, name, pr); !reflect.DeepEqual(got, base) {
				t.Errorf("%s with %s: %d packets differ from the unprofiled %d", name, pname, len(got), len(base))
			}
		}
	}
}

func TestTheBossFightReadsTheBossOverrideAndNoOtherFamilys(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		set      func(*tactics.Params)
	}{
		{"ranger shooting from its band", func(p *tactics.Params) { p.ShotEvery = 60 }},
		{"hurt striker eating off the boss", func(p *tactics.Params) { p.HealBelow = 18 }},
		{"striker regrouping to the start", func(p *tactics.Params) { p.RegroupWithin = 6 }},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			base := replayBoss(t, tc.scenario, nil)
			if got := replayBoss(t, tc.scenario, bossProfileWith(tactics.FamilyBoss, tc.set)); reflect.DeepEqual(got, base) {
				t.Error("a boss override changed nothing about the boss fight")
			}
			if got := replayBoss(t, tc.scenario, bossProfileWith(tactics.FamilyZombie, tc.set)); !reflect.DeepEqual(got, base) {
				t.Error("a zombie override changed the boss fight")
			}
		})
	}
}

func TestBossShotEveryPacesTheRangersArrows(t *testing.T) {
	shots := func(pr *tactics.Profile) int {
		r, _ := newBossWarrior(t, "ranger", arena.Sub(mgl32.Vec3{24, 0, 0}))
		if pr != nil {
			if err := r.l.SetProfile(*pr); err != nil {
				t.Fatal(err)
			}
		}
		r.run(200)
		return r.out.releases(2)
	}
	fast, slow := shots(nil), shots(bossProfileWith(tactics.FamilyBoss, func(p *tactics.Params) { p.ShotEvery = 60 }))
	if slow >= fast || slow == 0 {
		t.Errorf("%d arrows at shotEvery 60 against %d at the default, want fewer but some", slow, fast)
	}
}

func TestBossHealBelowSetsWhenAWarriorEatsAtTheBoss(t *testing.T) {
	eats := func(pr *tactics.Profile) bool {
		r, _ := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{10, 0, 0}))
		if pr != nil {
			if err := r.l.SetProfile(*pr); err != nil {
				t.Fatal(err)
			}
		}
		r.give(3, 363, 2)
		r.setHealth(16)
		r.run(40)
		return r.out.uses(3) > 0
	}
	if eats(nil) {
		t.Error("ate at 16 health under the default heal line of 14")
	}
	if !eats(bossProfileWith(tactics.FamilyBoss, func(p *tactics.Params) { p.HealBelow = 18 })) {
		t.Error("did not eat at 16 health with the boss's heal line at 18")
	}
}

func TestBossHealSafeSetsHowNearAnEnemyLetsGoOfAMeal(t *testing.T) {
	r, _ := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{11, 0, 0}))
	r.spawn(91, "minecraft:zombie", r.feet().Add(mgl32.Vec3{0, 0, 5}))
	p := r.a.Pose()
	r.l.warrior.p = tactics.Defaults()
	if r.l.mealThreatened(p) {
		t.Fatal("a zombie 5 blocks off cancelled a meal under the default heal-safe distance of 3")
	}
	r.l.warrior.p.HealSafe = 6
	if !r.l.mealThreatened(p) {
		t.Error("a zombie 5 blocks off left a meal alone with heal-safe at 6")
	}
}
