package life

import (
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
