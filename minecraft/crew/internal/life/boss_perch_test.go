package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const dragonType = "minecraft:ender_dragon"

// newDragonWarrior is a warrior of a role with the dragon at the arena's
// middle, the squad around it as for any boss.
func newDragonWarrior(t *testing.T, role string, feet, dragon mgl32.Vec3) *rig {
	t.Helper()
	r, _ := newBossWarriorVs(t, role, feet, dragonType, dragon)
	return r
}

// fly moves the dragon a little every tick at a height, as a
// dragon on its holding pattern does.
func fly(r *rig, from, to int, height float32) {
	for i := from; i < to; i++ {
		r.moveTo(90, arena.Add(mgl32.Vec3{float32(i) * 0.5, height, 0}))
		if !r.step() {
			r.t.Fatalf("tick %d: the warrior dropped the dragon", i)
		}
	}
}

func TestDragonIsABossThatOnlyPerchesCanBeFoughtOn(t *testing.T) {
	for _, mob := range []string{"ender_dragon", "minecraft:ender_dragon"} {
		p, ok := bossOf(mob)
		if !ok || !p.perchOnly || !p.flies {
			t.Errorf("%s: profile %+v, want a flying perch-only boss", mob, p)
		}
		if kindOf(mob) != hostile {
			t.Errorf("%s is not hostile", mob)
		}
	}
	if w, _ := bossOf("warden"); w.perchOnly || w.flies {
		t.Error("the warden is fought as a perching flyer")
	}
}

func TestWarriorsHoldAndSwingNothingWhileTheDragonFlies(t *testing.T) {
	for _, role := range []string{"vanguard", "striker", "ranger"} {
		t.Run(role, func(t *testing.T) {
			start := arena.Sub(mgl32.Vec3{4, 0, 0})
			// Low enough to pass through the warrior's reach.
			r := newDragonWarrior(t, role, start, arena.Add(mgl32.Vec3{0, 3, 0}))
			fly(r, 0, 150, 3)
			if n := len(r.out.attacks()); n != 0 {
				t.Errorf("%d swings at a flying dragon", n)
			}
			if n := r.out.uses(2); n != 0 {
				t.Errorf("%d bow draws at a flying dragon", n)
			}
			if moved := r.feet().Sub(start).Len(); moved > 0.05 {
				t.Errorf("the warrior moved %.2f blocks while the dragon flew", moved)
			}
		})
	}
}

func TestWarriorsSwingAtTheDragonOnceItHasPerched(t *testing.T) {
	for _, role := range []string{"vanguard", "striker", "ranger"} {
		t.Run(role, func(t *testing.T) {
			r := newDragonWarrior(t, role, arena.Sub(mgl32.Vec3{4, 0, 0}), arena.Add(mgl32.Vec3{0, 20, 0}))
			fly(r, 0, 20, 20)

			// The dragon lands and sits: no more movement packets.
			r.moveTo(90, arena)
			r.run(perchStill - 1)
			if n := len(r.out.attacks()); n != 0 {
				t.Fatalf("%d swings before the dragon had stood still for %d ticks", n, perchStill)
			}
			r.run(30)
			if n := len(r.out.attacks()); n < 3 {
				t.Fatalf("%d swings at the perched dragon in 30 ticks, want the attack cadence", n)
			}
			if n := r.out.uses(2); n != 0 {
				t.Errorf("%d bow draws at a perched dragon", n)
			}
			for _, at := range r.out.attacks() {
				if at.TargetEntityRuntimeID != 90 {
					t.Errorf("swung at entity %d, want the dragon", at.TargetEntityRuntimeID)
				}
			}
		})
	}
}

func TestWarriorsStopSwingingWhenTheDragonTakesOff(t *testing.T) {
	r := newDragonWarrior(t, "vanguard", arena.Sub(mgl32.Vec3{4, 0, 0}), arena)
	r.run(perchStill + 20)
	swung := len(r.out.attacks())
	if swung == 0 {
		t.Fatal("no swings at the perched dragon")
	}
	feet := r.feet()

	fly(r, 0, 60, 3)
	if n := len(r.out.attacks()); n != swung {
		t.Errorf("%d swings after the dragon took off", n-swung)
	}
	if moved := r.feet().Sub(feet).Len(); moved > 0.05 {
		t.Errorf("the warrior chased the dragon %.2f blocks", moved)
	}
}

func TestWarriorsWalkUpToAPerchedDragonOutOfReach(t *testing.T) {
	for _, role := range []string{"vanguard", "striker", "ranger"} {
		t.Run(role, func(t *testing.T) {
			r := newDragonWarrior(t, role, arena.Sub(mgl32.Vec3{20, 0, 0}), arena)
			r.run(perchStill + 200)
			if len(r.out.attacks()) == 0 {
				t.Fatalf("no swings; the warrior ended %.1f blocks off the dragon", dist(r.feet(), arena))
			}
			p := r.a.Pose()
			if d := eyeToOrigin(p, belief.Entity{Pos: arena}); d > perchStrike {
				t.Errorf("swung from %.1f blocks off the dragon's origin, want within %.1f", d, perchStrike)
			}
		})
	}
}

func TestSwingsAreMeasuredEyeToOriginInThreeDimensions(t *testing.T) {
	e := belief.Entity{Pos: arena}
	for _, tc := range []struct {
		name string
		feet mgl32.Vec3
		want float64
	}{
		{name: "level", feet: arena.Add(mgl32.Vec3{5, 0, 0}), want: 5.26},
		{name: "below the origin", feet: arena.Add(mgl32.Vec3{0, -4.38, 0}), want: 2.76},
		{name: "far above", feet: arena.Add(mgl32.Vec3{0, 8, 0}), want: 9.62},
	} {
		got := eyeToOrigin(act.Pose{Feet: tc.feet}, e)
		if d := got - tc.want; d > 0.01 || d < -0.01 {
			t.Errorf("%s: %.2f, want %.2f", tc.name, got, tc.want)
		}
	}
}

func TestADragonPerchedBeyondReachIsNotSwungAt(t *testing.T) {
	// Perched high over a unit that cannot get any closer: the swing would
	// fall short, so none is made.
	r := newDragonWarrior(t, "vanguard", arena.Sub(mgl32.Vec3{2, 0, 0}), arena.Add(mgl32.Vec3{0, 9, 0}))
	r.run(perchStill + 60)
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings at a dragon %.1f blocks off", n, eyeToOrigin(r.a.Pose(), belief.Entity{Pos: arena.Add(mgl32.Vec3{0, 9, 0})}))
	}
}

func TestAnotherBossIsStillFoughtOnItsRolesTerms(t *testing.T) {
	// A warden standing still must not be taken for a perch: the Ranger
	// keeps shooting and never swings.
	r, _ := newBossWarrior(t, "ranger", arena.Sub(mgl32.Vec3{24, 0, 0}))
	r.run(perchStill + 100)
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings from the Ranger at a still warden", n)
	}
	if r.out.uses(2) == 0 {
		t.Error("the Ranger did not shoot a still warden")
	}
}

func TestAHeldWarriorDoesNotWalkUpToAPerchedDragon(t *testing.T) {
	r, squad := newBossWarriorVs(t, "vanguard", arena.Sub(mgl32.Vec3{20, 0, 0}), dragonType, arena)
	squad.order = Order{Kind: OrderHold}
	start := r.feet()
	r.run(perchStill + 100)
	if moved := r.feet().Sub(start).Len(); moved > 0.05 {
		t.Errorf("the held warrior walked %.1f blocks toward the perched dragon", moved)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings from a held warrior out of reach", n)
	}
}

func TestAHeldWarriorStillSwingsAtAPerchedDragonInReach(t *testing.T) {
	r, squad := newBossWarriorVs(t, "vanguard", arena.Sub(mgl32.Vec3{4, 0, 0}), dragonType, arena)
	squad.order = Order{Kind: OrderHold}
	start := r.feet()
	r.run(perchStill + 30)
	if n := len(r.out.attacks()); n < 3 {
		t.Errorf("%d swings from a held warrior in reach, want the attack cadence", n)
	}
	if moved := r.feet().Sub(start).Len(); moved > 0.05 {
		t.Errorf("the held warrior moved %.1f blocks", moved)
	}
}

func TestADragonsPerchRetryDoesNotStallAWardenRegroup(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	r, squad := newBossWarriorVs(t, "striker", arena.Sub(mgl32.Vec3{10, 0, 0}), dragonType, arena)
	r.run(2)
	// A perch approach that found no way, waiting to retry.
	r.l.warrior.boss.walkRetry = r.l.tick + 1000

	squad.down, squad.start, squad.startOK = 2, home, true
	r.spawn(80, "minecraft:warden", arena)
	r.moveTo(90, arena.Add(mgl32.Vec3{0, 30, 0}))
	r.run(250)
	if d := dist(r.feet(), home); d > regroupWithin {
		t.Fatalf("regrouped to %.1f blocks off the start, want within %.1f", d, float32(regroupWithin))
	}
}
