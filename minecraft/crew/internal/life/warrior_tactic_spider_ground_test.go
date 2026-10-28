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
	pr.Tactics = map[tactics.Family][]string{tactics.FamilySpider: {name}}
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

func TestSpiderGroundAndSwarmWorkTogetherInOneProfile(t *testing.T) {
	pr := tactics.NewProfile("both spiders")
	pr.Tactics = map[tactics.Family][]string{tactics.FamilySpider: {"spider/ground", "spider/swarm"}}

	wall := newWarrior(t, "vanguard", start)
	wall.give(0, 303, 1)
	if err := wall.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	wall.spawn(50, "minecraft:spider", mgl32.Vec3{22.5, 4, 20.5})
	wall.run(20)
	if moved := start.X() - wall.feet().X(); moved < 0.5 {
		t.Errorf("gave %.2f blocks of ground to a spider on the wall, want ground to take it", moved)
	}

	swarm := newWarrior(t, "vanguard", start)
	swarm.give(0, 303, 1)
	if err := swarm.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	swarm.spawn(50, "minecraft:cave_spider", mgl32.Vec3{22.5, 1, 20.5})
	swarm.run(40)
	if len(swarm.out.attacks()) == 0 {
		t.Error("never swung at the cave spider in reach")
	}
	if moved := dist(swarm.feet(), start); moved < 0.2 {
		t.Errorf("moved %.2f blocks against a cave spider, want the swarm orbit", moved)
	}
}

// groundThenSpy is a unit with spider/ground first and a spy behind it, so
// every tick ground declines shows up as a tick the spy was offered.
func groundThenSpy(t *testing.T) (*rig, *passTactic) {
	t.Helper()
	spy := &passTactic{name: "spider/spy", family: tactics.FamilySpider}
	withTactics(t, spiderGround{}, spy)
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	pr := tactics.NewProfile("ground then spy")
	pr.Tactics = map[tactics.Family][]string{tactics.FamilySpider: {"spider/ground", "spider/spy"}}
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	r.spawn(50, "minecraft:spider", mgl32.Vec3{22.5, 4, 20.5})
	return r, spy
}

func TestSpiderGroundHandsBackToTheRoleFightWhenTheSpiderNeverComesDown(t *testing.T) {
	r, spy := groundThenSpy(t)
	r.run(spiderWallCap - 5)
	if len(spy.seen) != 0 {
		t.Fatalf("ground declined %d ticks inside the cap", len(spy.seen))
	}
	r.run(10)
	if len(spy.seen) == 0 {
		t.Fatal("ground still held the standoff past the cap")
	}
	if f := spy.seen[0]; f.Life.tick < spiderWallCap {
		t.Errorf("ground gave up at tick %d, before the %d tick cap", f.Life.tick, spiderWallCap)
	}
}

func TestSpiderGroundStandoffRestartsAfterTheCooldown(t *testing.T) {
	r, spy := groundThenSpy(t)
	r.run(spiderWallCap + 5)
	if len(spy.seen) == 0 {
		t.Fatal("the cap never fired")
	}
	r.remove(50)
	r.run(spiderWallCooldown)
	r.spawn(51, "minecraft:spider", mgl32.Vec3{22.5, 4, 20.5})
	seen := len(spy.seen)
	r.run(10)
	if len(spy.seen) != seen {
		t.Errorf("ground declined %d ticks after the cooldown, want the standoff back", len(spy.seen)-seen)
	}
}

func TestSpiderGroundKeepsTheCooldownAfterTheCap(t *testing.T) {
	r, spy := groundThenSpy(t)
	r.run(spiderWallCap + 5)
	r.remove(50)
	r.spawn(51, "minecraft:spider", mgl32.Vec3{22.5, 4, 20.5})
	seen := len(spy.seen)
	r.run(10)
	if len(spy.seen) == seen {
		t.Error("ground took the next wall spider straight after the cap, inside its cooldown")
	}
}

func TestSpiderGroundCapRestartsWhenTheSpiderDrops(t *testing.T) {
	r, spy := groundThenSpy(t)
	r.run(spiderWallCap - 10)
	r.moveTo(50, mgl32.Vec3{26.5, 1, 20.5})
	r.run(5)
	r.moveTo(50, mgl32.Vec3{22.5, 4, 20.5})
	r.run(spiderWallCap - 10)
	for _, f := range spy.seen {
		if f.Target.Pos.Y() > 2 {
			t.Fatal("a wall standoff that was broken by a drop still counted toward the cap")
		}
	}
}

// waterStripX is a one-block-wide strip of water running north to south at
// x, long enough that no way round it is within a fight's reach.
func (r *rig) waterStripX(x int32) {
	for z := int32(5); z <= 40; z++ {
		r.w.over[protocol.BlockPos{x, 0, z}] = "water"
	}
}

// waterStripZ is the same strip running east to west at z.
func (r *rig) waterStripZ(z int32) {
	for x := int32(5); x <= 40; x++ {
		r.w.over[protocol.BlockPos{x, 0, z}] = "water"
	}
}

func TestSpiderGroundDoesNotBackOverAOneBlockWaterStrip(t *testing.T) {
	r := newWarrior(t, "striker", mgl32.Vec3{20.8, 1, 20.5})
	r.give(0, 303, 1)
	if err := r.l.SetProfile(spiderTacticProfile("spider/ground")); err != nil {
		t.Fatal(err)
	}
	r.waterStripX(21)
	// On the wall to the west: backing off from it runs east, over the strip.
	r.spawn(50, "minecraft:spider", mgl32.Vec3{18.5, 4, 20.5})
	for i := 0; i < 30; i++ {
		r.step()
		if f := r.feet(); f.X() >= 21 || f.Y() != 1 {
			t.Fatalf("tick %d: feet at %v, stepped into the water strip", i, f)
		}
	}
}

// letsGoOfTheBow starts a Ranger's draw against a mob at a distance, then
// puts the profile on and gives the fight the mob's new place: the tactic
// that takes the tick must let the half-drawn bow go.
func letsGoOfTheBow(t *testing.T, mob string, at mgl32.Vec3, pr tactics.Profile, then func(*rig)) {
	t.Helper()
	r := newWarrior(t, "ranger", start)
	r.give(1, 361, 1)
	r.give(20, 362, 32)
	r.spawn(50, mob, at)
	r.beginDraw(t)
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	then(r)
	r.step()
	if w := r.l.warrior; w.drawing {
		t.Error("still drawing after the tactic took the tick")
	}
	if n := r.out.releases(1); n == 0 {
		t.Error("the half-drawn bow was never released")
	}
}

func TestSpiderGroundLetsGoOfAHalfDrawnBow(t *testing.T) {
	letsGoOfTheBow(t, "minecraft:spider", mgl32.Vec3{28.5, 1, 20.5}, spiderTacticProfile("spider/ground"),
		func(r *rig) { r.moveTo(50, mgl32.Vec3{22.5, 4, 20.5}) })
}
