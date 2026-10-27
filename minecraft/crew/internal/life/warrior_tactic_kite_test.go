package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// chaser is a scripted melee mob: it walks at the unit, swings when the unit
// is inside its reach with its cooldown spent, and is pushed back by a blow.
// Its numbers are the script's, not Bedrock's; the tactic reads them off the
// fight.
type chaser struct {
	id     uint64
	pos    mgl32.Vec3
	speed  float32
	reach  float64
	every  int
	knock  float32
	cool   int
	hits   int
	landed int
}

func (c *chaser) tick(r *rig) {
	if n := len(r.out.attacks()); n > c.landed {
		c.landed = n
		away := r.feet().Sub(c.pos)
		away = mgl32.Vec3{-away.X(), 0, -away.Z()}.Normalize()
		c.pos = c.pos.Add(away.Mul(c.knock))
	}
	feet := r.feet()
	to := mgl32.Vec3{feet.X() - c.pos.X(), 0, feet.Z() - c.pos.Z()}
	if d := to.Len(); d > 1.0 {
		c.pos = c.pos.Add(to.Normalize().Mul(min(c.speed, float32(d)-1.0)))
	}
	if c.cool > 0 {
		c.cool--
	}
	// The unit's health is held up so the heal reflex never joins the
	// comparison; a dip of one marks the blow.
	if dist(feet, c.pos) <= c.reach && c.cool == 0 {
		c.hits++
		c.cool = c.every
		r.setHealth(19)
		r.hurt()
	} else {
		r.setHealth(20)
	}
	r.moveTo(c.id, c.pos)
}

func newChaser(id uint64, at mgl32.Vec3) *chaser {
	return &chaser{id: id, pos: at, speed: 0.17, reach: 1.9, every: 20, knock: 0.4}
}

func usingKite(fam tactics.Family) tactics.Profile {
	name := map[tactics.Family]string{
		tactics.FamilyZombie: "zombie/kite", tactics.FamilySpider: "spider/kite", tactics.FamilyBrute: "brute/kite",
	}[fam]
	pr := tactics.NewProfile("kite " + name)
	pr.Tactics = map[tactics.Family][]string{fam: {name}}
	return pr
}

// kiteFight runs a warrior of the role against a scripted zombie that
// charges from eight blocks, and reports what the zombie landed on it and
// what it landed on the zombie.
func kiteFight(t *testing.T, role string, pr tactics.Profile, c *chaser, ticks int, setup func(*rig)) (taken, swings int, r *rig) {
	t.Helper()
	r = newWarrior(t, role, mgl32.Vec3{30.5, 1, 20.5})
	r.give(0, 303, 1)
	if setup != nil {
		setup(r)
	}
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	r.setHealth(20)
	r.spawn(c.id, "minecraft:zombie", c.pos)
	for i := 0; i < ticks; i++ {
		r.step()
		c.tick(r)
	}
	return c.hits, len(r.out.attacks()), r
}

func TestKiteTakesFewerHitsThanTheRoleFightOnAScriptedZombieApproach(t *testing.T) {
	for _, role := range []string{"striker", "vanguard", "ranger"} {
		t.Run(role, func(t *testing.T) {
			setup := func(r *rig) { r.give(1, 360, 1) }
			roleTaken, roleSwings, _ := kiteFight(t, role, tactics.NewProfile("role"), newChaser(50, mgl32.Vec3{38.5, 1, 20.5}), 120, setup)
			kiteTaken, kiteSwings, r := kiteFight(t, role, usingKite(tactics.FamilyZombie), newChaser(50, mgl32.Vec3{38.5, 1, 20.5}), 120, setup)
			if roleTaken < 4 {
				t.Fatalf("the role fight took %d hits: the scripted zombie is not a fair test", roleTaken)
			}
			if kiteTaken >= roleTaken {
				t.Errorf("kiting took %d hits, the role fight %d: want fewer", kiteTaken, roleTaken)
			}
			if kiteSwings < roleSwings/2 {
				t.Errorf("kiting swung %d times against the role fight's %d: it must still fight", kiteSwings, roleSwings)
			}
			if r.l.ignore[50] > 0 {
				t.Errorf("kiting gave the zombie up (%s)", r.l.endReason)
			}
		})
	}
}

// A mob as quick as the unit cannot be opened a gap on, so kiting gives the
// backoff up and fights; it must never come out worse than the role fight.
func TestKiteAgainstAMobAsQuickAsTheUnitTakesNoMoreHitsThanTheRoleFight(t *testing.T) {
	mob := func() *chaser {
		c := newChaser(50, mgl32.Vec3{38.5, 1, 20.5})
		c.speed, c.knock = kiteUnitSpeed, 0
		return c
	}
	for _, role := range []string{"striker", "vanguard"} {
		t.Run(role, func(t *testing.T) {
			roleTaken, _, _ := kiteFight(t, role, tactics.NewProfile("role"), mob(), 160, nil)
			kiteTaken, swings, _ := kiteFight(t, role, usingKite(tactics.FamilyZombie), mob(), 160, nil)
			if kiteTaken > roleTaken {
				t.Errorf("kiting took %d hits, the role fight %d", kiteTaken, roleTaken)
			}
			if swings < 6 {
				t.Errorf("%d swings in 160 ticks: kiting stopped fighting", swings)
			}
		})
	}
}

func TestKiteStepsBackFromAMobAboutToSwingAndStrikesOnceItIsClear(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	if err := r.l.SetProfile(usingKite(tactics.FamilyZombie)); err != nil {
		t.Fatal(err)
	}
	z := mgl32.Vec3{21.5, 1, 20.5}
	r.spawn(50, "minecraft:zombie", z)
	r.run(3)
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings with the mob inside its own reach: want the step back first", n)
	}
	if d := dist(r.feet(), z); d <= 1.0 {
		t.Errorf("%.1f blocks from the mob after 3 ticks, want ground gained", d)
	}
	r.run(40)
	if n := len(r.out.attacks()); n == 0 {
		t.Error("never struck the mob once clear of it")
	}
	if d := dist(r.feet(), z); d < 1.6 {
		t.Errorf("ended %.1f blocks from the mob, inside its swing", d)
	}
}

func TestKiteWaitsForAClosingMobInsteadOfWalkingIntoIt(t *testing.T) {
	r := newWarrior(t, "striker", mgl32.Vec3{30.5, 1, 20.5})
	r.give(0, 303, 1)
	if err := r.l.SetProfile(usingKite(tactics.FamilyZombie)); err != nil {
		t.Fatal(err)
	}
	c := newChaser(50, mgl32.Vec3{36.5, 1, 20.5})
	r.spawn(50, "minecraft:zombie", c.pos)
	r.setHealth(20)
	var held float32
	for i := 0; i < 20; i++ {
		r.step()
		c.tick(r)
		if held == 0 && dist(r.feet(), c.pos) <= 4 {
			held = r.feet().X()
		}
		if held != 0 && r.feet().X() > held+0.3 {
			t.Fatalf("tick %d: walked %.1f blocks toward a mob that was coming anyway", i, r.feet().X()-held)
		}
	}
	if held == 0 {
		t.Fatal("the mob never came within four blocks")
	}
}

func TestKiteDoesNotFireUnderTheDefaultProfile(t *testing.T) {
	scenario := func(r *rig) {
		r.give(0, 303, 1)
		c := newChaser(50, mgl32.Vec3{36.5, 1, 20.5})
		r.spawn(50, "minecraft:zombie", c.pos)
		r.setHealth(20)
		for i := 0; i < 80; i++ {
			r.step()
			c.tick(r)
		}
	}
	for _, role := range []string{"vanguard", "striker", "ranger"} {
		withTactics(t)
		base := replay(t, role, scenario, nil)
		withTactics(t, kiteTactic{"zombie/kite", tactics.FamilyZombie}, kiteTactic{"spider/kite", tactics.FamilySpider}, kiteTactic{"brute/kite", tactics.FamilyBrute})
		got := replay(t, role, scenario, nil)
		if len(base) == 0 || !reflect.DeepEqual(got, base) {
			t.Errorf("%s: registering the kite tactics changed the default fight (%d packets, want %d)", role, len(got), len(base))
		}
	}
}

func TestKiteDeclinesWhereTheRoleOrTheSquadHasTheTick(t *testing.T) {
	var second *chaser
	for name, tc := range map[string]struct {
		role  string
		setup func(*rig, *fakeSquad)
	}{
		"ranger with a bow": {"ranger", func(r *rig, _ *fakeSquad) {
			r.give(1, 361, 1)
			r.give(2, 362, 16)
		}},
		"hold order":    {"striker", func(_ *rig, sq *fakeSquad) { sq.order = Order{Kind: OrderHold} }},
		"retreat order": {"vanguard", func(_ *rig, sq *fakeSquad) { sq.order = Order{Kind: OrderRetreat} }},
		"mob beside it": {"striker", func(r *rig, _ *fakeSquad) {
			second = newChaser(51, mgl32.Vec3{31.5, 1, 21.5})
			second.reach = 0
			r.spawn(51, "minecraft:zombie", second.pos)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			scenario := func(pr tactics.Profile) []any {
				second = nil
				r := newWarrior(t, tc.role, mgl32.Vec3{30.5, 1, 20.5})
				sq := &fakeSquad{leaderID: "lead", leader: r.feet(), leaderOK: true}
				r.setSquad(t, sq)
				r.give(0, 303, 1)
				tc.setup(r, sq)
				if err := r.l.SetProfile(pr); err != nil {
					t.Fatal(err)
				}
				c := newChaser(50, mgl32.Vec3{36.5, 1, 20.5})
				r.spawn(50, "minecraft:zombie", c.pos)
				r.setHealth(20)
				for i := 0; i < 60; i++ {
					sq.leader = r.feet()
					r.step()
					c.tick(r)
					if second != nil {
						second.tick(r)
					}
				}
				out := make([]any, len(r.out.got))
				for i, pk := range r.out.got {
					out[i] = pk
				}
				return out
			}
			base := scenario(tactics.NewProfile("role"))
			got := scenario(usingKite(tactics.FamilyZombie))
			if len(base) == 0 || !reflect.DeepEqual(got, base) {
				t.Errorf("kiting changed a fight it had no business in (%d packets, want %d)", len(got), len(base))
			}
		})
	}
}

func TestKiteDoesNotBackIntoAWallItCannotLeave(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			for y := int32(1); y <= 2; y++ {
				r.w.over[protocol.BlockPos{20 + dx, y, 20 + dz}] = "stone"
			}
		}
	}
	if err := r.l.SetProfile(usingKite(tactics.FamilyZombie)); err != nil {
		t.Fatal(err)
	}
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21.5, 1, 20.5})
	r.run(30)
	if len(r.out.attacks()) == 0 {
		t.Fatal("a unit with nowhere to back to never swung: the tactic must hand the fight back")
	}
	st, ok := kiteStates.has(r.l)
	if !ok || st.offUntil == 0 {
		t.Error("kiting was not put off after the backoff found no path")
	}
}

func TestKiteDoesNotRunFromAMobFasterThanItCanWalk(t *testing.T) {
	fast := newChaser(50, mgl32.Vec3{36.5, 1, 20.5})
	fast.speed = 0.4
	_, swings, r := kiteFight(t, "striker", usingKite(tactics.FamilySpider), fast, 80, nil)
	r.l.mode = modeFight
	if swings == 0 {
		t.Error("never swung at the fast mob")
	}
	if d := dist(r.feet(), mgl32.Vec3{30.5, 1, 20.5}); d > 6 {
		t.Errorf("ran %.1f blocks from a mob it cannot outwalk", d)
	}
}

func TestKiteStrikerJumpsForTheCriticalWhileTheMobIsStillFarFromSwinging(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	if err := r.l.SetProfile(usingKite(tactics.FamilyZombie)); err != nil {
		t.Fatal(err)
	}
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{23.0, 1, 20.5}) // 2.5 off and standing
	hits := 0
	for i := 0; i < 60; i++ {
		pose := r.a.Pose()
		before := len(r.out.attacks())
		r.step()
		if len(r.out.attacks()) > before {
			hits++
			if pose.OnGround || pose.Rise() >= 0 {
				t.Fatalf("attack %d at tick %d not mid-fall", hits, i)
			}
		}
	}
	if hits < 3 {
		t.Errorf("%d critical hits in 60 ticks, want the cadence kept up", hits)
	}
}

func TestKiteVanguardKeepsTheShieldUpWhileItWaitsAndPlants(t *testing.T) {
	r := newWarrior(t, "vanguard", mgl32.Vec3{30.5, 1, 20.5})
	r.give(0, 303, 1)
	r.give(1, 360, 1)
	if err := r.l.SetProfile(usingKite(tactics.FamilyBrute)); err != nil {
		t.Fatal(err)
	}
	c := newChaser(50, mgl32.Vec3{35.5, 1, 20.5})
	r.spawn(50, "minecraft:piglin_brute", c.pos)
	r.setHealth(20)
	raised := false
	for i := 0; i < 20; i++ {
		r.step()
		c.tick(r)
		if r.l.warrior.shield {
			raised = true
		}
	}
	if !raised {
		t.Error("the Vanguard never took the shield up while it waited on the brute")
	}
	if n := r.out.uses(1); n == 0 {
		t.Error("the shield was never raised as an item use")
	}
}

func TestKiteIsRegisteredForTheZombieSpiderAndBruteFamilies(t *testing.T) {
	for fam, want := range map[tactics.Family]string{
		tactics.FamilyZombie: "zombie/kite", tactics.FamilySpider: "spider/kite", tactics.FamilyBrute: "brute/kite",
	} {
		found := false
		for _, n := range TacticNames(fam) {
			found = found || n == want
		}
		if !found {
			t.Errorf("%s is not registered for the %s family", want, fam)
		}
		if _, ok := tacticNamed(want, fam); !ok {
			t.Errorf("%s does not resolve for the %s family", want, fam)
		}
	}
}

func TestKiteReadsTheBlowRhythmFromTheFight(t *testing.T) {
	st := &kiteState{hitEvery: kiteMobCooldown}
	l := &Life{tick: 100}
	f := &Fight{Target: belief.Entity{ID: 50}}
	f.Snapshot.HealthKnown, f.Snapshot.Health = true, 20
	st.seen = true
	st.health = 20
	st.lastHit = 100 - 12
	f.Snapshot.Health = 19
	st.observe(l, f)
	if st.hitEvery != 12 {
		t.Errorf("blow interval = %d after a second blow 12 ticks on, want 12", st.hitEvery)
	}
	if st.ready(l.tick + 5) {
		t.Error("the mob was ready 5 ticks after a blow")
	}
	if !st.ready(l.tick + 12) {
		t.Error("the mob was not ready a full interval after a blow")
	}
}
