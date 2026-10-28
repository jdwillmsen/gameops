package life

import (
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// swarmRig is a Vanguard with a sword and one cave spider in reach. The
// Vanguard is the sharp test: its shared circle plants it, so any movement
// is the swarm tactic's own.
func swarmRig(t *testing.T, tactic string) *rig {
	t.Helper()
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:cave_spider", mgl32.Vec3{22.5, 1, 20.5})
	if tactic != "" {
		if err := r.l.SetProfile(spiderTacticProfile(tactic)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestSpiderSwarmKeepsTheUnitMoving(t *testing.T) {
	base := swarmRig(t, "")
	base.run(40)
	if moved := dist(base.feet(), start); moved > 0.2 {
		t.Fatalf("the default profile moved %.2f blocks, want the Vanguard planted", moved)
	}
	if n := len(base.out.attacks()); n == 0 {
		t.Fatal("the default profile never attacked")
	}

	r := swarmRig(t, "spider/swarm")
	r.run(40)
	if n := len(r.out.attacks()); n == 0 {
		t.Fatal("the swarm tactic never attacked")
	}
	if moved := dist(r.feet(), start); moved < 0.5 {
		t.Errorf("moved %.2f blocks, want it circling between swings", moved)
	}
}

func TestSpiderSwarmLeavesAPlainSpiderToTheRoleFight(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:spider", mgl32.Vec3{22.5, 1, 20.5})
	if err := r.l.SetProfile(spiderTacticProfile("spider/swarm")); err != nil {
		t.Fatal(err)
	}
	r.run(40)

	if moved := dist(r.feet(), start); moved > 0.2 {
		t.Errorf("moved %.2f blocks against a plain spider, want the role's planted fight", moved)
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Error("never attacked the plain spider")
	}
}

func TestSpiderSwarmLetsAHurtUnitEatAfterTheSwarm(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	r.give(3, 363, 2)
	r.setHealth(10)
	// Far enough that the role heals, close enough that the spider is still
	// the focus and the tactic is offered the tick.
	r.spawn(50, "minecraft:cave_spider", mgl32.Vec3{28.5, 1, 20.5})
	if err := r.l.SetProfile(spiderTacticProfile("spider/swarm")); err != nil {
		t.Fatal(err)
	}
	r.run(15)

	if r.out.uses(3) == 0 {
		t.Error("never started the meal a hurt unit was free to eat")
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings while recovering from the swarm, want none", n)
	}
}

func TestSpiderSwarmObeysAHoldOrder(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	r.setSquad(t, &fakeSquad{
		leaderID: "lead", leader: start, leaderOK: true,
		order: Order{Kind: OrderHold},
	})
	r.spawn(50, "minecraft:cave_spider", mgl32.Vec3{22.5, 1, 20.5})
	if err := r.l.SetProfile(spiderTacticProfile("spider/swarm")); err != nil {
		t.Fatal(err)
	}
	r.run(40)

	if moved := dist(r.feet(), start); moved > 0.2 {
		t.Errorf("moved %.2f blocks while told to hold, want position kept", moved)
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Error("never attacked the cave spider while holding")
	}
}

func TestBothSpiderVariantsAreRegistered(t *testing.T) {
	names := TacticNames(tactics.FamilySpider)
	for _, want := range []string{"spider/ground", "spider/swarm"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("TacticNames(spider) = %v, missing %q", names, want)
		}
	}
}

// hurtOnTheSpiders is a Striker at 8 HP with the spiders in reach and the
// leader nine blocks west, the spot where the shared decisions regroup.
func hurtOnTheSpiders(t *testing.T, tactic string, spawn func(*rig)) *rig {
	t.Helper()
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.setHealth(8)
	r.setSquad(t, &fakeSquad{leaderID: "lead", leader: mgl32.Vec3{start.X() - 9, 1, start.Z()}, leaderOK: true})
	spawn(r)
	if tactic != "" {
		if err := r.l.SetProfile(spiderTacticProfile(tactic)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// chased runs n ticks with the mobs staying on the unit's heels, the chase
// that keeps a hurt unit inside the press range its regroup answers. It
// returns the swings made while still short of the leader.
func chased(r *rig, n int, ids ...uint64) (early int) {
	leader := mgl32.Vec3{start.X() - 9, 1, start.Z()}
	for range n {
		before := len(r.out.attacks())
		r.step()
		if len(r.out.attacks()) > before && dist(r.feet(), leader) > regroupWithin+0.5 {
			early++
		}
		for _, id := range ids {
			r.moveTo(id, r.feet().Add(mgl32.Vec3{1.2, 0, 0}))
		}
	}
	return early
}

func TestSpiderTacticsRegroupWhenAHurtUnitIsToFallBackOnTheLeader(t *testing.T) {
	for _, c := range []struct {
		tactic string
		spawn  func(*rig)
	}{
		{"spider/swarm", func(r *rig) {
			r.spawn(50, "minecraft:cave_spider", mgl32.Vec3{22, 1, 20.5})
			r.spawn(51, "minecraft:cave_spider", mgl32.Vec3{22, 1, 21.5})
		}},
		{"spider/ground", func(r *rig) {
			r.spawn(50, "minecraft:spider", mgl32.Vec3{22, 2.2, 20.5})
			r.spawn(51, "minecraft:spider", mgl32.Vec3{22, 2.2, 21.5})
		}},
	} {
		t.Run(c.tactic, func(t *testing.T) {
			base := hurtOnTheSpiders(t, "", c.spawn)
			chased(base, 40, 50, 51)
			gained := start.X() - base.feet().X()
			if gained < 3 {
				t.Fatalf("the default profile moved %.2f blocks toward the leader, want a regroup", gained)
			}

			r := hurtOnTheSpiders(t, c.tactic, c.spawn)
			early := chased(r, 40, 50, 51)
			if got := start.X() - r.feet().X(); got < gained-0.5 {
				t.Errorf("moved %.2f blocks toward the leader, the default regroup moved %.2f", got, gained)
			}
			if early != 0 {
				t.Errorf("%d swings before reaching the leader, want the regroup kept", early)
			}
		})
	}
}

func TestSpiderTacticsLetAHurtUnitStartItsMeal(t *testing.T) {
	for _, c := range []struct {
		tactic, mob string
		y           float32
	}{
		{"spider/swarm", "minecraft:cave_spider", 1},
		{"spider/ground", "minecraft:spider", 2.2},
	} {
		t.Run(c.tactic, func(t *testing.T) {
			r := newWarrior(t, "striker", start)
			r.give(0, 303, 1)
			r.give(3, 363, 2)
			r.setHealth(8)
			r.spawn(50, c.mob, mgl32.Vec3{28.5, c.y, 20.5})
			if err := r.l.SetProfile(spiderTacticProfile(c.tactic)); err != nil {
				t.Fatal(err)
			}
			r.run(15)
			if r.out.uses(3) == 0 {
				t.Error("never started the meal a hurt unit wanted")
			}
		})
	}
}

func TestSpiderTacticsObeyARetreatOrder(t *testing.T) {
	for _, c := range []struct {
		tactic, mob string
		y           float32
	}{
		{"spider/swarm", "minecraft:cave_spider", 1},
		{"spider/ground", "minecraft:spider", 2.2},
	} {
		t.Run(c.tactic, func(t *testing.T) {
			r := newWarrior(t, "striker", start)
			r.give(0, 303, 1)
			r.setSquad(t, &fakeSquad{
				leaderID: "lead", leader: mgl32.Vec3{start.X() - 9, 1, start.Z()}, leaderOK: true,
				order: Order{Kind: OrderRetreat},
			})
			r.spawn(50, c.mob, mgl32.Vec3{22, c.y, 20.5})
			if err := r.l.SetProfile(spiderTacticProfile(c.tactic)); err != nil {
				t.Fatal(err)
			}
			r.run(20)
			if n := len(r.out.attacks()); n != 0 {
				t.Errorf("%d swings under a retreat order", n)
			}
		})
	}
}

func TestSpiderSwarmDoesNotOrbitOverAOneBlockWaterStrip(t *testing.T) {
	for _, orbit := range []int8{1, -1} {
		t.Run(fmt.Sprintf("orbit%+d", orbit), func(t *testing.T) {
			r := newWarrior(t, "vanguard", mgl32.Vec3{20.5, 1, 20.8})
			r.give(0, 303, 1)
			r.waterStripZ(21)
			r.spawn(50, "minecraft:cave_spider", mgl32.Vec3{22.5, 1, 20.8})
			if err := r.l.SetProfile(spiderTacticProfile("spider/swarm")); err != nil {
				t.Fatal(err)
			}
			r.l.warrior.orbit = orbit
			for i := 0; i < 40; i++ {
				r.step()
				if f := r.feet(); f.Z() >= 21 || f.Y() != 1 {
					t.Fatalf("tick %d: feet at %v, orbited into the water strip", i, f)
				}
			}
		})
	}
}

func TestSpiderSwarmDoesNotBackOffOverAOneBlockWaterStrip(t *testing.T) {
	r := newWarrior(t, "vanguard", mgl32.Vec3{20.8, 1, 20.5})
	r.give(0, 303, 1)
	r.waterStripX(21)
	// Inside the orbit, on the west side: the step out runs east.
	r.spawn(50, "minecraft:cave_spider", mgl32.Vec3{19.9, 1, 20.5})
	if err := r.l.SetProfile(spiderTacticProfile("spider/swarm")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		r.step()
		if f := r.feet(); f.X() >= 21 || f.Y() != 1 {
			t.Fatalf("tick %d: feet at %v, stepped into the water strip", i, f)
		}
	}
}

func TestSpiderSwarmLetsGoOfAHalfDrawnBow(t *testing.T) {
	letsGoOfTheBow(t, "minecraft:cave_spider", mgl32.Vec3{28.5, 1, 20.5}, spiderTacticProfile("spider/swarm"),
		func(r *rig) { r.moveTo(50, mgl32.Vec3{21.7, 1, 20.5}) })
}
