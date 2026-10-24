package life

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// fakeRoster is a squad that also knows its members, its losses and where
// it started.
type fakeRoster struct {
	fakeSquad
	members map[Role]mgl32.Vec3
	down    int
	start   mgl32.Vec3
	startOK bool
}

func (f *fakeRoster) Member(r Role) (mgl32.Vec3, bool) {
	p, ok := f.members[r]
	return p, ok
}
func (f *fakeRoster) Down() int                 { return f.down }
func (f *fakeRoster) Start() (mgl32.Vec3, bool) { return f.start, f.startOK }

// arena is the middle of a floor big enough for the Ranger's band.
var arena = mgl32.Vec3{40.5, 1, 40.5}

// newBossWarrior rigs a warrior with a full kit on an 80-block floor, a
// warden standing in the middle facing +z, and a roster whose leader and
// Vanguard stand at the warden's side.
func newBossWarrior(t *testing.T, role string, feet mgl32.Vec3) (*rig, *fakeRoster) {
	t.Helper()
	r := newWarrior(t, role, feet)
	r.w.size = 80
	r.give(0, 303, 1)   // diamond sword
	r.give(1, 360, 1)   // shield
	r.give(2, 361, 1)   // bow
	r.give(20, 362, 64) // arrows
	van := arena.Add(mgl32.Vec3{0, 0, -2})
	squad := &fakeRoster{
		fakeSquad: fakeSquad{leaderID: "crew-18", leader: van, leaderOK: true},
		members:   map[Role]mgl32.Vec3{RoleVanguard: van},
	}
	r.l.SetSquad(squad)
	r.spawn(90, "minecraft:warden", arena)
	return r, squad
}

// faceWarden turns the warden's head to a yaw, as a move packet does.
func faceWarden(r *rig, yaw float32) {
	r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 90, Position: arena, Rotation: mgl32.Vec3{0, yaw, yaw}})
}

func TestRangerKeepsItsBandOffTheBoss(t *testing.T) {
	for _, tc := range []struct {
		name string
		from float32
	}{
		{name: "backs off from inside the band", from: 12},
		{name: "advances from beyond the band", from: 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newBossWarrior(t, "ranger", arena.Sub(mgl32.Vec3{tc.from, 0, 0}))
			for i := 0; i < 160; i++ {
				if !r.step() {
					t.Fatalf("tick %d: the Ranger dropped the warden", i)
				}
			}
			if d := dist(r.feet(), arena); d < 21 || d > 28 {
				t.Fatalf("the Ranger settled %.1f blocks off the warden, want 21-28", d)
			}
			if n := len(r.out.attacks()); n != 0 {
				t.Fatalf("%d melee attacks from the Ranger", n)
			}
		})
	}
}

func TestRangerShootsTheBossOnlyWithALine(t *testing.T) {
	r, _ := newBossWarrior(t, "ranger", arena.Sub(mgl32.Vec3{24, 0, 0}))
	r.run(130)
	if n := r.out.releases(2); n < 3 {
		t.Fatalf("%d arrows at the warden in 130 ticks with a clear line, want the bow cadence", n)
	}

	r, _ = newBossWarrior(t, "ranger", arena.Sub(mgl32.Vec3{24, 0, 0}))
	for z := int32(30); z <= 50; z++ {
		for y := int32(1); y <= 6; y++ {
			r.w.over[protocol.BlockPos{30, y, z}] = "stone"
		}
	}
	r.run(130)
	if n := r.out.uses(2); n != 0 {
		t.Fatalf("%d bow draws with a wall between the Ranger and the warden", n)
	}
	if d := dist(r.feet(), arena); d < 21 || d > 28 {
		t.Errorf("the Ranger left its band (%.1f blocks) behind the wall", d)
	}
}

func TestRangerStrafesWhenTheBossFacesIt(t *testing.T) {
	from := arena.Sub(mgl32.Vec3{24, 0, 0})
	r, _ := newBossWarrior(t, "ranger", from)
	r.run(10)
	z := r.feet().Z()
	r.run(30)
	if dz := math.Abs(float64(r.feet().Z() - z)); dz > 0.5 {
		t.Fatalf("the Ranger moved %.1f sideways with the warden looking away", dz)
	}

	faceWarden(r, 90) // towards -x, at the Ranger
	z = r.feet().Z()
	r.run(30)
	if dz := math.Abs(float64(r.feet().Z() - z)); dz < 2 {
		t.Fatalf("the Ranger stepped %.1f sideways with the warden facing it, want a strafe", dz)
	}
	if d := dist(r.feet(), arena); d < 21 || d > 28 {
		t.Errorf("the strafe left the band at %.1f blocks", d)
	}
}

func TestOnlyTheVanguardMeleesTheBoss(t *testing.T) {
	for _, tc := range []struct {
		role  string
		melee bool
	}{
		{role: "vanguard", melee: true},
		{role: "striker"},
		{role: "ranger"},
	} {
		t.Run(tc.role, func(t *testing.T) {
			r, squad := newBossWarrior(t, tc.role, arena.Sub(mgl32.Vec3{2, 0, 0}))
			// Nobody else stands at the warden, so the Striker has no
			// cover to join under.
			delete(squad.members, RoleVanguard)
			squad.leaderOK = false
			r.run(100)
			n := len(r.out.attacks())
			if tc.melee && n < 3 {
				t.Fatalf("%d attacks from the Vanguard in 100 ticks", n)
			}
			if !tc.melee && n != 0 {
				t.Fatalf("%d attacks on the warden from the %s", n, tc.role)
			}
			if !tc.melee {
				if d := dist(r.feet(), arena); d < 8 {
					t.Errorf("the %s stayed %.1f blocks off the warden", tc.role, d)
				}
			}
		})
	}
}

func TestStrikerJoinsOnlyBehindTheVanguardWithHealthToSpare(t *testing.T) {
	for _, tc := range []struct {
		name    string
		health  float32
		vanAt   float32
		melee   bool
		atLeast float64
	}{
		{name: "vanguard on the boss and healthy", health: 20, vanAt: 2, melee: true},
		{name: "vanguard too far off", health: 20, vanAt: 10, atLeast: 8},
		{name: "hurt below the commit line waits out of the boom", health: 13, vanAt: 2, atLeast: wardenBoomRange + 1},
		{name: "above the fall line but below the commit line", health: 12, vanAt: 2, atLeast: wardenBoomRange + 1},
		{name: "half health falls back past the darkness", health: 9, vanAt: 2, atLeast: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, squad := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{10, 0, 0}))
			r.give(3, 340, 0) // nothing to eat
			r.setHealth(tc.health)
			van := arena.Add(mgl32.Vec3{0, 0, -tc.vanAt})
			squad.members[RoleVanguard], squad.leader = van, van
			r.run(220)
			n := len(r.out.attacks())
			if tc.melee && n == 0 {
				t.Fatal("the Striker never joined the melee")
			}
			if !tc.melee && n != 0 {
				t.Fatalf("%d Striker attacks it was not cleared for", n)
			}
			if d := dist(r.feet(), arena); !tc.melee && d < tc.atLeast {
				t.Errorf("the Striker stands %.1f blocks off, want at least %.0f", d, tc.atLeast)
			}
			if d := dist(r.feet(), arena); !tc.melee && tc.atLeast == 8 && d > 12.5 {
				t.Errorf("the waiting Striker drifted to %.1f blocks, want its 8-12 band", d)
			}
		})
	}
}

func TestVanguardRaisesItsShieldBeforeTheBossSwings(t *testing.T) {
	r, _ := newBossWarrior(t, "vanguard", arena.Sub(mgl32.Vec3{2, 0, 0}))
	for i := 0; i < 40 && (r.l.warrior.shield || r.ticks < 5); i++ {
		r.step()
	}
	if r.l.warrior.shield {
		t.Fatal("the shield never came down to swing")
	}
	r.hurt() // the warden's first blow lands mid-swing
	r.step()
	hitAt := r.l.tick
	every := wardenProfile.meleeEvery
	due := hitAt + every
	for r.l.tick < due {
		before := len(r.out.attacks())
		r.step()
		if r.l.tick > due-guardLead {
			if !r.l.warrior.shield {
				t.Fatalf("tick %d of %d: the shield is down with the warden's swing due", r.l.tick-hitAt, every)
			}
			if len(r.out.attacks()) > before {
				t.Fatalf("tick %d of %d: swung into the warden's due blow", r.l.tick-hitAt, every)
			}
		}
	}

	// The due blow hurt nothing, so the shield took it and the warden
	// disabled it: swing instead of hiding behind it.
	r.run(guardTail + 1)
	if r.l.bossGuard(r.tracker.Snapshot()) {
		t.Fatal("guarding with a shield the warden just disabled")
	}
	attacks := len(r.out.attacks())
	r.run(60)
	if n := len(r.out.attacks()) - attacks; n < 4 {
		t.Errorf("%d swings with the shield disabled, want the cadence", n)
	}
}

func TestABlowThroughTheRaisedShieldStopsTheGuard(t *testing.T) {
	r, _ := newBossWarrior(t, "vanguard", arena.Sub(mgl32.Vec3{2, 0, 0}))
	for i := 0; i < 40 && !r.l.warrior.shield; i++ {
		r.step()
	}
	r.hurt()
	r.step()
	for i := 0; i < int(wardenProfile.meleeEvery); i++ {
		r.step()
		if r.l.bossGuard(r.tracker.Snapshot()) {
			t.Fatalf("tick %d: guarding after a blow came through the shield", i)
		}
	}
}

func TestWarriorsHealEarlyOnlyWithTheBossFarOff(t *testing.T) {
	for _, tc := range []struct {
		name string
		role string
		from float32
		eats bool
	}{
		{name: "striker with the boss in its band", role: "striker", from: 10, eats: true},
		{name: "ranger in its band", role: "ranger", from: 24, eats: true},
		{name: "striker inside the clearance backs off first", role: "striker", from: 6, eats: true},
		{name: "vanguard at the boss", role: "vanguard", from: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newBossWarrior(t, tc.role, arena.Sub(mgl32.Vec3{tc.from, 0, 0}))
			r.give(3, 363, 2) // golden apples
			r.setHealth(13)
			for i := 0; i < 40; i++ {
				before := r.out.uses(3)
				r.step()
				if r.out.uses(3) > before && dist(r.feet(), arena) <= wardenProfile.healClear {
					t.Fatalf("started a meal %.1f blocks from the warden", dist(r.feet(), arena))
				}
			}
			if ate := r.out.uses(3) > 0; ate != tc.eats {
				t.Fatalf("ate = %v at 13 health %.0f blocks off the warden, want %v", ate, tc.from, tc.eats)
			}
		})
	}
}

func TestMealIsLetGoWhenTheBossClosesIn(t *testing.T) {
	r, _ := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{11, 0, 0}))
	r.give(3, 363, 2)
	r.setHealth(13)
	r.run(10)
	if r.out.uses(3) != 1 {
		t.Fatal("the meal never started")
	}
	r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 90, Position: r.feet().Add(mgl32.Vec3{6, 0, 0})})
	r.run(3)
	if r.out.releases(3) == 0 {
		t.Fatal("kept eating with the warden 6 blocks off")
	}
}

func TestSurvivorsRegroupAtTheStartWhenTwoAreDown(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	for _, tc := range []struct {
		name    string
		down    int
		startOK bool
		regroup bool
	}{
		{name: "two down, start known", down: 2, startOK: true, regroup: true},
		{name: "one down fights on", down: 1, startOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, squad := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{10, 0, 0}))
			squad.down, squad.start, squad.startOK = tc.down, home, tc.startOK
			r.run(250)
			got := dist(r.feet(), home) <= regroupWithin
			if got != tc.regroup {
				t.Fatalf("at the start = %v (%.1f blocks off), want %v", got, dist(r.feet(), home), tc.regroup)
			}
			if tc.regroup && len(r.out.attacks()) != 0 {
				t.Error("traded blows on the way back")
			}
		})
	}
}

func TestRegroupFallsBackToWhereTheFightBegan(t *testing.T) {
	began := arena.Sub(mgl32.Vec3{10, 0, 0})
	r, squad := newBossWarrior(t, "ranger", began)
	r.run(80) // backs off to its band
	if dist(r.feet(), began) < 5 {
		t.Fatal("the Ranger never left where it began")
	}
	squad.down = 2
	r.run(200)
	if d := dist(r.feet(), began); d > regroupWithin {
		t.Fatalf("regrouped %.1f blocks from where the fight began", d)
	}
}

func TestALeaderlessRangerKeepsABossFocusedPastFocusRange(t *testing.T) {
	r := newWarrior(t, "ranger", arena.Sub(mgl32.Vec3{24, 0, 0}))
	r.w.size = 80
	r.spawn(90, "minecraft:warden", arena)
	if !r.step() || r.l.target.ID != 90 {
		t.Fatalf("target = %d, want the warden 24 blocks off", r.l.target.ID)
	}
	// A zombie as far off is no one's business.
	r = newWarrior(t, "ranger", arena.Sub(mgl32.Vec3{24, 0, 0}))
	r.w.size = 80
	spawnZombie(r, 50, arena)
	if r.step() {
		t.Fatal("engaged a zombie 24 blocks off")
	}
}

func TestNonBossFightsKeepTheirOldShape(t *testing.T) {
	r, squad := newBossWarrior(t, "striker", start)
	squad.leader, squad.members[RoleVanguard] = start, start
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 90})
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})
	r.run(40)
	if r.l.warrior.boss.active {
		t.Fatal("a zombie was fought as a boss")
	}
	if len(r.out.attacks()) == 0 {
		t.Fatal("the Striker would not melee a zombie")
	}

	r, squad = newBossWarrior(t, "ranger", start)
	squad.leader, squad.members[RoleVanguard] = start, start
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 90})
	squad.down = 2
	z := mgl32.Vec3{28.5, 1, 20.5}
	spawnZombie(r, 50, z)
	r.run(130)
	if d := dist(r.feet(), z); d > 12 {
		t.Errorf("the Ranger kept %.1f blocks off a zombie, the boss band", d)
	}
	if r.out.releases(2) < 3 {
		t.Errorf("%d arrows at a zombie, want the old cadence", r.out.releases(2))
	}
}

// pocket walls a Ranger in on three sides, open only towards the warden at
// +x: it cannot back off and cannot slide.
func pocket(r *rig, feet mgl32.Vec3) {
	x, z := int32(feet.X()), int32(feet.Z())
	for y := int32(1); y <= 2; y++ {
		for dz := int32(-1); dz <= 1; dz++ {
			r.w.over[protocol.BlockPos{x - 1, y, z + dz}] = "stone"
		}
		for dx := int32(0); dx <= 1; dx++ {
			r.w.over[protocol.BlockPos{x + dx, y, z - 1}] = "stone"
			r.w.over[protocol.BlockPos{x + dx, y, z + 1}] = "stone"
		}
	}
}

func TestACorneredRangerStillFightsTheBoss(t *testing.T) {
	t.Run("shoots from the corner", func(t *testing.T) {
		feet := arena.Sub(mgl32.Vec3{5, 0, 0})
		r, _ := newBossWarrior(t, "ranger", feet)
		pocket(r, feet)
		r.run(120)
		if n := r.out.releases(2); n == 0 {
			t.Fatal("a Ranger that could not back off never loosed an arrow")
		}
	})
	t.Run("swings with the boss at arm's length", func(t *testing.T) {
		feet := arena.Sub(mgl32.Vec3{2, 0, 0})
		r, _ := newBossWarrior(t, "ranger", feet)
		pocket(r, feet)
		r.run(60)
		if n := len(r.out.attacks()); n == 0 {
			t.Fatal("a cornered Ranger never swung at the warden in its face")
		}
	})
	t.Run("swings when the boss in reach goes for it", func(t *testing.T) {
		r, _ := newBossWarrior(t, "ranger", arena.Sub(mgl32.Vec3{2.5, 0, 0}))
		faceWarden(r, 90) // towards -x, at the Ranger
		for i := 0; i < 20; i++ {
			r.moveTo(90, r.feet().Add(mgl32.Vec3{2.5, 0, 0}))
			faceWarden(r, 90)
			r.step()
		}
		if n := len(r.out.attacks()); n == 0 {
			t.Fatal("the Ranger never swung at a warden on its heels")
		}
	})
}

func TestARegroupedUnitFightsABossThatFollowsIt(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	for _, tc := range []struct {
		role  string
		melee bool
	}{
		{role: "vanguard", melee: true},
		{role: "striker"},
	} {
		t.Run(tc.role, func(t *testing.T) {
			r, squad := newBossWarrior(t, tc.role, arena.Sub(mgl32.Vec3{10, 0, 0}))
			squad.down, squad.start, squad.startOK = 2, home, true
			r.run(200)
			if d := dist(r.feet(), home); d > regroupWithin {
				t.Fatalf("never regrouped: %.1f blocks from the start", d)
			}
			boss := r.feet().Add(mgl32.Vec3{2, 0, 0})
			r.moveTo(90, boss)
			r.run(80)
			n := len(r.out.attacks())
			if tc.melee && n == 0 {
				t.Fatal("the Vanguard never swung at the warden that followed it back")
			}
			if !tc.melee {
				if n != 0 {
					t.Fatalf("%d attacks from a regrouped Striker", n)
				}
				if d := dist(r.feet(), boss); d < meleeReach {
					t.Fatalf("the Striker stayed %.1f blocks off the warden that followed it", d)
				}
			}
		})
	}
}

func TestAUnitAtTheBossStillRegroupsWhenTwoGoDown(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	r, squad := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{6, 0, 0}))
	squad.start, squad.startOK = home, true
	r.run(30)
	if len(r.out.attacks()) == 0 {
		t.Fatal("the Striker never joined the melee")
	}
	squad.down = 2
	r.run(250)
	if d := dist(r.feet(), home); d > regroupWithin {
		t.Fatalf("stayed in the melee %.1f blocks from the start", d)
	}
}

func TestARegroupEndsAfterAWhile(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	r, squad := newBossWarrior(t, "vanguard", arena.Sub(mgl32.Vec3{10, 0, 0}))
	squad.down, squad.start, squad.startOK = 2, home, true
	r.run(380) // short of the regroup's 20 s
	if d := dist(r.feet(), home); d > regroupWithin || len(r.out.attacks()) != 0 {
		t.Fatalf("regroup broke early: %.1f blocks from the start, %d attacks", d, len(r.out.attacks()))
	}
	r.run(300)
	if len(r.out.attacks()) == 0 {
		t.Fatal("the Vanguard parked at the start for good")
	}
}

func TestAnUnreachableRegroupPointIsNotReplannedEveryTick(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	r, squad := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{10, 0, 0}))
	// A ring of wall too high to climb round the start.
	for x := int32(8); x <= 12; x++ {
		for z := int32(38); z <= 42; z++ {
			if x != 8 && x != 12 && z != 38 && z != 42 {
				continue
			}
			for y := int32(1); y <= 3; y++ {
				r.w.over[protocol.BlockPos{x, y, z}] = "stone"
			}
		}
	}
	squad.down, squad.start, squad.startOK = 2, home, true
	plans := 0
	for i := 0; i < 100; i++ {
		r.step()
		if r.l.warrior.boss.walkAt == r.l.tick {
			plans++
		}
	}
	if plans > 5 {
		t.Fatalf("planned the walk to an unreachable start %d times in 100 ticks", plans)
	}
}
