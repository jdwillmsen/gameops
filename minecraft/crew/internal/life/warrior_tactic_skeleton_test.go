package life

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	swordItem  = 303
	shieldItem = 360
	bowItem    = 361
	arrowItem  = 362
	shieldSlot = 1
)

// usingSkeletonTactic is a profile that fights the skeleton family with the
// named tactic.
func usingSkeletonTactic(name string) tactics.Profile {
	pr := tactics.NewProfile("tactic " + name)
	pr.Tactics = map[tactics.Family]string{tactics.FamilySkeleton: name}
	return pr
}

// aimedAtUnit is the head yaw that points a mob at a unit.
func aimedAtUnit(mob, unit mgl32.Vec3) float32 {
	return float32(yawOf(mgl32.Vec2{unit.X() - mob.X(), unit.Z() - mob.Z()}))
}

// spawnArcher puts an archer in the world with its head on the unit.
func (r *rig) spawnArcher(id uint64, typ string, at mgl32.Vec3) {
	r.l.Apply(&packet.AddActor{EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: typ, Position: at, HeadYaw: aimedAtUnit(at, r.feet())})
}

// keepAimed turns the archer's head back on the unit, as a real one does as
// the unit moves.
func (r *rig) keepAimed(id uint64, at mgl32.Vec3) {
	r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: id, Position: at, Rotation: mgl32.Vec3{0, 0, aimedAtUnit(at, r.feet())}})
}

// armed gives the unit a sword and a shield.
func (r *rig) armed() {
	r.give(0, swordItem, 1)
	r.give(shieldSlot, shieldItem, 1)
}

// fightOf is the Fight the warrior engine would offer a tactic this tick.
func (r *rig) fightOf(t *testing.T) *Fight {
	t.Helper()
	snap := r.tracker.Snapshot()
	if !r.l.Engaged(r.a.Pose(), snap) {
		t.Fatal("the unit is not engaged")
	}
	e, ok := r.l.ents.Get(r.l.target.ID)
	if !ok {
		t.Fatal("the target is not in the world")
	}
	return &Fight{Life: r.l, Actuator: r.a, Pose: r.a.Pose(), Target: e, Snapshot: snap, Params: r.l.warrior.p, Family: tactics.FamilySkeleton}
}

// archerAt is within a leader's focus range at leaderAt, and 8 blocks from
// the unit's start.
var (
	archerAt = mgl32.Vec3{28.5, 1, 20.5}
	leaderAt = mgl32.Vec3{17.5, 1, 20.5}
)

// skeletonCases are the situations a skeleton tactic is offered. take lists
// the tactics expected to handle the tick.
var skeletonCases = []struct {
	name  string
	role  string
	build func(t *testing.T, r *rig)
	zig   bool
	flank bool
}{
	{name: "a striker with a leader closing on a skeleton", role: "striker", zig: true, flank: true,
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
			r.spawnArcher(50, "minecraft:skeleton", archerAt)
		}},
	{name: "a striker with no leader", role: "striker", zig: true, flank: false,
		build: func(t *testing.T, r *rig) { r.spawnArcher(50, "minecraft:skeleton", archerAt) }},
	{name: "a ranger with no bow", role: "ranger", zig: true, flank: false,
		build: func(t *testing.T, r *rig) { r.spawnArcher(50, "minecraft:stray", archerAt) }},
	{name: "a bogged", role: "striker", zig: true, flank: false,
		build: func(t *testing.T, r *rig) { r.spawnArcher(50, "minecraft:bogged", archerAt) }},
	{name: "a wither skeleton", role: "striker",
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
			r.spawnArcher(50, "minecraft:wither_skeleton", archerAt)
		}},
	{name: "the vanguard", role: "vanguard",
		build: func(t *testing.T, r *rig) { r.spawnArcher(50, "minecraft:skeleton", archerAt) }},
	{name: "a ranger with a bow", role: "ranger",
		build: func(t *testing.T, r *rig) {
			r.give(2, bowItem, 1)
			r.give(20, arrowItem, 32)
			r.spawnArcher(50, "minecraft:skeleton", archerAt)
		}},
	{name: "a hold order", role: "striker",
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderHold}})
			r.spawnArcher(50, "minecraft:skeleton", archerAt)
		}},
	{name: "a retreat order", role: "striker",
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderRetreat}})
			r.spawnArcher(50, "minecraft:skeleton", archerAt)
		}},
	{name: "a regroup order away from the leader", role: "striker",
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{10.5, 1, 20.5}, leaderOK: true, order: Order{Kind: OrderRegroup}})
			r.spawnArcher(50, "minecraft:skeleton", mgl32.Vec3{21.5, 1, 20.5})
		}},
	{name: "a hurt unit with food to eat", role: "striker",
		build: func(t *testing.T, r *rig) {
			r.give(3, 363, 2)
			r.setHealth(10)
			r.spawnArcher(50, "minecraft:skeleton", archerAt)
		}},
	{name: "an archer past bow range", role: "striker",
		build: func(t *testing.T, r *rig) {
			pr := tactics.NewProfile("far")
			far := tactics.Defaults()
			far.FocusRange = 20
			pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilySkeleton: far}
			if err := r.l.SetProfile(pr); err != nil {
				t.Fatal(err)
			}
			r.spawnArcher(50, "minecraft:skeleton", mgl32.Vec3{38.5, 1, 20.5})
		}},
	{name: "an archer on another floor", role: "striker",
		build: func(t *testing.T, r *rig) {
			for x := int32(26); x < 34; x++ {
				for z := int32(16); z < 25; z++ {
					r.w.over[protocol.BlockPos{x, 2, z}] = "stone"
				}
			}
			r.spawnArcher(50, "minecraft:skeleton", mgl32.Vec3{30.5, 3, 20.5})
		}},
	{name: "a ledge where the weave would step off", role: "striker",
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
			for x := int32(21); x < 40; x++ {
				for z := int32(0); z < 40; z++ {
					for y := int32(-2); y <= 0; y++ {
						r.w.over[protocol.BlockPos{x, y, z}] = "air"
					}
				}
			}
			r.spawn(50, "minecraft:skeleton", mgl32.Vec3{27.5, 1, 20.5})
		}},
	{name: "lava ahead", role: "striker", flank: true,
		build: func(t *testing.T, r *rig) {
			r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
			for z := int32(18); z <= 22; z++ {
				r.w.over[protocol.BlockPos{21, 0, z}] = "lava"
			}
			r.spawn(50, "minecraft:skeleton", archerAt)
		}},
}

func TestSkeletonTacticsTakeTheTickOnlyWhereTheyFit(t *testing.T) {
	for _, tc := range skeletonCases {
		for _, tac := range []struct {
			tactic FamilyTactic
			want   bool
		}{{skeletonZigzag{}, tc.zig}, {skeletonFlank{}, tc.flank}} {
			t.Run(tc.name+" "+tac.tactic.Name(), func(t *testing.T) {
				r := newWarrior(t, tc.role, start)
				r.armed()
				tc.build(t, r)
				f := r.fightOf(t)
				before := len(r.out.got)
				handled, err := tac.tactic.Engage(context.Background(), f)
				if err != nil {
					t.Fatal(err)
				}
				if handled != tac.want {
					t.Fatalf("handled = %v, want %v", handled, tac.want)
				}
				sent := len(r.out.got) - before
				switch {
				case !handled && sent != 0:
					t.Errorf("declined the tick but sent %d packets", sent)
				case handled && len(r.out.inputs()) != 1:
					t.Errorf("handled the tick with %d inputs, want exactly 1", len(r.out.inputs()))
				}
			})
		}
	}
}

func TestSkeletonTacticsAreFamilyBoundAndNamed(t *testing.T) {
	for _, tac := range []FamilyTactic{skeletonZigzag{}, skeletonFlank{}} {
		if tac.Family() != tactics.FamilySkeleton {
			t.Errorf("%s fights the %q family", tac.Name(), tac.Family())
		}
	}
	names := TacticNames(tactics.FamilySkeleton)
	for _, want := range []string{"skeleton/flank", "skeleton/zigzag"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("%s is not registered; skeleton tactics are %v", want, names)
		}
	}
}

// weaveRun walks a striker with a sword and shield up to an archer that
// stands its ground with its head on the unit, and reports what it did.
type weaveRun struct {
	minZ, maxZ    float64
	firstSwing    int
	shieldUp      int
	shieldAtSwing bool
	closed        bool
}

func runAtArcher(t *testing.T, set func(*Life)) weaveRun {
	t.Helper()
	r := newWarrior(t, "striker", start)
	set(r.l)
	r.armed()
	r.spawnArcher(50, "minecraft:skeleton", archerAt)
	run := weaveRun{minZ: 1e9, maxZ: -1e9, firstSwing: -1}
	for i := 0; i < 160 && run.firstSwing < 0; i++ {
		r.keepAimed(50, archerAt)
		attacks := len(r.out.attacks())
		r.step()
		z := float64(r.feet().Z() - start.Z())
		run.minZ, run.maxZ = math.Min(run.minZ, z), math.Max(run.maxZ, z)
		if len(r.out.attacks()) > attacks {
			run.firstSwing = i
			run.shieldAtSwing = r.l.warrior.shield
		}
	}
	run.shieldUp = r.out.uses(shieldSlot)
	run.closed = dist(r.feet(), archerAt) <= meleeReach
	return run
}

func TestZigzagWeavesInOnAnArcherAndLowersTheShieldToSwing(t *testing.T) {
	zig := runAtArcher(t, func(l *Life) {
		if err := l.SetProfile(usingSkeletonTactic("skeleton/zigzag")); err != nil {
			t.Fatal(err)
		}
	})
	base := runAtArcher(t, func(*Life) {})

	if !zig.closed || zig.firstSwing < 0 {
		t.Fatalf("the weave never reached the archer: closed=%v firstSwing=%d", zig.closed, zig.firstSwing)
	}
	if zig.minZ > -0.3 || zig.maxZ < 0.3 {
		t.Errorf("the track stayed within z %.2f..%.2f of the line, want sideways travel on both sides", zig.minZ, zig.maxZ)
	}
	if base.maxZ-base.minZ > 0.5 {
		t.Errorf("the role fight swayed %.2f blocks off the line; the contrast needs a straight walk", base.maxZ-base.minZ)
	}
	if zig.shieldUp == 0 {
		t.Error("the shield never went up on the way in against an archer aiming at the unit")
	}
	if zig.shieldAtSwing {
		t.Error("swung with the shield still up")
	}
}

func TestZigzagStandsBehindTheShieldOnlyWhileTheArcherAimsAtTheUnit(t *testing.T) {
	pr := usingSkeletonTactic("skeleton/zigzag")
	for name, aimed := range map[string]bool{"aimed": true, "looking away": false} {
		t.Run(name, func(t *testing.T) {
			r := newWarrior(t, "striker", start)
			if err := r.l.SetProfile(pr); err != nil {
				t.Fatal(err)
			}
			r.armed()
			r.spawnArcher(50, "minecraft:skeleton", archerAt)
			for i := 0; i < 12; i++ {
				if aimed {
					r.keepAimed(50, archerAt)
				} else {
					r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: archerAt, Rotation: mgl32.Vec3{0, 0, aimedAtUnit(archerAt, r.feet()) + 120}})
				}
				r.step()
			}
			if got := r.out.uses(shieldSlot) > 0; got != aimed {
				t.Errorf("shield raised = %v, want %v", got, aimed)
			}
			if !aimed && dist(r.feet(), start) < 1 {
				t.Errorf("the unit moved %.2f blocks in 12 ticks with nothing to guard against", dist(r.feet(), start))
			}
		})
	}
}

func TestZigzagGuardIsAPauseNotAStallAgainstAnArcherThatNeverLooksAway(t *testing.T) {
	zig := runAtArcher(t, func(l *Life) {
		if err := l.SetProfile(usingSkeletonTactic("skeleton/zigzag")); err != nil {
			t.Fatal(err)
		}
	})
	// The archer stands 10 blocks off and never stops aiming, yet the unit
	// is in reach well before the sixtieth tick: guarded pauses are
	// followed by advance windows.
	if zig.firstSwing < 0 || zig.firstSwing > 100 {
		t.Errorf("first swing at tick %d, want the approach to finish despite a constant aim", zig.firstSwing)
	}
}

func TestZigzagRaisesTheShieldForAnArrowInFlightWithoutAnAim(t *testing.T) {
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingSkeletonTactic("skeleton/zigzag")); err != nil {
		t.Fatal(err)
	}
	r.armed()
	r.spawnArcher(50, "minecraft:skeleton", archerAt)
	r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: archerAt, Rotation: mgl32.Vec3{0, 0, aimedAtUnit(archerAt, r.feet()) + 120}})

	arrow := mgl32.Vec3{25.5, 1.5, 20.5}
	r.spawn(70, "minecraft:arrow", arrow)
	for i := 0; i < 6; i++ {
		r.step()
		arrow = arrow.Sub(mgl32.Vec3{0.35, 0, 0})
		r.moveTo(70, arrow)
	}
	if r.out.uses(shieldSlot) == 0 {
		t.Fatal("an arrow closing on the unit never raised the shield")
	}
	r.moveTo(70, arrow.Sub(mgl32.Vec3{40, 0, 0}))
	r.run(4)
	if r.l.warrior.shield {
		t.Error("the shield stayed up after the arrow was gone and nothing aimed at the unit")
	}
}

func TestZigzagHoldsTheShieldBetweenSwingsAtPointBlank(t *testing.T) {
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingSkeletonTactic("skeleton/zigzag")); err != nil {
		t.Fatal(err)
	}
	r.armed()
	at := mgl32.Vec3{22.5, 1, 20.5}
	r.spawnArcher(50, "minecraft:skeleton", at)
	swings, shieldedBetween := 0, false
	for i := 0; i < 60; i++ {
		r.keepAimed(50, at)
		attacks := len(r.out.attacks())
		r.step()
		if len(r.out.attacks()) > attacks {
			swings++
			if r.l.warrior.shield {
				t.Fatal("swung with the shield up")
			}
		} else if r.l.warrior.shield {
			shieldedBetween = true
		}
	}
	if swings < 2 {
		t.Fatalf("%d swings at an adjacent archer, want the role fight's cadence", swings)
	}
	if !shieldedBetween {
		t.Error("never raised the shield between swings against an adjacent archer aiming at the unit")
	}
}

func TestZigzagPrefersTheSideCoveredFromTheArchersLine(t *testing.T) {
	setup := func(t *testing.T, pillar bool) *rig {
		r := newWarrior(t, "striker", start)
		if pillar {
			for _, y := range []int32{1, 2} {
				r.w.over[protocol.BlockPos{22, y, 21}] = "stone"
			}
		}
		r.spawnArcher(50, "minecraft:skeleton", archerAt)
		return r
	}
	headingZ := func(r *rig, side int) float64 {
		e, _ := r.l.ents.Get(50)
		yaw, ok := r.l.skeletonHeading(r.a.Pose(), e, side, skelAngle)
		if !ok {
			t.Fatal("no heading found on open ground")
		}
		return math.Cos(yaw * math.Pi / 180)
	}
	open := setup(t, false)
	if got := headingZ(open, -1); got >= 0 {
		t.Fatalf("open ground, side -1: heading z component %.2f, want the leg's own side (towards -z)", got)
	}
	covered := setup(t, true)
	if got := headingZ(covered, -1); got <= 0 {
		t.Errorf("with a pillar hiding the +z side: heading z component %.2f, want the covered side", got)
	}
}

func TestZigzagHandsAnUnclosableWalkToThePathingLayer(t *testing.T) {
	from := mgl32.Vec3{8.5, 1, 20.5}
	r := newWarrior(t, "striker", from)
	if err := r.l.SetProfile(usingSkeletonTactic("skeleton/zigzag")); err != nil {
		t.Fatal(err)
	}
	r.armed()
	r.spawnArcher(50, "minecraft:skeleton", from.Add(mgl32.Vec3{8, 0, 0}))
	for i := 0; i < 160; i++ {
		// The archer backs off as fast as the unit comes on.
		at := mgl32.Vec3{r.feet().X() + 8, 1, 20.5}
		r.keepAimed(50, at)
		r.step()
	}
	st := skelStateOf(r.l)
	if st.yieldUntil == 0 {
		t.Fatal("the weave kept going for 160 ticks without closing and never yielded")
	}
}

func TestSkeletonTacticsAreSilentUnderTheDefaultProfile(t *testing.T) {
	scenario := func(r *rig) {
		r.setSquad(r.t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
		r.armed()
		r.spawnArcher(50, "minecraft:skeleton", archerAt)
		for i := 0; i < 80; i++ {
			r.keepAimed(50, archerAt)
			r.step()
		}
	}
	withTactics(t)
	base := replay(t, "striker", scenario, nil)
	if len(base) == 0 {
		t.Fatal("the scenario sent nothing")
	}
	withTactics(t, skeletonZigzag{}, skeletonFlank{})
	got := replay(t, "striker", scenario, nil)
	if !reflect.DeepEqual(got, base) {
		t.Errorf("registering the skeleton tactics changed the default fight: %d packets, want %d", len(got), len(base))
	}

	named := replay(t, "striker", scenario, func(l *Life) {
		if err := l.SetProfile(usingSkeletonTactic("skeleton/zigzag")); err != nil {
			t.Fatal(err)
		}
	})
	if reflect.DeepEqual(named, base) {
		t.Error("naming the zigzag changed nothing; the contrast with the default is empty")
	}
}

func TestSkeletonTacticsLeaveOtherFamiliesToTheirOwnFight(t *testing.T) {
	scenario := func(r *rig) {
		r.give(0, swordItem, 1)
		r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
		r.run(40)
	}
	base := replay(t, "striker", scenario, nil)
	pr := tactics.NewProfile("skeleton tactics only")
	pr.Tactics = map[tactics.Family]string{tactics.FamilySkeleton: "skeleton/zigzag"}
	got := replay(t, "striker", scenario, func(l *Life) {
		if err := l.SetProfile(pr); err != nil {
			t.Fatal(err)
		}
	})
	if !reflect.DeepEqual(got, base) {
		t.Error("a skeleton tactic changed a fight with a zombie")
	}
}

func TestFlankKeepsTheShieldUpAgainstAnArrowOnTheWayToTheFlank(t *testing.T) {
	run := func(set func(*Life)) (shielded bool) {
		r := newWarrior(t, "striker", start)
		set(r.l)
		r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
		r.armed()
		r.spawnArcher(50, "minecraft:skeleton", archerAt)
		r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: archerAt, Rotation: mgl32.Vec3{0, 0, aimedAtUnit(archerAt, r.feet()) + 120}})

		arrow := mgl32.Vec3{26.5, 1.5, 20.5}
		r.spawn(70, "minecraft:arrow", arrow)
		for i := 0; i < 120; i++ {
			if i < 8 {
				arrow = arrow.Sub(mgl32.Vec3{0.35, 0, 0})
				r.moveTo(70, arrow)
			} else {
				r.moveTo(70, mgl32.Vec3{-40, 1.5, 20.5})
			}
			r.step()
			shielded = shielded || r.out.uses(shieldSlot) > 0
		}
		return shielded
	}
	flank := run(func(l *Life) {
		if err := l.SetProfile(usingSkeletonTactic("skeleton/flank")); err != nil {
			t.Fatal(err)
		}
	})
	base := run(func(*Life) {})

	if !flank {
		t.Error("the flank walk never raised the shield for the arrow")
	}
	if base {
		t.Error("the role fight raised the shield on the way in; the contrast needs it bare")
	}
}

func TestFlankWalksTheApproachAndLeavesTheSwingToTheRole(t *testing.T) {
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingSkeletonTactic("skeleton/flank")); err != nil {
		t.Fatal(err)
	}
	r.setSquad(t, &fakeSquad{leaderID: "crew-18", leader: leaderAt, leaderOK: true})
	r.armed()
	r.spawnArcher(50, "minecraft:skeleton", archerAt)
	for i := 0; i < 150 && len(r.out.attacks()) == 0; i++ {
		r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: archerAt, Rotation: mgl32.Vec3{0, 0, aimedAtUnit(archerAt, r.feet()) + 120}})
		r.step()
	}
	if len(r.out.attacks()) == 0 {
		t.Fatalf("never swung at the archer: %.1f blocks off", dist(r.feet(), archerAt))
	}
	if w := r.l.warrior; w.shield {
		t.Error("swung with the shield up")
	}
}
