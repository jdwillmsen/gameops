package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// bossTactic is a profile that fights bosses with the named tactic.
func bossTactic(name string) *tactics.Profile {
	pr := tactics.NewProfile("boss " + name)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyBoss: {name}}
	return &pr
}

// bossRig is a boss warrior with the profile set, if any.
func bossRig(t *testing.T, role string, from float32, pr *tactics.Profile) (*rig, *fakeRoster) {
	t.Helper()
	r, squad := newBossWarrior(t, role, arena.Sub(mgl32.Vec3{from, 0, 0}))
	if pr != nil {
		if err := r.l.SetProfile(*pr); err != nil {
			t.Fatal(err)
		}
	}
	return r, squad
}

// vanguardAfterABlow is a Vanguard at the warden whose first blow has just
// landed, so the beat is known; it returns the tick the blow landed on.
func vanguardAfterABlow(t *testing.T, pr *tactics.Profile) (*rig, uint64) {
	t.Helper()
	r, _ := bossRig(t, "vanguard", 2, pr)
	r.run(5)
	r.hurt()
	r.step()
	if r.l.warrior.boss.hitAt == 0 {
		t.Fatal("the landed blow set no beat")
	}
	return r, r.l.warrior.boss.hitAt
}

// packets runs a scenario and returns every packet the unit sent.
func packets(r *rig, run func()) []any {
	run()
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	return got
}

func TestBossTacticsAreRegisteredForTheBossFamily(t *testing.T) {
	got := TacticNames(tactics.FamilyBoss)
	want := []string{bossKiteBurstName, bossStandardName}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boss tactics = %v, want %v", got, want)
	}
	for _, n := range want {
		if _, ok := tacticNamed(n, tactics.FamilyZombie); ok {
			t.Errorf("%s runs against a zombie", n)
		}
	}
}

func TestKiteBurstBacksOutBeforeTheBlowAndSwingsAfterIt(t *testing.T) {
	r, hitAt := vanguardAfterABlow(t, bossTactic(bossKiteBurstName))
	every := wardenProfile.meleeEvery

	swung := len(r.out.attacks())
	for r.l.tick < hitAt+every-kiteLeave-1 {
		r.step()
	}
	if len(r.out.attacks()) == swung {
		t.Fatal("no swing while the warden's melee was recovering")
	}

	for r.l.tick < hitAt+every {
		before := len(r.out.attacks())
		r.step()
		if len(r.out.attacks()) > before {
			t.Fatalf("tick %d of %d: swung with the warden's blow due", r.l.tick-hitAt, every)
		}
	}
	if d := dist(r.feet(), arena); d <= bossReach || d >= wardenBoomRange {
		t.Errorf("%.1f blocks off the warden when its blow came due, want clear of its %.1f reach and well inside the boom", d, bossReach)
	}
	if r.l.warrior.shield {
		t.Error("shield up while backing out")
	}
}

func TestKiteBurstKeepsTheVanguardClearOfEveryDueBlow(t *testing.T) {
	r, hitAt := vanguardAfterABlow(t, bossTactic(bossKiteBurstName))
	every := wardenProfile.meleeEvery
	swings := 0
	for beat := uint64(1); beat <= 5; beat++ {
		for r.l.tick < hitAt+beat*every {
			r.step()
		}
		if d := dist(r.feet(), arena); d <= bossReach {
			t.Fatalf("beat %d: %.1f blocks off the warden at its due tick, inside the %.1f its blow reaches", beat, d, bossReach)
		}
		swings = len(r.out.attacks())
	}
	if swings < 5 {
		t.Errorf("%d swings over five beats, want at least one a beat", swings)
	}
}

func TestKiteBurstStandsOffWhereTheStandardVanguardTradesBlows(t *testing.T) {
	std, hitAt := vanguardAfterABlow(t, nil)
	kite, _ := vanguardAfterABlow(t, bossTactic(bossKiteBurstName))
	for std.l.tick < hitAt+wardenProfile.meleeEvery-1 {
		std.step()
	}
	for kite.l.tick < hitAt+wardenProfile.meleeEvery-1 {
		kite.step()
	}
	if d := dist(std.feet(), arena); d > bossReach {
		t.Fatalf("the standard Vanguard stands %.1f blocks off with the blow due, want it at the warden to guard", d)
	}
	if d := dist(kite.feet(), arena); d <= bossReach {
		t.Errorf("the kiting Vanguard stands %.1f blocks off with the blow due, want it clear", d)
	}
}

func TestKiteBurstStrikerWaitsPastTheBoomAndStillJoinsUnderCover(t *testing.T) {
	r, squad := bossRig(t, "striker", 10, bossTactic(bossKiteBurstName))
	delete(squad.members, RoleVanguard)
	r.run(160)
	if d := dist(r.feet(), arena); d < wardenBoomRange+kiteBoomMargin-0.5 || d > 21 {
		t.Fatalf("the waiting Striker stands %.1f blocks off, want just past the %.0f boom", d, wardenBoomRange)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks with no cover to join under", n)
	}

	squad.members[RoleVanguard] = arena.Add(mgl32.Vec3{0, 0, -2})
	r.run(220)
	if n := len(r.out.attacks()); n == 0 {
		t.Error("the Striker never joined the melee with the Vanguard on the boss")
	}
}

func TestKiteBurstDeclinesWhatItHasNoBetterIdeaFor(t *testing.T) {
	scenarios := map[string]struct {
		role string
		from float32
		run  func(*testing.T, *rig, *fakeRoster)
	}{
		"a Vanguard before any blow set a beat": {role: "vanguard", from: 2, run: func(_ *testing.T, r *rig, _ *fakeRoster) { r.run(60) }},
		"a Ranger":                              {role: "ranger", from: 24, run: func(_ *testing.T, r *rig, _ *fakeRoster) { r.run(100) }},
		"a Striker fit to join under cover":     {role: "striker", from: 10, run: func(_ *testing.T, r *rig, _ *fakeRoster) { r.run(120) }},
		"a Striker too hurt to join": {role: "striker", from: 10, run: func(_ *testing.T, r *rig, _ *fakeRoster) {
			r.setHealth(12)
			r.run(120)
		}},
		"a Striker with the squad two down": {role: "striker", from: 10, run: func(_ *testing.T, r *rig, squad *fakeRoster) {
			squad.down, squad.start, squad.startOK = 2, mgl32.Vec3{10.5, 1, 40.5}, true
			r.run(200)
		}},
		"a Vanguard under a hold order": {role: "vanguard", from: 2, run: func(_ *testing.T, r *rig, squad *fakeRoster) {
			squad.order = Order{Kind: OrderHold}
			r.run(5)
			r.hurt()
			r.run(60)
		}},
		"a Vanguard ordered to retreat": {role: "vanguard", from: 2, run: func(_ *testing.T, r *rig, squad *fakeRoster) {
			r.run(5)
			r.hurt()
			r.step()
			squad.order = Order{Kind: OrderRetreat}
			r.run(60)
		}},
		"a Vanguard with a beat that needs to eat": {role: "vanguard", from: 2, run: func(_ *testing.T, r *rig, _ *fakeRoster) {
			r.run(5)
			r.hurt()
			r.step()
			r.moveTo(90, arena.Add(mgl32.Vec3{12, 0, 0}))
			r.give(3, 363, 2)
			r.setHealth(10)
			r.run(40)
		}},
		"a Vanguard with a beat and the squad two down": {role: "vanguard", from: 2, run: func(_ *testing.T, r *rig, squad *fakeRoster) {
			r.run(5)
			r.hurt()
			r.step()
			squad.down, squad.start, squad.startOK = 2, mgl32.Vec3{10.5, 1, 40.5}, true
			r.run(120)
		}},
	}
	for name, sc := range scenarios {
		t.Run(name, func(t *testing.T) {
			var runs [2][]any
			for i, pr := range []*tactics.Profile{nil, bossTactic(bossKiteBurstName)} {
				r, squad := bossRig(t, sc.role, sc.from, pr)
				runs[i] = packets(r, func() { sc.run(t, r, squad) })
			}
			if len(runs[0]) == 0 {
				t.Fatal("the scenario sent nothing")
			}
			if !reflect.DeepEqual(runs[0], runs[1]) {
				t.Errorf("kiteburst changed %d packets of a fight it has no better idea for", len(runs[0]))
			}
		})
	}
}

func TestKiteBurstLeavesBossesWithoutAMeleeBeatAlone(t *testing.T) {
	for _, boss := range []string{"minecraft:wither", "minecraft:elder_guardian"} {
		t.Run(boss, func(t *testing.T) {
			var runs [2][]any
			for i, pr := range []*tactics.Profile{nil, bossTactic(bossKiteBurstName)} {
				r, _ := bossRig(t, "vanguard", 2, pr)
				r.spawn(91, boss, arena.Add(mgl32.Vec3{0, 0, 3}))
				r.l.Apply(&packet.RemoveActor{EntityUniqueID: 90})
				runs[i] = packets(r, func() {
					r.run(5)
					r.hurt()
					r.run(80)
				})
			}
			if !reflect.DeepEqual(runs[0], runs[1]) {
				t.Error("kiteburst changed a fight against a boss with no melee beat")
			}
		})
	}
}

func TestTheDefaultProfileAndTheStandardTacticNeverKite(t *testing.T) {
	run := func(pr *tactics.Profile) []any {
		r, hitAt := vanguardAfterABlow(t, pr)
		return packets(r, func() {
			for r.l.tick < hitAt+3*wardenProfile.meleeEvery {
				r.step()
			}
		})
	}
	base := run(nil)
	if !reflect.DeepEqual(run(bossTactic(bossStandardName)), base) {
		t.Error("naming the standard tactic changed the fight")
	}
	zombieOnly := tactics.NewProfile("kite the zombies")
	zombieOnly.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {bossKiteBurstName}}
	if !reflect.DeepEqual(run(&zombieOnly), base) {
		t.Error("a boss tactic named for the zombie family changed the boss fight")
	}
	if reflect.DeepEqual(run(bossTactic(bossKiteBurstName)), base) {
		t.Error("kiteburst fought exactly as the default, so the comparison above proves nothing")
	}
}
