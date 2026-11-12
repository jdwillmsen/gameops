package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// baselineRig is a unit fighting by the combat baseline, in daylight.
func baselineRig(t *testing.T, feet mgl32.Vec3, mod func(*CombatBaseline)) *rig {
	t.Helper()
	cb := DefaultCombatBaseline()
	if mod != nil {
		mod(&cb)
	}
	cfg := defaults
	cfg.Combat = &cb
	r := newRig(t, cfg, feet)
	r.tracker.ItemNames(gearTable)
	r.tracker.SetClock(6000, true, 0)
	return r
}

func (r *rig) night() { r.tracker.SetClock(14000, true, 0) }

// geared arms the unit with an iron sword in slot 0 and a full set of iron.
func (r *rig) geared() {
	r.give(0, 301, 1)
	r.wear(idHelmet, idChest, idLegs, idBoots)
}

func (r *rig) baseline() *baselineState { return baselineUnits.of(r.l) }

// cornerUnit surrounds the unit's own cell with lava, so no retreat has a goal.
func (r *rig) cornerUnit() {
	for x := int32(0); x < 40; x++ {
		for z := int32(0); z < 40; z++ {
			if x != 20 || z != 20 {
				r.w.over[protocol.BlockPos{x, 0, z}] = "lava"
			}
		}
	}
}

func TestBaselineNeverStrikesALitCreeperEvenCornered(t *testing.T) {
	for _, tc := range []struct {
		name string
		lit  func(*rig)
	}{
		{"fuse flag", func(r *rig) { r.setActorFlags(80, protocol.EntityDataFlagIgnited) }},
		{"charged flag", func(r *rig) { r.setActorFlags(80, protocol.EntityDataFlagPowered) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := baselineRig(t, start, nil)
			r.geared()
			r.cornerUnit()
			r.spawn(80, "minecraft:creeper", mgl32.Vec3{21.5, 1, 20.5})
			tc.lit(r)
			r.run(60)
			if n := len(r.out.attacks()); n != 0 {
				t.Errorf("%d blows at a creeper that was lit or charged", n)
			}
			if st := r.baseline().stance; st != stanceHold {
				t.Errorf("cornered stance %v, want hold", st)
			}
		})
	}
}

func TestBaselineLitCreeperIsBackedAwayFromNotMeleed(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	c := mgl32.Vec3{22.5, 1, 20.5}
	r.spawn(80, "minecraft:creeper", c)
	r.setActorFlags(80, protocol.EntityDataFlagIgnited)
	d0 := dist(r.feet(), c)
	for i := 0; i < 30; i++ {
		r.step()
		c = c.Add(mgl32.Vec3{-0.02, 0, 0})
		r.moveTo(80, c)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d blows at a lit creeper", n)
	}
	if d := dist(r.feet(), c); d <= d0 {
		t.Errorf("distance to the lit creeper %.1f -> %.1f, want ground gained", d0, d)
	}
}

func TestBaselineCorneredUnitHitsAnUnlitCreeperOnceThenHoldsOff(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.cornerUnit()
	c := mgl32.Vec3{21.5, 1, 20.5}
	r.spawn(80, "minecraft:creeper", c)
	for i := 0; i < 40; i++ {
		r.step()
		// A creeper that keeps shifting is not taken for a lit one by its
		// stillness; the blow itself is what marks the fuse.
		c = c.Add(mgl32.Vec3{0.05 * float32(1-2*(i%2)), 0, 0})
		r.moveTo(80, c)
	}
	if n := len(r.out.attacks()); n != 1 {
		t.Errorf("%d blows at the creeper, want exactly the one before its fuse is believed lit", n)
	}
}

func TestBaselineCorneredAtLowHealthDoesNotHitACreeper(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.cornerUnit()
	r.setHealth(4)
	c := mgl32.Vec3{21.5, 1, 20.5}
	r.spawn(80, "minecraft:creeper", c)
	for i := 0; i < 40; i++ {
		r.step()
		c = c.Add(mgl32.Vec3{0.05 * float32(1-2*(i%2)), 0, 0})
		r.moveTo(80, c)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d blows from a fleeing unit at a creeper", n)
	}
}

func TestBaselineChargedCreeperIsWatchedFromFarther(t *testing.T) {
	far := mgl32.Vec3{29.5, 1, 20.5} // 9 blocks, past the plain creeper range
	plain := newRig(t, defaults, start)
	plain.spawn(80, "minecraft:creeper", far)
	plain.setActorFlags(80, protocol.EntityDataFlagPowered)
	if plain.step() {
		t.Error("the reactive defence engaged a charged creeper beyond its range")
	}

	r := baselineRig(t, start, nil)
	r.spawn(80, "minecraft:creeper", far)
	r.setActorFlags(80, protocol.EntityDataFlagPowered)
	if !r.step() {
		t.Fatal("the baseline ignored a charged creeper whose blast reaches 12 blocks")
	}
	if r.l.mode != modeRetreat {
		t.Errorf("mode %v, want retreat", r.l.mode)
	}
}

func TestBaselineAFusedCreeperIsTheTargetBeforeANearerZombie(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22, 1, 20.5})
	r.spawn(80, "minecraft:creeper", mgl32.Vec3{24.5, 1, 20.5})
	r.setActorFlags(80, protocol.EntityDataFlagIgnited)
	r.step()
	if r.l.target.ID != 80 {
		t.Errorf("target %d, want the lit creeper 80", r.l.target.ID)
	}
}

func TestBaselineUnderGearedUnitGivesGroundAtNightButNotByDay(t *testing.T) {
	approach := func(night bool, gear func(*rig)) (attacks int, moved float64) {
		r := baselineRig(t, start, nil)
		if night {
			r.night()
		}
		gear(r)
		z := mgl32.Vec3{25, 1, 20.5}
		r.spawn(50, "minecraft:zombie", z)
		for i := 0; i < 40; i++ {
			r.step()
			// The zombie closes slowly, as a trailing one does.
			z = z.Add(mgl32.Vec3{-0.04, 0, 0})
			r.moveTo(50, z)
		}
		return len(r.out.attacks()), dist(r.feet(), start)
	}
	bare := func(r *rig) { r.give(0, 312, 1) } // a pickaxe, no armour

	if a, moved := approach(true, bare); a != 0 || moved < 2 {
		t.Errorf("night, bare: %d attacks and %.1f blocks moved, want a retreat", a, moved)
	}
	if a, _ := approach(false, bare); a == 0 {
		t.Error("day, bare: the unit never fought a zombie that came to it")
	}
	if a, _ := approach(true, (*rig).geared); a == 0 {
		t.Error("night, geared: the unit never fought")
	}
}

func TestBaselineUnderGearedNightRetreatEndsInAFightWhenCornered(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.night()
	r.give(0, 312, 1)
	r.cornerUnit()
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{23, 1, 20.5})
	r.run(60)
	if len(r.out.attacks()) == 0 {
		t.Error("a cornered under-geared unit never fought back")
	}
}

func TestBaselineFleeLatchHoldsUntilHealthIsRegained(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})

	step := func(health float32) mode {
		t.Helper()
		r.setHealth(health)
		// Keep the zombie three blocks off, as a pursuer is.
		r.moveTo(50, r.feet().Add(mgl32.Vec3{3, 0, 0}))
		r.step()
		return r.l.mode
	}
	if m := step(5); m != modeRetreat {
		t.Fatalf("mode %v at 5 health, want retreat", m)
	}
	if m := step(9); m != modeRetreat {
		t.Errorf("mode %v at 9 health after fleeing, want the retreat kept", m)
	}
	if m := step(11); m != modeRetreat {
		t.Errorf("mode %v at 11 health, want the retreat kept", m)
	}
	if m := step(13); m != modeFight {
		t.Errorf("mode %v at 13 health, want the fight resumed", m)
	}
	var fled, resumed int
	for _, e := range r.log.lines {
		switch e.event {
		case "combat_flee":
			fled++
		case "combat_resume":
			resumed++
		}
	}
	if fled != 1 || resumed != 1 {
		t.Errorf("logged %d flee and %d resume events, want one each", fled, resumed)
	}
}

// A fleeing unit with a mob already within a blow cannot outrun it, so it
// fights instead of taking hits in the back.
func TestBaselineFleeingUnitFightsAMobInReach(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.setHealth(5)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21.5, 1, 20.5})
	r.run(30)
	if !r.baseline().fleeing {
		t.Fatal("the latch did not trip")
	}
	r.setHealth(9)
	r.run(30)
	if r.l.mode != modeFight {
		t.Errorf("mode %v with the mob in reach, want fight", r.l.mode)
	}
	if len(r.out.attacks()) == 0 {
		t.Error("a fleeing unit with a zombie beside it never struck back")
	}
}

// RETREAT_HEALTH raises the baseline's flee bar when it is the higher of the
// two, and COMBAT_FLEE_HEALTH keeps it when that is.
func TestBaselineFleeBarIsTheHigherOfFleeAndRetreatHealth(t *testing.T) {
	for _, tc := range []struct {
		name          string
		retreat, flee int
		health        float32
		want          mode
	}{
		{"retreat bar higher: retreats at 7", 8, 6, 7, modeRetreat},
		{"retreat bar higher: fights at 9", 8, 6, 9, modeFight},
		{"flee bar higher: retreats at 7", 4, 8, 7, modeRetreat},
		{"both at default: fights at 7", 6, 6, 7, modeFight},
		{"retreat unset keeps the flee bar", 0, 6, 7, modeFight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb := DefaultCombatBaseline()
			cb.FleeHealth = float32(tc.flee)
			cb.NightFleeHealth = max(cb.NightFleeHealth, cb.FleeHealth)
			cfg := defaults
			cfg.Combat = &cb
			cfg.Vitals.RetreatHealth = tc.retreat
			r := newRig(t, cfg, start)
			r.tracker.ItemNames(gearTable)
			r.tracker.SetClock(6000, true, 0)
			r.geared()
			r.setHealth(tc.health)
			r.spawn(50, "minecraft:zombie", mgl32.Vec3{23.5, 1, 20.5})
			r.step()
			if r.l.mode != tc.want {
				t.Errorf("mode %v, want %v", r.l.mode, tc.want)
			}
		})
	}
}

// A unit that cannot regenerate does not hold the latch for good: past the
// hold it fights again at a health over the flee bar, and a further hit
// trips it once more.
func TestBaselineLatchIsLetGoOfAfterTheHoldWithoutRegeneration(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.setHealth(5)
	r.step()
	if !r.baseline().fleeing {
		t.Fatal("the latch did not trip")
	}
	r.setHealth(9)
	r.step()
	if !r.baseline().fleeing {
		t.Fatal("the latch released before the hold ran out")
	}
	r.l.tick += fleeHoldTicks + 1
	r.step()
	if r.baseline().fleeing {
		t.Error("the latch outlived the hold at a health over the flee bar")
	}
	r.setHealth(5)
	r.step()
	if !r.baseline().fleeing {
		t.Error("the latch did not trip again at the flee bar")
	}
	// Under the flee bar the hold does not release it.
	r.l.tick += fleeHoldTicks + 1
	r.step()
	if !r.baseline().fleeing {
		t.Error("the latch let go of a unit still under the flee bar")
	}
}

func TestBaselineNightRaisesTheFleeBarOfAnUnderGearedUnit(t *testing.T) {
	// The mob starts out of reach: a fleeing unit fights one that is in it.
	mode7 := func(night, geared bool) mode {
		r := baselineRig(t, start, nil)
		if night {
			r.night()
		}
		if geared {
			r.geared()
		} else {
			r.give(0, 312, 1)
		}
		r.setHealth(8)
		r.spawn(50, "minecraft:zombie", mgl32.Vec3{24.5, 1, 20.5})
		r.step()
		return r.l.mode
	}
	if m := mode7(true, false); m != modeRetreat {
		t.Errorf("night under-geared at 8 health: %v, want retreat", m)
	}
	if m := mode7(false, false); m != modeFight {
		t.Errorf("day under-geared at 8 health: %v, want fight", m)
	}
	if m := mode7(true, true); m != modeFight {
		t.Errorf("night geared at 8 health: %v, want fight", m)
	}
}

func TestBaselineDeathResetsTheLatch(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.geared()
	r.setHealth(3)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.step()
	if !r.baseline().fleeing {
		t.Fatal("the latch did not trip")
	}
	r.l.Reset()
	if r.baseline().fleeing {
		t.Error("the latch survived Reset")
	}
}

func TestBaselineIsOffForAWarriorAndWhenNotConfigured(t *testing.T) {
	cb := DefaultCombatBaseline()
	cfg := defaults
	cfg.Role = "striker"
	cfg.Combat = &cb
	if w := newRig(t, cfg, start); w.l.baselineOn() {
		t.Error("the baseline is on for a warrior")
	}
	if r := newRig(t, defaults, start); r.l.baselineOn() {
		t.Error("the baseline is on with no config")
	}
}

// archerRig is a unit with a sword and a shield on the hotbar facing a
// skeleton ten blocks east that keeps its head on the unit.
func archerRig(t *testing.T) (*rig, mgl32.Vec3) {
	t.Helper()
	r := baselineRig(t, start, nil)
	r.give(0, 301, 1)
	r.give(1, 360, 1)
	archer := mgl32.Vec3{30.5, 1, 20.5}
	r.spawnArcher(50, "minecraft:skeleton", archer)
	return r, archer
}

func TestBaselineClosesOnAnArcherAlongAZigzag(t *testing.T) {
	r, archer := archerRig(t)
	d0 := dist(r.feet(), archer)
	var minZ, maxZ float32 = start.Z(), start.Z()
	for i := 0; i < 60; i++ {
		if i%40 == 0 {
			r.hurt() // an archer past the threat range is a target once it has shot
		}
		r.keepAimed(50, archer)
		r.step()
		z := r.feet().Z()
		minZ, maxZ = min(minZ, z), max(maxZ, z)
	}
	if d := dist(r.feet(), archer); d >= d0-2 {
		t.Errorf("distance to the archer %.1f -> %.1f, want it closed", d0, d)
	}
	if maxZ-minZ < 0.5 {
		t.Errorf("sideways travel %.2f blocks, want a weave and not a straight line", maxZ-minZ)
	}
}

func TestBaselineRaisesTheShieldAgainstAClosingArrowAndLowersItAfter(t *testing.T) {
	r, archer := archerRig(t)
	r.hurt()
	r.run(3)
	arrow := mgl32.Vec3{25.5, 1.5, 20.5}
	r.spawn(70, "minecraft:arrow", arrow)
	for i := 0; i < 10; i++ {
		arrow = arrow.Sub(mgl32.Vec3{0.35, 0, 0})
		r.moveTo(70, arrow)
		r.keepAimed(50, archer)
		r.step()
	}
	if r.out.uses(1) == 0 {
		t.Fatal("an arrow closing on the unit never raised the shield")
	}
	if r.out.startUsingInputs() == 0 {
		t.Error("the raise was not begun as an item use")
	}

	// The archer dies; the fight's grace runs out with the shield let down.
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 70})
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.run(graceTicks + 5)
	if r.out.releases(1) == 0 {
		t.Error("the shield stayed up after the fight")
	}
}

func TestBaselineWithoutAShieldNeverUsesAnItem(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.give(0, 301, 1)
	archer := mgl32.Vec3{30.5, 1, 20.5}
	r.spawnArcher(50, "minecraft:skeleton", archer)
	arrow := mgl32.Vec3{25.5, 1.5, 20.5}
	r.spawn(70, "minecraft:arrow", arrow)
	for i := 0; i < 10; i++ {
		arrow = arrow.Sub(mgl32.Vec3{0.35, 0, 0})
		r.moveTo(70, arrow)
		r.keepAimed(50, archer)
		r.step()
	}
	if r.out.startUsingInputs() != 0 {
		t.Error("the unit started an item use with no shield")
	}
}

func TestBaselineShieldCanBeSwitchedOff(t *testing.T) {
	r := baselineRig(t, start, func(c *CombatBaseline) { c.Shield = false })
	r.give(0, 301, 1)
	r.give(1, 360, 1)
	archer := mgl32.Vec3{30.5, 1, 20.5}
	r.spawnArcher(50, "minecraft:skeleton", archer)
	arrow := mgl32.Vec3{25.5, 1.5, 20.5}
	r.spawn(70, "minecraft:arrow", arrow)
	for i := 0; i < 10; i++ {
		arrow = arrow.Sub(mgl32.Vec3{0.35, 0, 0})
		r.moveTo(70, arrow)
		r.keepAimed(50, archer)
		r.step()
	}
	if r.out.uses(1) != 0 {
		t.Error("the shield was raised with COMBAT_SHIELD off")
	}
}

func TestBaselineMeleesAnArcherInReachWithTheShieldDown(t *testing.T) {
	r, _ := archerRig(t)
	archer := mgl32.Vec3{22, 1, 20.5}
	r.moveTo(50, archer)
	r.run(20)
	if len(r.out.attacks()) == 0 {
		t.Error("an archer in reach was never struck")
	}
}

func TestBaselineHurtUnitBreaksAnArchersLineOfSight(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.give(0, 301, 1)
	r.setHealth(4)
	for z := int32(18); z <= 22; z++ {
		for y := int32(1); y <= 2; y++ {
			r.w.over[protocol.BlockPos{17, y, z}] = "stone"
		}
	}
	archer := mgl32.Vec3{30.5, 1, 20.5}
	r.spawnArcher(50, "minecraft:skeleton", archer)
	r.keepAimed(50, archer)
	r.hurt()
	r.step()
	if st := r.baseline().stance; st != stanceCover {
		t.Fatalf("stance %v, want cover", st)
	}
	for i := 0; i < 120; i++ {
		if i%50 == 0 {
			r.hurt()
		}
		r.keepAimed(50, archer)
		r.step()
	}
	if r.l.bowLine(act.Pose{Feet: archer}, belief.Entity{Pos: r.feet()}) {
		t.Errorf("the archer still has a line on the unit at %v", r.feet())
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d blows from a fleeing unit", n)
	}
}

func TestBaselineWitherSkeletonIsFoughtAsAMeleeMob(t *testing.T) {
	r := baselineRig(t, start, nil)
	r.give(0, 301, 1)
	r.give(1, 360, 1)
	r.spawnArcher(50, "minecraft:wither_skeleton", mgl32.Vec3{25, 1, 20.5})
	r.step()
	if st := r.baseline().in.Threat; st != threatMelee {
		t.Errorf("threat %v, want melee", st)
	}
}
