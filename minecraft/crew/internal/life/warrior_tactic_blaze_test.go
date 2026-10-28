package life

import (
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// snowballID is the fake table's runtime id for the snowball, which the
// shipped table has no entry for.
const snowballID = 370

// armSnowballs extends both item tables — each replaces its whole table —
// with the snowball, then hands a stack over.
func armSnowballs(r *rig, slot int, count int) {
	r.t.Helper()
	table := append(slices.Clone(itemTable), protocol.ItemEntry{Name: "minecraft:snowball", RuntimeID: snowballID})
	r.tracker.ItemNames(table)
	r.a.ItemNames(table)
	r.give(slot, snowballID, count)
}

// usingBlaze is a profile that fights the blaze family with the named
// tactic.
func usingBlaze(tactic string) tactics.Profile {
	pr := tactics.NewProfile("blaze " + tactic)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyBlaze: {tactic}}
	return pr
}

func mustProfile(t *testing.T, r *rig, pr tactics.Profile) {
	t.Helper()
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
}

// shieldBlazeFighter arms a vanguard with a sword and a shield.
func shieldBlazeFighter(r *rig) {
	r.give(0, 303, 1)
	r.give(1, 360, 1)
}

// vanguardOnABlaze is a vanguard and a blaze six and a half blocks east,
// left to fight a while.
func vanguardOnABlaze(r *rig) {
	shieldBlazeFighter(r)
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{27, 1, 20.5})
	r.run(40)
}

var blazeAt = mgl32.Vec3{27, 1, 20.5}

func TestTheBlazeTacticsAreRegistered(t *testing.T) {
	for _, want := range []string{"blaze/shield", "blaze/snowball"} {
		if !slices.Contains(TacticNames(tactics.FamilyBlaze), want) {
			t.Errorf("no tactic registered under %q", want)
		}
	}
}

func TestBlazeShieldShieldsTheVolleyThenClosesAndSwings(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	mustProfile(t, r, usingBlaze("blaze/shield"))
	shieldBlazeFighter(r)
	r.spawn(50, "minecraft:blaze", blazeAt)

	closed := false
	for i := 0; i < 60 && !closed; i++ {
		r.step()
		closed = dist(r.feet(), blazeAt) < 4
	}
	if !closed {
		t.Fatalf("never closed on the blaze; feet %v", r.feet())
	}

	// A volley: fireballs in the air, closing on the unit.
	r.spawn(70, "minecraft:small_fireball", mgl32.Vec3{26.5, 1.5, 20.5})
	swings, stood := len(r.out.attacks()), dist(r.feet(), blazeAt)
	shielded := false
	for i := 0; i < 4; i++ {
		r.moveTo(70, mgl32.Vec3{25.5 - float32(i), 1.5, 20.5})
		r.step()
		shielded = shielded || r.out.uses(1) > 0
		if d := dist(r.feet(), blazeAt); d < stood-0.3 {
			t.Fatalf("tick %d: closed %.2f further while the volley stood", i, stood-d)
		}
	}
	if !shielded {
		t.Error("the shield never went up with a fireball in the air")
	}
	if n := len(r.out.attacks()); n != swings {
		t.Errorf("%d swings while the volley was in the air, want none", n-swings)
	}

	// The volley spent: the fight closes and the blaze is struck.
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 70})
	struck := false
	for i := 0; i < 120 && !struck; i++ {
		r.step()
		struck = len(r.out.attacks()) > swings
	}
	if !struck {
		t.Fatalf("never swung at the blaze after the volley; feet %v", r.feet())
	}
	if d := dist(r.feet(), blazeAt); d > meleeReach+0.2 {
		t.Errorf("swung from %.1f blocks off, past reach", d)
	}
}

func TestBlazeShieldNeverStandsInFireOnTheWayIn(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	mustProfile(t, r, usingBlaze("blaze/shield"))
	shieldBlazeFighter(r)
	// A wall of fire across the walk to the blaze.
	for z := int32(17); z <= 23; z++ {
		for y := int32(1); y <= 2; y++ {
			r.w.over[protocol.BlockPos{24, y, z}] = "fire"
		}
	}
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{28.5, 1, 20.5})

	struck := false
	for i := 0; i < 500 && !struck; i++ {
		r.step()
		c := cellOf(r.feet())
		if n, ok := r.w.Block(c); !ok || !safeFloor(n) {
			t.Fatalf("tick %d: stood in %q at %v", i, n, r.feet())
		}
		if n, ok := r.w.Block(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()}); ok && !safeFloor(n) {
			t.Fatalf("tick %d: stood on %q at %v", i, n, r.feet())
		}
		struck = len(r.out.attacks()) > 0
	}
	if !struck {
		t.Fatalf("never reached the blaze past the fire; feet %v", r.feet())
	}
}

func TestBlazeShieldLeavesBurningGroundAndResumesTheFight(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	mustProfile(t, r, usingBlaze("blaze/shield"))
	shieldBlazeFighter(r)
	r.w.over[protocol.BlockPos{20, 1, 20}] = "fire"
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{27.5, 1, 20.5})

	clear := false
	for i := 0; i < 80; i++ {
		r.step()
		c := cellOf(r.feet())
		hot := ""
		if n, ok := r.w.Block(c); ok && !safeFloor(n) {
			hot = n
		}
		if n, ok := r.w.Block(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()}); ok && !safeFloor(n) {
			hot = n
		}
		if hot == "" {
			clear = true
			continue
		}
		// Walking out of the cell it spawned in takes a few ticks from a
		// standstill; walking back into it, or dawdling in it, does not.
		if clear {
			t.Fatalf("tick %d: walked back into %q at %v", i, hot, r.feet())
		}
		if i >= 12 {
			t.Fatalf("tick %d: still in %q after the walk out; feet %v", i, hot, r.feet())
		}
	}
	if d := dist(r.feet(), mgl32.Vec3{27.5, 1, 20.5}); d > meleeReach+0.5 {
		t.Errorf("never resumed the fight after the fire; still %.1f off", d)
	}
}

func TestBlazeShieldShieldsTheVolleyEvenFromBurningGround(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	mustProfile(t, r, usingBlaze("blaze/shield"))
	shieldBlazeFighter(r)
	r.w.over[protocol.BlockPos{20, 1, 20}] = "fire"
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{27.5, 1, 20.5})
	r.spawn(70, "minecraft:small_fireball", mgl32.Vec3{26, 1.5, 20.5})

	for i := 0; i < 10; i++ {
		r.step()
		if r.out.uses(1) == 0 {
			t.Fatalf("tick %d: no shield with the volley up and fire underfoot", i)
		}
		if d := dist(r.feet(), start); d > 0.3 {
			t.Fatalf("tick %d: moved %.2f blocks while the volley stood", i, d)
		}
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 70})
	left := false
	for i := 0; i < 60 && !left; i++ {
		r.step()
		left = dist(r.feet(), start) > 0.5
	}
	if !left {
		t.Fatal("never left the fire once the volley was spent")
	}
}

func TestBlazeShieldKeepsPositionUnderAHoldOrder(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	mustProfile(t, r, usingBlaze("blaze/shield"))
	shieldBlazeFighter(r)
	r.setSquad(t, &fakeSquad{order: Order{Kind: OrderHold}})
	r.spawn(50, "minecraft:blaze", blazeAt)
	r.spawn(70, "minecraft:small_fireball", mgl32.Vec3{26, 1.5, 20.5})

	for i := 0; i < 20; i++ {
		r.step()
	}
	if r.out.uses(1) == 0 {
		t.Error("no shield while holding against a volley")
	}
	// The volley spent: the position is still held, the blaze met only
	// when it comes into reach.
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 70})
	for i := 0; i < 20; i++ {
		r.step()
		if d := dist(r.feet(), start); d > 0.5 {
			t.Fatalf("tick %d: advanced %.2f blocks under a hold order", i, d)
		}
	}
}

func TestBlazeShieldDeclinesARetreatOrder(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	mustProfile(t, r, usingBlaze("blaze/shield"))
	shieldBlazeFighter(r)
	squad := &fakeSquad{order: Order{Kind: OrderRetreat}}
	r.setSquad(t, squad)
	r.spawn(50, "minecraft:blaze", blazeAt)

	for i := 0; i < 30; i++ {
		r.step()
	}
	if d := dist(r.feet(), start); d < 2 {
		t.Errorf("only %.2f blocks from where it stood under a retreat order", d)
	}
	if n := r.out.uses(1); n != 0 {
		t.Errorf("%d shield raises during an ordered retreat", n)
	}
}

func TestBlazeSnowballThrowsFromTheBandAndNeverCloses(t *testing.T) {
	r := newWarrior(t, "striker", start)
	mustProfile(t, r, usingBlaze("blaze/snowball"))
	r.give(0, 303, 1)
	armSnowballs(r, 2, 10)
	r.spawn(50, "minecraft:blaze", blazeAt)

	for i := 0; i < 60; i++ {
		r.step()
	}
	if n := r.out.uses(2); n < 3 {
		t.Fatalf("%d snowballs thrown in 60 ticks, want the cadence's few", n)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings while snowballs were the fight", n)
	}
	if d := dist(r.feet(), start); d > 1.5 {
		t.Errorf("moved %.2f blocks while the blaze held its distance", d)
	}
}

func TestBlazeSnowballGivesGroundWhenTheBlazePressesIn(t *testing.T) {
	r := newWarrior(t, "striker", start)
	mustProfile(t, r, usingBlaze("blaze/snowball"))
	r.give(0, 303, 1)
	armSnowballs(r, 2, 10)
	r.spawn(50, "minecraft:blaze", blazeAt)

	for i := 0; i < 60; i++ {
		r.step()
		r.moveTo(50, mgl32.Vec3{27 - float32(i)*0.2, 1, 20.5})
	}
	if d := dist(r.feet(), mgl32.Vec3{15, 1, 20.5}); d < 4 {
		t.Errorf("only %.1f blocks from the blaze that pressed in", d)
	}
	if d := dist(r.feet(), start); d < 5 {
		t.Errorf("only %.2f blocks from where it stood, want real ground given", d)
	}
	if n := r.out.uses(2); n < 3 {
		t.Errorf("%d throws while giving ground, want the cadence's few", n)
	}
}

func TestBlazeSnowballDeclinesWithoutSnowballs(t *testing.T) {
	r := newWarrior(t, "striker", start)
	mustProfile(t, r, usingBlaze("blaze/snowball"))
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{22.5, 1, 20.5})

	for i := 0; i < 40; i++ {
		r.step()
	}
	if len(r.out.attacks()) == 0 {
		t.Fatal("the role's own fight never ran without snowballs")
	}
	if n := r.out.uses(2); n != 0 {
		t.Errorf("%d throws with an empty hand", n)
	}
}

func TestBlazeSnowballLeavesTheRangerItsBow(t *testing.T) {
	r := newWarrior(t, "ranger", start)
	mustProfile(t, r, usingBlaze("blaze/snowball"))
	r.give(0, 361, 1)  // bow
	r.give(1, 362, 32) // arrows
	armSnowballs(r, 2, 10)
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{28.5, 1, 20.5})

	for i := 0; i < 80; i++ {
		r.step()
	}
	if n := r.out.releases(0); n == 0 {
		t.Error("the ranger never released an arrow")
	}
	if n := r.out.uses(2); n != 0 {
		t.Errorf("%d snowball throws beside a drawn bow", n)
	}
}

func TestTheBlazeTacticsNeverRunUnderTheDefaultProfile(t *testing.T) {
	withTactics(t)
	base := replay(t, "vanguard", vanguardOnABlaze, nil)

	withTactics(t, &blazeShieldTactic{}, &blazeSnowTactic{})
	got := replay(t, "vanguard", vanguardOnABlaze, nil)
	if !reflect.DeepEqual(got, base) {
		t.Errorf("registering the blaze tactics changed the default fight: %d packets vs %d", len(got), len(base))
	}
}

// throwSpy is an actuator that only counts what a throw sends.
type throwSpy struct {
	skill.Actuator
	uses int
}

func (s *throwSpy) Tick(act.Input) error                { return nil }
func (s *throwSpy) SelectHotbar(int, belief.Item) error { return nil }
func (s *throwSpy) UseItem(int, belief.Item) error      { s.uses++; return nil }

func TestBlazeSnowballKeepsEachUnitsCadenceUnderConcurrentUnits(t *testing.T) {
	tac := &blazeSnowTactic{}
	snow := belief.Item{Slot: 2, Name: snowballName, Count: 10}
	snap := belief.Snapshot{HeldKnown: true, HeldSlot: 2}
	var wg sync.WaitGroup
	for range 8 {
		l := &Life{}
		spy := &throwSpy{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tick := range uint64(100) {
				l.tick = tick
				if err := tac.throw(l, spy, snap, snow, act.Input{}); err != nil {
					t.Error(err)
					return
				}
			}
			if want := 100 / snowEvery; spy.uses != want {
				t.Errorf("%d throws in 100 ticks, want %d", spy.uses, want)
			}
		}()
	}
	wg.Wait()
}

func TestBlazeSnowballDoesNotBackOverAOneBlockWaterStrip(t *testing.T) {
	r := newWarrior(t, "striker", mgl32.Vec3{20.8, 1, 20.5})
	mustProfile(t, r, usingBlaze("blaze/snowball"))
	r.give(0, 303, 1)
	armSnowballs(r, 2, 10)
	r.waterStripX(21)
	r.spawn(50, "minecraft:blaze", mgl32.Vec3{18.5, 1, 20.5})
	for i := 0; i < 30; i++ {
		r.step()
		if f := r.feet(); f.X() >= 21 || f.Y() != 1 {
			t.Fatalf("tick %d: feet at %v, backed into the water strip", i, f)
		}
	}
	if n := r.out.uses(2); n == 0 {
		t.Error("never threw while holding the line")
	}
}

func TestBlazeShieldLetsGoOfAHalfDrawnBow(t *testing.T) {
	letsGoOfTheBow(t, "minecraft:blaze", mgl32.Vec3{28.5, 1, 20.5}, usingBlaze("blaze/shield"), func(*rig) {})
}
