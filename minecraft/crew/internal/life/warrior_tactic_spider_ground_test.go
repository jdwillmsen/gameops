package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// spiderTacticProfile names the spider family's tactic variant.
func spiderTacticProfile(name string) tactics.Profile {
	pr := tactics.NewProfile("spider " + name)
	pr.Tactics = map[tactics.Family]string{tactics.FamilySpider: name}
	return pr
}

func TestSpiderGroundBacksOffAClimbingSpider(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	if err := r.l.SetProfile(spiderTacticProfile("spider/ground")); err != nil {
		t.Fatal(err)
	}
	// Three blocks up and just two across: on the wall, out of a swing.
	climber := mgl32.Vec3{22.5, 4, 20.5}
	r.spawn(50, "minecraft:spider", climber)
	r.run(20)

	if moved := start.X() - r.feet().X(); moved < 0.5 {
		t.Fatalf("gave %.2f blocks of ground, want it opening distance from the wall", moved)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings at a spider above reach, want none", n)
	}
}

func TestSpiderGroundStrikesTheSpiderOnItsLeap(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	if err := r.l.SetProfile(spiderTacticProfile("spider/ground")); err != nil {
		t.Fatal(err)
	}
	// A block and a bit up and inside reach is a spider coming down at the
	// unit: the standoff's payoff.
	r.spawn(50, "minecraft:spider", mgl32.Vec3{22, 2.2, 20.5})
	r.run(30)

	if n := len(r.out.attacks()); n == 0 {
		t.Fatal("never swung at a spider leaping into reach")
	}
	if moved := dist(r.feet(), start); moved > 0.5 {
		t.Errorf("moved %.2f blocks while the spider was in reach, want it stood and hit", moved)
	}
}

func TestSpiderGroundLeavesAGroundedSpiderToTheRoleFight(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	if err := r.l.SetProfile(spiderTacticProfile("spider/ground")); err != nil {
		t.Fatal(err)
	}
	spider := mgl32.Vec3{26.5, 1, 20.5}
	r.spawn(50, "minecraft:spider", spider)
	r.run(80)

	if d := dist(r.feet(), spider); d > meleeReach+0.5 {
		t.Errorf("stopped %.1f blocks off a grounded spider, want the role's own chase", d)
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Error("never swung at the grounded spider")
	}
	if moved := start.X() - r.feet().X(); moved > 0.1 {
		t.Errorf("gave %.2f blocks of ground to a spider already on the floor", moved)
	}
}

func TestSpiderGroundDoesNotFireUnderTheDefaultProfile(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:spider", mgl32.Vec3{22.5, 4, 20.5})
	r.run(20)

	if moved := start.X() - r.feet().X(); moved > 0.2 {
		t.Errorf("gave %.2f blocks of ground without the tactic named", moved)
	}
}

func TestSpiderGroundRefusesToBackIntoLava(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	if err := r.l.SetProfile(spiderTacticProfile("spider/ground")); err != nil {
		t.Fatal(err)
	}
	// The only cell behind the unit is not floor the planner accepts.
	r.w.over[protocol.BlockPos{19, 0, 20}] = "lava"
	r.spawn(50, "minecraft:spider", mgl32.Vec3{22.5, 4, 20.5})
	r.run(20)

	if moved := start.X() - r.feet().X(); moved > 0.2 {
		t.Errorf("backed %.2f blocks toward lava, want it holding the safe spot", moved)
	}
}
