package life

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	tacticPriority = "illager/priority"
	tacticSplit    = "illager/split"

	// Slots the scenarios stock: the sword is what a fight swings, the
	// shield and bow sit on the hotbar, the arrows and apple behind.
	swordSlot, shieldSlot, bowSlot, appleSlot, arrowSlot = 0, 1, 1, 3, 20
)

// illagerProfile fights the illager family with the named tactic.
func illagerProfile(tactic string) tactics.Profile {
	pr := tactics.NewProfile("illager " + tactic)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyIllager: {tactic}}
	return pr
}

// illagerRig is a warrior with a sword and, for a melee role, a shield; the
// profile is the default when tactic is empty.
func illagerRig(t *testing.T, role, tactic string) *rig {
	t.Helper()
	r := newWarrior(t, role, start)
	r.give(swordSlot, 303, 1)
	if role == "ranger" {
		r.give(bowSlot, 361, 1)
		r.give(arrowSlot, 362, 32)
	} else {
		r.give(shieldSlot, 360, 1)
	}
	if tactic != "" {
		if err := r.l.SetProfile(illagerProfile(tactic)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *rig) packets() []any {
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	return got
}

// untilAttack steps until another swing lands and returns who it hit, or 0
// when none did within max ticks.
func (r *rig) untilAttack(max int) uint64 {
	r.t.Helper()
	before := len(r.out.attacks())
	for i := 0; i < max; i++ {
		r.step()
		if got := r.out.attacks(); len(got) > before {
			return got[before].TargetEntityRuntimeID
		}
	}
	return 0
}

// facing is the head yaw of a mob at 'at' looking at the point 'to'.
func facing(at, to mgl32.Vec3) float32 {
	return float32(yawOf(mgl32.Vec2{to.X() - at.X(), to.Z() - at.Z()}))
}

// spawnIllager adds a mob whose head points at yaw.
func (r *rig) spawnIllager(id uint64, typ string, at mgl32.Vec3, yaw float32) {
	r.l.Apply(&packet.AddActor{EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: typ, Position: at, HeadYaw: yaw})
}

func (r *rig) remove(id uint64) {
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: int64(id)})
}

// trio is the three illager kinds around the unit at arm's length, with the
// vindicator on the lowest id so the shared focus would swing at it first.
func trio(r *rig) {
	r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{22.5, 1, 20.5}, 0)
	r.spawnIllager(51, "minecraft:pillager", mgl32.Vec3{20.5, 1, 22.5}, 0)
	r.spawnIllager(52, "minecraft:evoker", mgl32.Vec3{18.5, 1, 20.5}, 0)
}

func TestIllagerTacticsAreRegisteredForTheirFamily(t *testing.T) {
	want := []string{tacticPriority, tacticSplit}
	if got := TacticNames(tactics.FamilyIllager); !reflect.DeepEqual(got, want) {
		t.Errorf("TacticNames(illager) = %v, want %v", got, want)
	}
	for _, n := range want {
		if _, ok := tacticNamed(n, tactics.FamilyIllager); !ok {
			t.Errorf("%s is not offered to the illager family", n)
		}
	}
}

func TestPriorityKillsTheEvokerThenThePillagerThenTheVindicator(t *testing.T) {
	for _, role := range []string{"striker", "vanguard"} {
		t.Run(role, func(t *testing.T) {
			// The control: the shared focus swings at the lowest id.
			control := illagerRig(t, role, "")
			trio(control)
			if got := control.untilAttack(60); got != 50 {
				t.Fatalf("default profile hit %d first, want the vindicator 50", got)
			}

			r := illagerRig(t, role, tacticPriority)
			trio(r)
			for _, want := range []uint64{52, 51, 50} {
				got := r.untilAttack(120)
				if got != want {
					t.Fatalf("hit %d, want %d: the kill order is evoker, pillager, vindicator", got, want)
				}
				r.remove(want)
				r.run(2)
			}
		})
	}
}

func TestPriorityAnswersEveryEvokerAliasAndKeepsVexLast(t *testing.T) {
	r := illagerRig(t, "striker", tacticPriority)
	r.spawnIllager(50, "minecraft:vex", mgl32.Vec3{22.5, 1, 20.5}, 0)
	r.spawnIllager(51, "minecraft:vindicator", mgl32.Vec3{20.5, 1, 22.5}, 0)
	r.spawnIllager(52, "minecraft:evocation_illager", mgl32.Vec3{18.5, 1, 20.5}, 0)
	for _, want := range []uint64{52, 51} {
		if got := r.untilAttack(120); got != want {
			t.Fatalf("hit %d, want %d", got, want)
		}
		r.remove(want)
		r.run(2)
	}
	if got := r.untilAttack(120); got != 50 {
		t.Fatalf("hit %d, want the vex 50 once nothing else was left", got)
	}
}

func TestPriorityTurnsAChaseOverToAnEvokerThatArrivesLater(t *testing.T) {
	r := illagerRig(t, "striker", tacticPriority)
	r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{20.5, 1, 29.5}, 0)
	r.run(8)
	if z := r.feet().Z(); z <= start.Z()+0.5 {
		t.Fatalf("the unit has not started toward the vindicator (z=%.1f)", z)
	}
	r.spawnIllager(51, "minecraft:evoker", mgl32.Vec3{11.5, 1, 20.5}, 0)
	x0 := r.feet().X()
	r.run(30)
	if dx := x0 - r.feet().X(); dx < 3 {
		t.Errorf("the unit moved %.1f blocks toward the evoker in 30 ticks, want it turned from the vindicator", dx)
	}
}

func TestEvokerIsRushedWithoutShieldJumpOrCircling(t *testing.T) {
	scenario := func(r *rig) (left bool, shields int) {
		r.spawnIllager(50, "minecraft:evoker", mgl32.Vec3{28.5, 1, 20.5}, facing(mgl32.Vec3{28.5, 1, 20.5}, start))
		for i := 0; i < 150; i++ {
			r.step()
			if !r.a.Pose().OnGround {
				left = true
			}
			if len(r.out.attacks()) > 0 {
				break
			}
		}
		return left, r.out.uses(shieldSlot)
	}

	left, _ := scenario(illagerRig(t, "striker", ""))
	if !left {
		t.Fatal("the default Striker never jumped for the critical hit: the control proves nothing")
	}

	r := illagerRig(t, "striker", tacticPriority)
	left, shields := scenario(r)
	if len(r.out.attacks()) == 0 {
		t.Fatal("the unit never reached the evoker")
	}
	if left {
		t.Error("the unit left the ground on its way in or at the first swing; an evoker is struck on arrival")
	}
	if shields != 0 {
		t.Errorf("the shield went up %d times against an evoker", shields)
	}
	if d := dist(r.feet(), mgl32.Vec3{28.5, 1, 20.5}); d > meleeReach {
		t.Errorf("first swing from %.1f blocks off, want within reach", d)
	}

	// Between swings the unit stays put on the evoker instead of strafing,
	// once the walk's own momentum has died away.
	r.run(4)
	at := r.feet()
	for i := 0; i < attackEvery-2; i++ {
		r.step()
		if d := dist(r.feet(), at); d > 0.05 {
			t.Fatalf("the unit moved %.2f blocks between swings", d)
		}
	}
}

func TestPillagerAimingDownAThrowIsApproachedBehindTheShield(t *testing.T) {
	pillager := mgl32.Vec3{30.5, 1, 20.5}
	run := func(tactic string, yaw float32) (*rig, int) {
		r := illagerRig(t, "striker", tactic)
		r.spawnIllager(50, "minecraft:pillager", pillager, yaw)
		r.untilAttack(200)
		return r, r.out.uses(shieldSlot)
	}
	at := facing(pillager, start)

	if r, shields := run("", at); shields != 0 || len(r.out.attacks()) == 0 {
		t.Fatalf("control: %d shield raises and %d swings, want the plain approach", shields, len(r.out.attacks()))
	}

	r, shields := run(tacticPriority, at)
	if len(r.out.attacks()) == 0 {
		t.Fatal("the unit never closed on an aiming pillager: the guard stalled it")
	}
	if shields == 0 {
		t.Error("the shield never went up on the way in under a pillager's aim")
	}

	// The same pillager looking elsewhere is no reason for a shield.
	if _, shields := run(tacticPriority, at+180); shields != 0 {
		t.Errorf("shield raised %d times against a pillager facing away", shields)
	}
}

func TestRaisedShieldNeverOutlastsTheRoundSoTheUnitKeepsClosing(t *testing.T) {
	// A long MeleeShieldTick would otherwise guard for the whole round.
	pr := illagerProfile(tacticPriority)
	long := tactics.Defaults()
	long.MeleeShieldTick = 30
	pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyIllager: long}
	r := illagerRig(t, "striker", "")
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	pillager := mgl32.Vec3{30.5, 1, 20.5}
	r.spawnIllager(50, "minecraft:pillager", pillager, facing(pillager, start))
	if r.untilAttack(200) == 0 {
		t.Fatal("a maximal guard held the unit off the pillager for 200 ticks")
	}
}

func TestPillagerInReachIsLeftToTheMeleeFight(t *testing.T) {
	pillager := mgl32.Vec3{22.5, 1, 20.5}
	run := func(tactic string) []any {
		r := illagerRig(t, "striker", tactic)
		r.spawnIllager(50, "minecraft:pillager", pillager, facing(pillager, start))
		r.run(60)
		return r.packets()
	}
	if got, want := run(tacticPriority), run(""); !reflect.DeepEqual(got, want) {
		t.Error("a lone pillager at arm's length was fought differently with the tactic")
	}
}

func TestAVexAtArmsLengthIsSwattedOnTheWayToTheEvoker(t *testing.T) {
	r := illagerRig(t, "striker", tacticPriority)
	r.spawnIllager(50, "minecraft:evoker", mgl32.Vec3{29.5, 1, 20.5}, 0)
	r.spawnIllager(51, "minecraft:vex", mgl32.Vec3{21.5, 1, 20.5}, 0)
	if got := r.untilAttack(5); got != 51 {
		t.Fatalf("first swing at %d, want the vex 51 at arm's length", got)
	}
	r.remove(51)
	if got := r.untilAttack(200); got != 50 {
		t.Fatalf("next swing at %d, want the evoker 50 once the vex was gone", got)
	}
}

func TestSplitSendsEachRoleAtItsOwnIllager(t *testing.T) {
	t.Run("vanguard holds the vindicator", func(t *testing.T) {
		r := illagerRig(t, "vanguard", tacticSplit)
		trio(r)
		if got := r.untilAttack(60); got != 50 {
			t.Errorf("vanguard hit %d, want the vindicator 50", got)
		}
	})
	t.Run("striker goes for the evoker", func(t *testing.T) {
		r := illagerRig(t, "striker", tacticSplit)
		trio(r)
		if got := r.untilAttack(60); got != 52 {
			t.Errorf("striker hit %d, want the evoker 52", got)
		}
	})
	t.Run("ranger aims at the pillager", func(t *testing.T) {
		toward := func(tactic string) float32 {
			r := illagerRig(t, "ranger", tactic)
			trio(r)
			// The first tick, before the trio pins the ranger against the
			// evoker and it falls back on its sword.
			r.run(1)
			ins := r.out.inputs()
			return ins[len(ins)-1].Yaw
		}
		pillager := float32(0)
		if got := toward(tacticSplit); math.Abs(float64(got-pillager)) > 5 {
			t.Errorf("ranger faced yaw %.0f, want the pillager at %.0f", got, pillager)
		}
		// The control: the shared focus has the ranger on the vindicator, east.
		if got := toward(""); math.Abs(float64(got-(-90))) > 5 {
			t.Fatalf("control ranger faced yaw %.0f, want the vindicator at -90", got)
		}
	})
}

func TestSplitFallsBackToTheNextInTheRolesOrder(t *testing.T) {
	r := illagerRig(t, "vanguard", tacticSplit)
	r.spawnIllager(51, "minecraft:pillager", mgl32.Vec3{20.5, 1, 22.5}, 0)
	r.spawnIllager(52, "minecraft:evoker", mgl32.Vec3{18.5, 1, 20.5}, 0)
	if got := r.untilAttack(120); got != 51 {
		t.Fatalf("vanguard with no vindicator hit %d, want the pillager 51", got)
	}
}

func TestIllagerTacticDeclinesWhereTheRoleFightIsTheBetterIdea(t *testing.T) {
	west := mgl32.Vec3{12.5, 1, 20.5}
	for _, tc := range []struct {
		name  string
		role  string
		setup func(r *rig)
		// control says what the default run must have done, so the
		// comparison is of a fight that really happened.
		control func(t *testing.T, r *rig)
	}{
		{"a lone vindicator", "striker", func(r *rig) {
			r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{24.5, 1, 20.5}, 0)
		}, wantAttacks},
		{"a lone vindicator as the vanguard", "vanguard", func(r *rig) {
			r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{24.5, 1, 20.5}, 0)
		}, wantAttacks},
		{"a lone vex", "striker", func(r *rig) {
			r.spawnIllager(50, "minecraft:vex", mgl32.Vec3{23.5, 1, 20.5}, 0)
		}, wantAttacks},
		{"a lone vindicator for an armed ranger", "ranger", func(r *rig) {
			r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{28.5, 1, 20.5}, 0)
		}, func(t *testing.T, r *rig) {
			if r.out.releases(bowSlot) == 0 {
				t.Error("the control ranger never loosed an arrow")
			}
		}},
		{"a hold order", "striker", func(r *rig) {
			r.setSquad(t, &fakeSquad{leader: start, leaderOK: true, order: Order{Kind: OrderHold}})
			trio(r)
		}, wantAttacks},
		{"a regroup order", "striker", func(r *rig) {
			r.setSquad(t, &fakeSquad{leader: mgl32.Vec3{34.5, 1, 20.5}, leaderOK: true, order: Order{Kind: OrderRegroup}})
			trio(r)
		}, func(t *testing.T, r *rig) {
			if d := dist(r.feet(), mgl32.Vec3{34.5, 1, 20.5}); d > 6 {
				t.Errorf("the control unit is %.1f blocks from its leader, want it regrouped", d)
			}
		}},
		{"a retreat order", "striker", func(r *rig) {
			r.setSquad(t, &fakeSquad{leader: start, leaderOK: true, order: Order{Kind: OrderRetreat}})
			r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{22.5, 1, 20.5}, 0)
			r.spawnIllager(51, "minecraft:evoker", west, 0)
		}, func(t *testing.T, r *rig) {
			if len(r.out.attacks()) != 0 {
				t.Error("the control unit swung while under a retreat order")
			}
		}},
		{"a meal to eat", "striker", func(r *rig) {
			r.give(appleSlot, 363, 2)
			r.setHealth(10)
			r.spawnIllager(50, "minecraft:vindicator", mgl32.Vec3{29.5, 1, 20.5}, 0)
			r.spawnIllager(51, "minecraft:evoker", west, 0)
		}, func(t *testing.T, r *rig) {
			if r.out.uses(appleSlot) == 0 {
				t.Error("the control unit never ate")
			}
		}},
		{"an order naming the vindicator", "striker", func(r *rig) {
			r.setSquad(t, &fakeSquad{leader: start, leaderOK: true, order: Order{Kind: OrderFocus, Target: "vindicator"}})
			r.spawnIllager(50, "minecraft:evoker", mgl32.Vec3{18.5, 1, 20.5}, 0)
			r.spawnIllager(51, "minecraft:vindicator", mgl32.Vec3{22.5, 1, 20.5}, 0)
		}, func(t *testing.T, r *rig) {
			if got := r.out.attacks(); len(got) == 0 || got[0].TargetEntityRuntimeID != 51 {
				t.Error("the control unit did not follow the order onto the vindicator")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(tactic string) *rig {
				r := illagerRig(t, tc.role, tactic)
				tc.setup(r)
				r.run(80)
				return r
			}
			base := run("")
			tc.control(t, base)
			for _, tactic := range []string{tacticPriority, tacticSplit} {
				r := run(tactic)
				if !reflect.DeepEqual(r.packets(), base.packets()) {
					t.Errorf("%s changed the fight: %d packets, want the role fight's %d", tactic, len(r.out.got), len(base.out.got))
				}
			}
		})
	}
}

func wantAttacks(t *testing.T, r *rig) {
	t.Helper()
	if len(r.out.attacks()) == 0 {
		t.Error("the control unit never swung")
	}
}

func TestTheDefaultProfileFightsIllagersAsShipped(t *testing.T) {
	scenario := func(r *rig) {
		trio(r)
		r.spawnIllager(53, "minecraft:vex", mgl32.Vec3{21.5, 1, 21.5}, 0)
		r.run(80)
	}
	var base []any
	t.Run("no illager tactic registered", func(t *testing.T) {
		withTactics(t)
		r := illagerRig(t, "striker", "")
		scenario(r)
		base = r.packets()
	})
	r := illagerRig(t, "striker", "")
	scenario(r)
	if len(base) == 0 || !reflect.DeepEqual(r.packets(), base) {
		t.Errorf("registering the illager tactics changed the default fight: %d packets, want %d", len(r.packets()), len(base))
	}

	// The positive control: a profile that names one does fight differently.
	named := illagerRig(t, "striker", tacticPriority)
	scenario(named)
	if reflect.DeepEqual(named.packets(), base) {
		t.Error("naming the tactic changed nothing: the comparison above proves nothing")
	}
}

func TestEvokerAcrossAGapIsNeverChasedIntoTheVoid(t *testing.T) {
	// The same evoker on solid ground is reached, so the gap is what held
	// the unit back.
	open := illagerRig(t, "striker", tacticPriority)
	open.spawnIllager(50, "minecraft:evoker", mgl32.Vec3{30.5, 1, 20.5}, 0)
	if open.untilAttack(200) != 50 {
		t.Fatal("control: the evoker was not reached over open ground")
	}

	r := illagerRig(t, "striker", tacticPriority)
	r.raise(24, -2, 0, 26, 0, 39, "air")
	r.spawnIllager(50, "minecraft:evoker", mgl32.Vec3{30.5, 1, 20.5}, 0)
	for i := 0; i < 160; i++ {
		r.step()
		f := r.feet()
		if f.X() >= 24 || f.Y() < 1 {
			t.Fatalf("tick %d: unit at %v, past the edge of the ground", i, f)
		}
	}
}

// mixedPack is a zombie on the lowest id, so the shared focus picks it, with
// an evoker and a pillager a little further off.
func mixedPack(r *rig) {
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.spawnIllager(51, "minecraft:evoker", mgl32.Vec3{18.5, 1, 20.5}, 0)
	r.spawnIllager(52, "minecraft:pillager", mgl32.Vec3{20.5, 1, 22.5}, 0)
}

func squadFocus(t *testing.T, r *rig) uint64 {
	t.Helper()
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true})
	anchor, _ := r.l.warrior.leader(r.a.Pose())
	e, _ := r.l.warriorTarget(r.l.Profile(), r.a.Pose(), anchor)
	return e.ID
}

func TestPriorityPullsTheSquadFocusToTheEvokerBesideAZombie(t *testing.T) {
	base := illagerRig(t, "striker", "")
	mixedPack(base)
	if got := squadFocus(t, base); got != 50 {
		t.Fatalf("control focus = %d, want the zombie 50 on the lowest id", got)
	}

	r := illagerRig(t, "striker", tacticPriority)
	mixedPack(r)
	if got := squadFocus(t, r); got != 51 {
		t.Errorf("focus = %d, want the evoker 51 first in the kill order", got)
	}
	if got := r.untilAttack(120); got != 51 {
		t.Errorf("the unit hit %d, want the evoker 51 before the zombie", got)
	}
}

func TestPriorityFocusFallsToTheNextInTheOrderThenToTheDefault(t *testing.T) {
	r := illagerRig(t, "striker", tacticPriority)
	mixedPack(r)
	r.remove(51)
	if got := squadFocus(t, r); got != 52 {
		t.Errorf("focus = %d, want the pillager 52 with the evoker gone", got)
	}
	r.remove(52)
	if got := squadFocus(t, r); got != 50 {
		t.Errorf("focus = %d, want the lone zombie 50 once no illager is left", got)
	}
}

func TestEveryRoleOnAProfileAgreesOnTheMixedPackFocus(t *testing.T) {
	for _, tactic := range []string{"", tacticPriority, tacticSplit} {
		focus := map[uint64]string{}
		for _, role := range []string{"vanguard", "striker", "ranger"} {
			r := illagerRig(t, role, tactic)
			mixedPack(r)
			focus[squadFocus(t, r)] = role
		}
		if len(focus) != 1 {
			t.Errorf("tactic %q: roles split the squad across %v", tactic, focus)
		}
	}
}

func TestSplitLeavesTheMixedPackToTheSharedFocus(t *testing.T) {
	r := illagerRig(t, "striker", tacticSplit)
	mixedPack(r)
	if got := squadFocus(t, r); got != 50 {
		t.Errorf("focus = %d, want the shared pick 50: split's order is per role and cannot steer the squad", got)
	}
}

func TestAStandingOrderForATypeBeatsTheKillOrderPreference(t *testing.T) {
	r := illagerRig(t, "striker", tacticPriority)
	mixedPack(r)
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true,
		order: Order{Kind: OrderFocus, Target: "minecraft:zombie"}})
	anchor, _ := r.l.warrior.leader(r.a.Pose())
	if e, _ := r.l.warriorTarget(r.l.Profile(), r.a.Pose(), anchor); e.ID != 50 {
		t.Errorf("focus = %d, want the zombie 50 the order named", e.ID)
	}
}

func TestAPreferenceOutsideTheCandidatesIsIgnored(t *testing.T) {
	withTactics(t, strayPreferrer{})
	r := newWarrior(t, "striker", start)
	pr := tactics.NewProfile("stray")
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {"zombie/stray"}}
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.spawn(51, "minecraft:zombie", mgl32.Vec3{18.5, 1, 20.5})
	if got := squadFocus(t, r); got != 50 {
		t.Errorf("focus = %d, want the default 50 when Prefer names a mob that is no candidate", got)
	}
}

type strayPreferrer struct{}

func (strayPreferrer) Name() string           { return "zombie/stray" }
func (strayPreferrer) Family() tactics.Family { return tactics.FamilyZombie }
func (strayPreferrer) Engage(context.Context, *Fight) (bool, error) {
	return false, nil
}
func (strayPreferrer) Prefer(*Fight, []belief.Entity) (belief.Entity, bool) {
	return belief.Entity{ID: 999}, true
}

func TestAHoldingOrderKeepsTheSharedFocusOverTheKillOrder(t *testing.T) {
	for _, kind := range []OrderKind{OrderHold, OrderRegroup, OrderRetreat} {
		r := illagerRig(t, "striker", tacticPriority)
		mixedPack(r)
		r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true, order: Order{Kind: kind}})
		anchor, _ := r.l.warrior.leader(r.a.Pose())
		if e, _ := r.l.warriorTarget(r.l.Profile(), r.a.Pose(), anchor); e.ID != 50 {
			t.Errorf("%s order: focus = %d, want the shared pick 50", kind, e.ID)
		}
	}
}
