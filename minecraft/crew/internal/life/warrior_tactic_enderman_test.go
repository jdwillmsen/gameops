package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	lowground = "enderman/lowground"
	ignoring  = "enderman/ignore"
)

// againstEnderman is a profile that fights the enderman family with the
// named tactic and everything else as shipped.
func againstEnderman(name string) tactics.Profile {
	pr := tactics.NewProfile("enderman " + name)
	pr.Tactics = map[tactics.Family]string{tactics.FamilyEnderman: name}
	return pr
}

func setEnderTactic(name string) func(*Life) {
	return func(l *Life) {
		if err := l.SetProfile(againstEnderman(name)); err != nil {
			panic(err)
		}
	}
}

// facing places an enderman with its head turned towards (or, with away, from)
// a point on the floor.
func (r *rig) endermanAt(id uint64, at mgl32.Vec3, towards *mgl32.Vec3, away bool) {
	yaw := float32(0)
	if towards != nil {
		yaw, _ = lookAt(at, *towards)
		if away {
			yaw += 180
		}
	}
	r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: id, Position: at, Rotation: mgl32.Vec3{0, yaw, yaw}})
}

// hitByEnderman lets an enderman beside the unit land a blow, as the first
// thing that makes the squad see it as a threat at all, and leaves it there.
func (r *rig) hitByEnderman(id uint64) {
	r.give(0, 303, 1)
	r.spawn(id, "minecraft:enderman", r.feet().Add(mgl32.Vec3{2, 0, 0}))
	r.step()
	r.hurt()
	r.step()
}

// nook roofs the cells x0..x1, z0..z1 two blocks above the floor, so a unit
// standing there has a ceiling just over its head.
func (r *rig) nook(x0, z0, x1, z1 int32) {
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			r.w.over[protocol.BlockPos{x, 3, z}] = "stone"
		}
	}
}

func inNook(r *rig, x0, z0, x1, z1 int) bool {
	f := r.feet()
	return int(f.X()) >= x0 && int(f.X()) <= x1 && int(f.Z()) >= z0 && int(f.Z()) <= z1
}

// sameAsDefault runs a scenario with the tactic named and with no tactic, and
// requires every packet to match.
func sameAsDefault(t *testing.T, role, tactic string, scenario func(*rig)) {
	t.Helper()
	base := replay(t, role, scenario, nil)
	if len(base) == 0 {
		t.Fatal("the scenario sent nothing")
	}
	got := replay(t, role, scenario, setEnderTactic(tactic))
	if !reflect.DeepEqual(got, base) {
		t.Errorf("%s changed the fight: %d packets, want the default's %d", tactic, len(got), len(base))
	}
}

func TestEndermanTacticsAreRegisteredForTheEndermanFamily(t *testing.T) {
	got := TacticNames(tactics.FamilyEnderman)
	want := []string{ignoring, lowground}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("enderman tactics = %v, want %v", got, want)
	}
}

func TestGazeOnTheHeadIsToldFromGazeOnTheGround(t *testing.T) {
	eye := mgl32.Vec3{0, 1 + enderEyeHeight, 0}
	e := belief.Entity{ID: 50, Type: "minecraft:enderman", Pos: mgl32.Vec3{12, 1, 0}}
	yawEast, _ := lookAt(eye, e.Pos)
	for name, tc := range map[string]struct {
		yaw, pitch float32
		want       bool
	}{
		"level at it, the head a little above the view":     {yawEast, 0, true},
		"aimed at the head":                                 {yawEast, -3, true},
		"aimed at its feet":                                 {yawEast, 15, false},
		"looking down at the floor":                         {yawEast, enderLookDownPitch, false},
		"turned away":                                       {yawEast + 90, 0, false},
		"turned a few degrees off the head":                 {yawEast + 4, 0, true},
		"turned well off the head at the same level":        {yawEast + 30, 0, false},
		"the other way round with the head behind the view": {yawEast + 180, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := enderGazeOnHead(eye, tc.yaw, tc.pitch, e); got != tc.want {
				t.Errorf("gaze on head = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAngleDiffWrapsAroundTheBackOfTheCircle(t *testing.T) {
	for _, tc := range []struct{ a, b, want float64 }{
		{10, 350, 20}, {350, 10, -20}, {179, -179, -2}, {0, 0, 0}, {90, -90, 180},
	} {
		got := enderAngleDiff(tc.a, tc.b)
		if got != tc.want && got != -tc.want {
			t.Errorf("enderAngleDiff(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---- enderman/ignore ----

// A calmed enderman is one that hit the unit a moment ago and now stands off
// with its head turned away.
func calmedEnderman(r *rig) {
	r.hitByEnderman(50)
	away := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &away, true)
	r.run(60)
}

func TestIgnoreLeavesAnEndermanThatLooksAwayAndDropsTheGrudge(t *testing.T) {
	r := newWarrior(t, "striker", start)
	setEnderTactic(ignoring)(r.l)
	r.hitByEnderman(50)
	away := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &away, true)
	// The blow is answered as any is for a second, until the enderman has
	// had the time to calm down or come again.
	r.run(enderFreshHit)
	held, swings := r.feet(), len(r.out.attacks())

	r.run(40)
	if d := dist(r.feet(), held); d > 0.5 {
		t.Errorf("the unit walked %.1f blocks after a calm enderman", d)
	}
	if n := len(r.out.attacks()) - swings; n != 0 {
		t.Errorf("%d swings at an enderman that was not after the unit", n)
	}
	if r.l.target.ID != 0 {
		t.Errorf("still fighting entity %d after the enderman calmed", r.l.target.ID)
	}
}

func TestIgnoreIsTheDefaultFightWhileTheEndermanFacesTheUnit(t *testing.T) {
	scenario := func(r *rig) {
		r.hitByEnderman(50)
		toward := r.feet()
		r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
		r.run(60)
	}
	sameAsDefault(t, "striker", ignoring, scenario)
}

func TestIgnoreIsTheDefaultFightWhileTheEndermanIsInReach(t *testing.T) {
	scenario := func(r *rig) {
		r.hitByEnderman(50)
		away := r.feet()
		r.endermanAt(50, r.feet().Add(mgl32.Vec3{2, 0, 0}), &away, true)
		r.run(40)
	}
	sameAsDefault(t, "striker", ignoring, scenario)
	r := newWarrior(t, "striker", start)
	setEnderTactic(ignoring)(r.l)
	scenario(r)
	if len(r.out.attacks()) == 0 {
		t.Error("never struck an enderman standing in reach")
	}
}

func TestIgnoreIsTheDefaultFightRightAfterABlow(t *testing.T) {
	scenario := func(r *rig) {
		r.hitByEnderman(50)
		away := r.feet()
		r.endermanAt(50, mgl32.Vec3{27.5, 1, 20.5}, &away, true)
		r.run(enderFreshHit - 4)
	}
	sameAsDefault(t, "striker", ignoring, scenario)
}

func TestIgnoreDoesNotFireUnderTheDefaultProfile(t *testing.T) {
	r := newWarrior(t, "striker", start)
	calmedEnderman(r)
	if d := dist(r.feet(), start); d < 2 {
		t.Errorf("the default fight moved %.1f blocks: it should have gone after the enderman", d)
	}
}

func TestIgnoreYieldsToOrdersAndOtherMobsAndTheRangersBow(t *testing.T) {
	for name, tc := range map[string]struct {
		role     string
		order    Order
		zombie   bool
		tactic   string
		scenario func(r *rig, sq *fakeSquad)
	}{
		"hold":     {role: "striker", order: Order{Kind: OrderHold}},
		"regroup":  {role: "striker", order: Order{Kind: OrderRegroup}},
		"retreat":  {role: "striker", order: Order{Kind: OrderRetreat}},
		"a zombie": {role: "striker", zombie: true},
		"a ranger": {role: "ranger"},
	} {
		for _, tactic := range []string{ignoring, lowground} {
			t.Run(name+"/"+tactic, func(t *testing.T) {
				scenario := func(r *rig) {
					sq := &fakeSquad{leaderID: "lead", leader: r.feet(), leaderOK: true, order: tc.order}
					r.setSquad(t, sq)
					r.hitByEnderman(50)
					if tc.zombie {
						r.spawn(51, "minecraft:zombie", r.feet().Add(mgl32.Vec3{0, 0, 2.5}))
					}
					away := r.feet()
					r.endermanAt(50, mgl32.Vec3{27.5, 1, 20.5}, &away, true)
					for i := 0; i < 40; i++ {
						if tc.zombie {
							// It keeps pace, as a zombie on a unit does.
							r.moveTo(51, r.feet().Add(mgl32.Vec3{0, 0, 2.5}))
						}
						r.step()
					}
				}
				sameAsDefault(t, tc.role, tactic, scenario)
			})
		}
	}
}

// ---- enderman/lowground ----

func TestLowgroundWalksIntoALowCeilingAndWaitsThereForTheEnderman(t *testing.T) {
	r := newWarrior(t, "striker", start)
	setEnderTactic(lowground)(r.l)
	r.nook(23, 19, 24, 22)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
	swings := len(r.out.attacks())

	r.run(40)
	if !inNook(r, 23, 19, 24, 22) {
		t.Fatalf("the unit stands at %v, not under the ceiling", r.feet())
	}
	held := r.feet()
	r.run(20)
	if d := dist(r.feet(), held); d > 0.01 {
		t.Errorf("left the cover for the enderman 5 blocks off: moved %.2f", d)
	}
	if n := len(r.out.attacks()) - swings; n != 0 {
		t.Errorf("%d swings at an enderman out of reach", n)
	}
}

func TestLowgroundKeepsItsGazeOffTheHeadOnTheWayIn(t *testing.T) {
	for name, nook := range map[string]bool{"to cover": true, "to the enderman": false} {
		t.Run(name, func(t *testing.T) {
			r := newWarrior(t, "striker", start)
			setEnderTactic(lowground)(r.l)
			if nook {
				r.nook(23, 19, 24, 22)
			}
			r.hitByEnderman(50)
			toward := r.feet()
			target := mgl32.Vec3{29.5, 1, 20.5}
			if !nook {
				target = mgl32.Vec3{34.5, 1, 20.5}
			}
			r.endermanAt(50, target, &toward, false)
			e, _ := r.l.ents.Get(50)
			for i := 0; i < 40; i++ {
				r.step()
				p := r.a.Pose()
				eye := p.Feet.Add(mgl32.Vec3{0, enderEyeHeight, 0})
				if enderGazeOnHead(eye, p.Yaw, p.Pitch, e) {
					t.Fatalf("tick %d: view yaw %.0f pitch %.0f is on the head from %v", i, p.Yaw, p.Pitch, p.Feet)
				}
			}
		})
	}
}

func TestLowgroundApproachesWhenThereIsNoCover(t *testing.T) {
	r := newWarrior(t, "striker", start)
	setEnderTactic(lowground)(r.l)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{30.5, 1, 20.5}, &toward, false)
	r.run(25)
	if d := dist(r.feet(), start); d < 3 {
		t.Errorf("only %.1f blocks towards the enderman with no cover to wait in", d)
	}
}

func TestLowgroundReacquiresAnEndermanThatTeleportsWithinATickOrTwo(t *testing.T) {
	// The teleport comes before the default's walk may be replanned, which is
	// the case the default is slowest in.
	turn := func(setup func(*Life)) int {
		r := newWarrior(t, "striker", start)
		if setup != nil {
			setup(r.l)
		}
		r.hitByEnderman(50)
		toward := r.feet()
		r.endermanAt(50, mgl32.Vec3{30.5, 1, 20.5}, &toward, false)
		r.run(3)
		if r.feet().X() <= start.X() {
			t.Fatal("the unit had not set off towards the first position")
		}
		// It reappears on the other side.
		r.endermanAt(50, mgl32.Vec3{10.5, 1, 20.5}, &toward, false)
		for i := 1; i <= 30; i++ {
			before := r.feet().X()
			r.step()
			if r.feet().X() < before {
				return i
			}
		}
		return 31
	}
	tactic, plain := turn(setEnderTactic(lowground)), turn(nil)
	if tactic > 3 {
		t.Errorf("took %d ticks to turn after the teleport, want at most 3", tactic)
	}
	if tactic >= plain {
		t.Errorf("turned in %d ticks, no faster than the default's %d", tactic, plain)
	}
}

func TestLowgroundIsTheDefaultFightOnceTheEndermanIsInReach(t *testing.T) {
	scenario := func(r *rig) {
		r.nook(23, 19, 24, 22)
		r.hitByEnderman(50)
		r.run(40)
	}
	sameAsDefault(t, "striker", lowground, scenario)
	r := newWarrior(t, "striker", start)
	setEnderTactic(lowground)(r.l)
	scenario(r)
	if len(r.out.attacks()) == 0 {
		t.Error("never struck an enderman standing in reach")
	}
}

func TestLowgroundHandsBackWhenTheEndermanIsTooFarToBeWaitedFor(t *testing.T) {
	r := newWarrior(t, "striker", start)
	setEnderTactic(lowground)(r.l)
	r.nook(20, 20, 21, 21)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{31.5, 1, 20.5}, &toward, false)
	r.run(40)
	if d := dist(r.feet(), start); d < 2 {
		t.Errorf("stayed in cover %.1f blocks from an enderman 11 blocks off", d)
	}
}

func TestLowgroundNeverWalksAUnitOutOfItsLeadersFocusRange(t *testing.T) {
	r := newWarrior(t, "striker", start)
	setEnderTactic(lowground)(r.l)
	r.nook(25, 20, 26, 21)
	sq := &fakeSquad{leaderID: "lead", leader: mgl32.Vec3{12.5, 1, 20.5}, leaderOK: true}
	r.setSquad(t, sq)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
	r.run(30)
	if inNook(r, 25, 20, 26, 21) {
		t.Errorf("took cover at %v, %.1f blocks from a leader whose focus range is %.0f",
			r.feet(), dist(r.feet(), sq.leader), tactics.Defaults().FocusRange)
	}
}

func TestLowgroundDoesNotFireUnderTheDefaultProfile(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.nook(23, 19, 24, 22)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
	r.run(40)
	if d := dist(r.feet(), mgl32.Vec3{28.5, 1, 20.5}); d > 4 {
		t.Errorf("the default fight stands %.1f blocks from the enderman: it should have closed on it", d)
	}
}

func TestLowgroundNeverSendsTwoInputsInATick(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	setEnderTactic(lowground)(r.l)
	r.nook(23, 19, 24, 22)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
	r.run(120)
}

// ---- endermite ----

func TestAnEndermiteIsFoughtAsTheDefaultFamilyWhateverIsSetForEndermen(t *testing.T) {
	if got := familyOf(belief.Entity{ID: 50, Type: "minecraft:endermite"}); got != tactics.FamilyDefault {
		t.Fatalf("an endermite is fought as %q, want the default family", got)
	}
	scenario := func(r *rig) {
		r.give(0, 303, 1)
		r.spawn(50, "minecraft:endermite", mgl32.Vec3{22.5, 1, 20.5})
		r.run(60)
	}
	base := replay(t, "striker", scenario, nil)
	for _, tactic := range []string{lowground, ignoring} {
		pr := tactics.NewProfile("endermen only")
		pr.Tactics = map[tactics.Family]string{tactics.FamilyEnderman: tactic}
		// Another family's tactic, set for the default one, must not run.
		pr.Tactics[tactics.FamilyDefault] = tactic
		got := replay(t, "striker", scenario, func(l *Life) {
			if err := l.SetProfile(pr); err != nil {
				t.Fatal(err)
			}
		})
		if !reflect.DeepEqual(got, base) {
			t.Errorf("%s changed the fight with an endermite: %d packets, want %d", tactic, len(got), len(base))
		}
	}
	r := newWarrior(t, "striker", start)
	scenario(r)
	if len(r.out.attacks()) == 0 {
		t.Error("never struck an endermite beside the unit")
	}
}
