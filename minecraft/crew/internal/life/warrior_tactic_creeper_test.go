package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const creeperID = 80

// creeperProfile fights the creeper family with the named tactic.
func creeperProfile(name string) tactics.Profile {
	pr := tactics.NewProfile("creeper " + name)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyCreeper: {name}}
	return pr
}

// creeperRig is a healthy warrior of a role with a sword, and a bow and
// arrows besides, fighting creepers with the tactic registered under name.
func creeperRig(t *testing.T, role, name string, charged func(belief.Entity) bool) *rig {
	t.Helper()
	tc := newCreeperTactic(name, name == "creeper/ranged")
	tc.charged = charged
	withTactics(t, tc)
	r := newWarrior(t, role, start)
	if err := r.l.SetProfile(creeperProfile(name)); err != nil {
		t.Fatal(err)
	}
	r.give(0, 301, 1)
	r.give(1, 361, 1)
	r.give(9, 362, 16)
	r.setHealth(20)
	return r
}

// fuse plays a creeper as the server does for the part this tactic sees: it
// walks at the unit until it is within three blocks, then stops dead and
// burns for thirty ticks.
type fuse struct {
	r       *rig
	pos     mgl32.Vec3
	speed   float32
	litAt   int
	lit     bool
	atBlast float64
}

func newFuse(r *rig, at mgl32.Vec3, speed float32) *fuse {
	r.spawn(creeperID, "minecraft:creeper", at)
	return &fuse{r: r, pos: at, speed: speed}
}

// advance moves the creeper one tick, after the unit's, and notes how far
// the unit stood when the fuse ran out.
func (f *fuse) advance(tick int) {
	if f.lit {
		if tick == f.litAt+fuseTicks {
			f.atBlast = dist(f.r.feet(), f.pos)
		}
		return
	}
	d := dist(f.r.feet(), f.pos)
	if d <= 3.0 {
		f.lit, f.litAt = true, tick
		return
	}
	to := f.r.feet().Sub(f.pos)
	to[1] = 0
	f.pos = f.pos.Add(to.Normalize().Mul(f.speed))
	f.r.moveTo(creeperID, f.pos)
}

func (f *fuse) run(ticks int) {
	for i := 0; i < ticks; i++ {
		f.r.step()
		f.advance(i)
	}
}

// swings is the blows the unit has sent so far.
func swings(r *rig) int { return len(r.out.attacks()) }

func TestBothCreeperTacticsAreRegisteredForTheFamily(t *testing.T) {
	got := TacticNames(tactics.FamilyCreeper)
	want := []string{"creeper/backoff", "creeper/ranged"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TacticNames(creeper) = %v, want %v", got, want)
	}
}

func TestEveryRoleHitsAnApproachingCreeperThenGetsClearOfTheBlast(t *testing.T) {
	for _, name := range []string{"creeper/backoff", "creeper/ranged"} {
		for _, role := range []string{"vanguard", "striker", "ranger"} {
			t.Run(name+"/"+role, func(t *testing.T) {
				r := creeperRig(t, role, name, nil)
				// Without arrows the Ranger is a melee unit, which is the
				// variant under test here.
				if role == "ranger" {
					r.give(9, 3, 1)
				}
				f := newFuse(r, mgl32.Vec3{25.0, 1, 20.5}, 0.2)
				f.run(70)
				if !f.lit {
					t.Fatal("the creeper never reached the unit to light its fuse")
				}
				if swings(r) == 0 {
					t.Error("the unit never knocked the creeper back")
				}
				if f.atBlast < blastReach {
					t.Errorf("unit stood %.1f blocks from the blast, want at least %.1f", f.atBlast, blastReach)
				}
			})
		}
	}
}

func TestTheRangedVariantGetsOutOfTheFusesReachNotJustTheBlast(t *testing.T) {
	back := creeperRig(t, "striker", "creeper/backoff", nil)
	fb := newFuse(back, mgl32.Vec3{25.0, 1, 20.5}, 0.2)
	fb.run(70)

	ran := creeperRig(t, "striker", "creeper/ranged", nil)
	fr := newFuse(ran, mgl32.Vec3{25.0, 1, 20.5}, 0.2)
	fr.run(70)

	if fr.atBlast <= fb.atBlast {
		t.Errorf("ranged stood %.1f blocks off, backoff %.1f: the ranged variant should run farther", fr.atBlast, fb.atBlast)
	}
	if fr.atBlast < defuseReach {
		t.Errorf("ranged stood %.1f blocks off, want past the %.1f a fuse winds down at", fr.atBlast, defuseReach)
	}
}

func TestACreeperStandingStillCloseByIsTakenForALitOne(t *testing.T) {
	for _, name := range []string{"creeper/backoff", "creeper/ranged"} {
		t.Run(name, func(t *testing.T) {
			r := creeperRig(t, "striker", name, nil)
			c := mgl32.Vec3{23.95, 1, 20.5}
			r.spawn(creeperID, "minecraft:creeper", c)
			r.run(30)
			if n := swings(r); n != 0 {
				t.Errorf("%d blows on a creeper standing at ignition range", n)
			}
			if d := dist(r.feet(), c); d < blastReach {
				t.Errorf("%.1f blocks from the stopped creeper, want out of its blast", d)
			}
		})
	}
}

func TestACreeperThatKeepsWalkingIsHitNotFledFrom(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	f := newFuse(r, mgl32.Vec3{24.4, 1, 20.5}, 0.1)
	f.run(14)
	if swings(r) == 0 {
		t.Fatal("a walking creeper in lunge range was never hit")
	}
}

func TestAChargedCreeperIsKeptFarOffAndNeverHit(t *testing.T) {
	charged := func(belief.Entity) bool { return true }
	for name, tc := range map[string]func(belief.Entity) bool{"charged": charged, "plain": nil} {
		t.Run(name, func(t *testing.T) {
			r := creeperRig(t, "striker", "creeper/backoff", tc)
			f := newFuse(r, mgl32.Vec3{31.5, 1, 20.5}, 0.12)
			f.run(20)
			moved := dist(r.feet(), start)
			if name == "plain" {
				if moved > 0.01 {
					t.Errorf("the unit moved %.1f blocks for a plain creeper %.1f blocks off", moved, dist(r.feet(), f.pos))
				}
				return
			}
			if moved < 1 {
				t.Errorf("the unit moved %.1f blocks: it should be backing away from a charged creeper already", moved)
			}
			f.run(80)
			if f.lit {
				t.Error("the charged creeper got close enough to light its fuse")
			}
			if n := swings(r); n != 0 {
				t.Errorf("%d blows on a charged creeper", n)
			}
		})
	}
}

func TestAChargedBlastIsRetreatedFromFartherThanAPlainOne(t *testing.T) {
	plain, charged := newCreeperTactic("creeper/backoff", false), newCreeperTactic("creeper/backoff", false)
	charged.charged = func(belief.Entity) bool { return true }
	if p, c := plain.safeReach(false), charged.safeReach(true); c < p+(chargedBlastReach-blastReach)-0.01 {
		t.Errorf("safe reach %.1f against a charged creeper, %.1f against a plain one", c, p)
	}
}

func TestASquadMateBeyondTheCreeperIsNotKnockedIntoTheBlast(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	// The mate stands behind the creeper, on the line a blow would drive it
	// along.
	r.player(61, mgl32.Vec3{26.0, 1, 20.5})
	f := newFuse(r, mgl32.Vec3{24.4, 1, 20.5}, 0.1)
	f.run(30)
	if n := swings(r); n != 0 {
		t.Fatalf("%d blows with a squad-mate behind the creeper", n)
	}
	if d := dist(r.feet(), start); d < 2 {
		t.Errorf("the unit moved %.1f blocks: it should draw the creeper after itself, not stand by", d)
	}
}

func TestASquadMateBehindTheUnitDoesNotStopTheBlow(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	r.player(61, mgl32.Vec3{17.5, 1, 20.5})
	f := newFuse(r, mgl32.Vec3{24.4, 1, 20.5}, 0.1)
	f.run(14)
	if swings(r) == 0 {
		t.Fatal("a mate behind the unit kept it from hitting the creeper")
	}
}

func TestAHurtUnitBacksOffRatherThanLungesAtAnUnlitCreeper(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	r.setHealth(10)
	f := newFuse(r, mgl32.Vec3{24.4, 1, 20.5}, 0.1)
	f.run(30)
	if n := swings(r); n != 0 {
		t.Errorf("%d blows from a unit too hurt to take the blast", n)
	}
	if d := dist(r.feet(), start); d < 2 {
		t.Errorf("the unit moved %.1f blocks: it should back off the creeper", d)
	}
}

func TestARangerShootsACreeperNotYetCloseThenRunsFromOneThatIs(t *testing.T) {
	r := creeperRig(t, "ranger", "creeper/ranged", nil)
	c := mgl32.Vec3{25.0, 1, 20.5}
	r.spawn(creeperID, "minecraft:creeper", c)
	// An idle creeper at the edge of the band is shot at.
	for i := 0; i < 50; i++ {
		r.step()
	}
	if r.out.uses(1) == 0 {
		t.Fatal("the Ranger never drew its bow on a creeper at four and a half blocks")
	}
	if r.out.releases(1) == 0 {
		t.Error("the Ranger drew but never loosed")
	}
	if n := swings(r); n != 0 {
		t.Errorf("%d sword blows from a Ranger with arrows", n)
	}

	// Once it closes on the unit the bow is put away and the unit runs.
	c = r.feet().Add(mgl32.Vec3{2.9, 0, 0})
	r.moveTo(creeperID, c)
	f := &fuse{r: r, pos: c, speed: 0.2}
	f.run(60)
	if f.atBlast < defuseReach {
		t.Errorf("Ranger stood %.1f blocks from the fuse, want past %.1f", f.atBlast, defuseReach)
	}
	if n := swings(r); n != 0 {
		t.Errorf("%d sword blows from a Ranger with arrows", n)
	}
}

func TestOrdersAreNeverAbandonedForACreeper(t *testing.T) {
	for _, kind := range []OrderKind{OrderHold, OrderRegroup, OrderRetreat} {
		for _, name := range []string{"creeper/backoff", "creeper/ranged"} {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				scenario := func(r *rig) {
					r.setSquad(t, &fakeSquad{leaderID: "lead", leader: mgl32.Vec3{12.5, 1, 20.5}, leaderOK: true, order: Order{Kind: kind}})
					r.setHealth(20)
					c := mgl32.Vec3{25.0, 1, 20.5}
					r.spawn(creeperID, "minecraft:creeper", c)
					for i := 0; i < 40; i++ {
						r.step()
						c = c.Add(mgl32.Vec3{-0.1, 0, 0})
						r.moveTo(creeperID, c)
					}
				}
				// The profile alone widens how far a creeper is watched, so
				// the baseline is a tactic that takes nothing, not no tactic.
				withTactics(t, &passTactic{name: name, family: tactics.FamilyCreeper})
				base := replay(t, "striker", scenario, func(l *Life) {
					if err := l.SetProfile(creeperProfile(name)); err != nil {
						t.Fatal(err)
					}
				})
				withTactics(t, newCreeperTactic(name, name == "creeper/ranged"))
				got := replay(t, "striker", scenario, func(l *Life) {
					if err := l.SetProfile(creeperProfile(name)); err != nil {
						t.Fatal(err)
					}
				})
				if !reflect.DeepEqual(got, base) {
					t.Errorf("the tactic changed what a unit under a %s order does: %d packets, want %d", kind, len(got), len(base))
				}
			})
		}
	}
}

func TestTheDefaultProfileIsUntouchedByTheCreeperTactics(t *testing.T) {
	scenario := func(r *rig) {
		r.give(0, 301, 1)
		r.setHealth(20)
		c := mgl32.Vec3{25.0, 1, 20.5}
		r.spawn(creeperID, "minecraft:creeper", c)
		for i := 0; i < 50; i++ {
			r.step()
			if dist(r.feet(), c) > 3 {
				c = c.Add(mgl32.Vec3{-0.2, 0, 0})
				r.moveTo(creeperID, c)
			}
		}
	}
	for _, role := range []string{"vanguard", "striker", "ranger"} {
		t.Run(role, func(t *testing.T) {
			withTactics(t)
			base := replay(t, role, scenario, nil)
			withTactics(t, newCreeperTactic("creeper/backoff", false), newCreeperTactic("creeper/ranged", true))
			got := replay(t, role, scenario, nil)
			if !reflect.DeepEqual(got, base) {
				t.Errorf("registered creeper tactics changed the default fight: %d packets, want %d", len(got), len(base))
			}
		})
	}
}

// onAnIsland leaves a floor of the given half-width around the start and
// open air everywhere else, so there is nowhere to retreat to.
func onAnIsland(r *rig, half int32) {
	for x := int32(0); x < r.w.size; x++ {
		for z := int32(0); z < r.w.size; z++ {
			if x >= 20-half && x <= 20+half && z >= 20-half && z <= 20+half {
				continue
			}
			for y := int32(-2); y <= 0; y++ {
				r.w.over[protocol.BlockPos{x, y, z}] = "air"
			}
		}
	}
}

func TestACorneredUnitNeverWalksOffAnEdgeToGetAwayFromACreeper(t *testing.T) {
	for _, name := range []string{"creeper/backoff", "creeper/ranged"} {
		t.Run(name, func(t *testing.T) {
			r := creeperRig(t, "striker", name, nil)
			onAnIsland(r, 2)
			f := newFuse(r, mgl32.Vec3{22.5, 1, 20.5}, 0.0)
			for i := 0; i < 80; i++ {
				r.step()
				f.advance(i)
				feet := r.feet()
				if feet.Y() < 0.9 || feet.X() < 18 || feet.X() > 23 || feet.Z() < 18 || feet.Z() > 23 {
					t.Fatalf("tick %d: unit left the island at %v", i, feet)
				}
			}
		})
	}
}

func TestACreeperAcrossAGapIsNotChasedIntoIt(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	onAnIsland(r, 1)
	c := mgl32.Vec3{23.5, 1, 20.5}
	r.spawn(creeperID, "minecraft:creeper", c)
	for i := 0; i < 60; i++ {
		r.step()
		c = c.Add(mgl32.Vec3{-0.01, 0, 0})
		r.moveTo(creeperID, c)
		feet := r.feet()
		if feet.Y() < 0.9 || feet.X() < 19 || feet.X() > 22 || feet.Z() < 19 || feet.Z() > 22 {
			t.Fatalf("tick %d: unit left the island at %v", i, feet)
		}
	}
}

func TestACreeperThatNeverClosesIsLeftToTheRoleAfterAWhile(t *testing.T) {
	tc := newCreeperTactic("creeper/backoff", false)
	withTactics(t, tc)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(creeperProfile("creeper/backoff")); err != nil {
		t.Fatal(err)
	}
	r.setHealth(20)
	r.spawn(creeperID, "minecraft:creeper", mgl32.Vec3{25.0, 1, 20.5})
	r.run(waitPatience / 2)
	if d := dist(r.feet(), start); d > 0.01 {
		t.Fatalf("the unit moved %.2f blocks while waiting on the creeper", d)
	}
	r.run(waitPatience)
	if d := dist(r.feet(), start); d < 0.5 {
		t.Errorf("the unit still stood its ground %.2f blocks from where it started, after waiting out the patience", d)
	}
}
