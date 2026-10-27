package life

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func TestHitReadyFollowsTheHitWindow(t *testing.T) {
	for _, tc := range []struct {
		now, last uint64
		want      bool
	}{
		{now: 100, last: 90, want: true},
		{now: 99, last: 90, want: false},
		{now: 91, last: 90, want: false},
		{now: 100, last: 100, want: false},
		{now: 500, last: 90, want: true},
	} {
		if got := hitReady(tc.now, tc.last); got != tc.want {
			t.Errorf("hitReady(%d, %d) = %v, want %v", tc.now, tc.last, got, tc.want)
		}
	}
}

func TestAnAxeBlowDisablesAShieldForFiveSeconds(t *testing.T) {
	if shieldDisabled(1000, 0) {
		t.Error("a blow that never landed disabled a shield")
	}
	if !shieldDisabled(1001, 1000) || !shieldDisabled(1099, 1000) {
		t.Error("the shield came back before five seconds were up")
	}
	if shieldDisabled(1100, 1000) {
		t.Error("the shield stayed disabled past five seconds")
	}
}

func TestMeleeWeaponOpensWithTheAxeAndSpendsTheWindowOnTheSword(t *testing.T) {
	for _, tc := range []struct {
		name         string
		now, axeHit  uint64
		haveAxe, shd bool
		want         weaponKind
	}{
		{"no blow yet", 200, 0, true, true, weaponAxe},
		{"inside the disable window", 250, 200, true, true, weaponSword},
		{"window run out", 300, 200, true, true, weaponAxe},
		{"no axe in the kit", 200, 0, false, true, weaponSword},
		{"opponent not assumed to shield", 200, 0, true, false, weaponSword},
	} {
		if got := meleeWeapon(tc.now, tc.axeHit, tc.haveAxe, tc.shd); got != tc.want {
			t.Errorf("%s: meleeWeapon = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestBowingHasHysteresis(t *testing.T) {
	const near = 5.0
	if bowing(near+pvpBowHysteresis-0.1, false, near) {
		t.Error("drew the bow from inside the hysteresis band")
	}
	if !bowing(near+pvpBowHysteresis+0.1, false, near) {
		t.Error("did not draw the bow from beyond the band")
	}
	if !bowing(near+0.1, true, near) {
		t.Error("put the bow away while the target was still beyond kite distance")
	}
	if bowing(near, true, near) {
		t.Error("kept the bow once the target was at kite distance")
	}
}

func TestStrafeBandStaysInsideReachAndKeepsWidth(t *testing.T) {
	for _, orbitMin := range []float64{1.2, 1.5, 2.2, 3} {
		lo, hi := strafeBand(orbitMin)
		if hi >= pvpReach {
			t.Errorf("orbitMin %.1f: ring edge %.2f is not inside reach %.2f", orbitMin, hi, pvpReach)
		}
		if hi-lo < 0.4-1e-9 {
			t.Errorf("orbitMin %.1f: ring [%.2f, %.2f] is narrower than 0.4", orbitMin, lo, hi)
		}
	}
}

func TestStrafeHeadingIsTangentInTheRingAndLeansInOrOutOutside(t *testing.T) {
	to := mgl32.Vec2{1, 0}
	lo, hi := 1.5, 2.6
	dot := func(a, b mgl32.Vec2) float64 { return float64(a.Dot(b)) }

	in := strafeHeading(to, 2.0, 1, lo, hi)
	if math.Abs(dot(in, to)) > 1e-5 {
		t.Errorf("in the ring the step %v is not tangent to %v", in, to)
	}
	if far := strafeHeading(to, 3.0, 1, lo, hi); dot(far, to) <= 0.5 {
		t.Errorf("beyond the ring the step %v does not lean towards the target", far)
	}
	if close := strafeHeading(to, 1.0, 1, lo, hi); dot(close, to) >= -0.5 {
		t.Errorf("inside the ring the step %v does not lean away from the target", close)
	}
	a, b := strafeHeading(to, 2.0, 1, lo, hi), strafeHeading(to, 2.0, -1, lo, hi)
	if math.Abs(dot(a, b)+1) > 1e-5 {
		t.Errorf("the two orbits %v and %v are not opposite", a, b)
	}
	for _, d := range []float64{1.0, 2.0, 3.0} {
		if n := strafeHeading(to, d, -1, lo, hi).Len(); math.Abs(float64(n)-1) > 1e-5 {
			t.Errorf("step at %.1f has length %.3f, want a unit vector", d, n)
		}
	}
}

func TestBlockNowRaisesTheShieldJustAheadOfEachPredictedBlow(t *testing.T) {
	const hurt = 200
	up := map[uint64]bool{}
	for now := uint64(hurt); now < hurt+pvpBlockMemory+5; now++ {
		if blockNow(now, hurt) {
			up[now] = true
		}
	}
	// Blows were at 200, so the next fall at 210, 220, 230; the shield is up
	// for the lead before each.
	for _, swing := range []uint64{210, 220, 230} {
		for lead := uint64(1); lead <= pvpBlockLead; lead++ {
			if !up[swing-lead] {
				t.Errorf("shield not up %d ticks before the predicted blow at %d", lead, swing)
			}
		}
		if up[swing-pvpBlockLead-1] {
			t.Errorf("shield up earlier than the lead before the blow at %d", swing)
		}
	}
	if blockNow(150, 0) {
		t.Error("blocked with no blow to predict from")
	}
	if blockNow(hurt+pvpBlockMemory+1, hurt) {
		t.Error("kept predicting from a blow older than the memory")
	}
	if blockNow(100, hurt) {
		t.Error("blocked on a blow in the future")
	}
}

func TestTapForwardPausesOnTheSwingAndStartsAgainAfter(t *testing.T) {
	if got := tapForward(100, 100); got != 0 {
		t.Errorf("forward on the swing tick = %v, want 0", got)
	}
	if got := tapForward(101, 100); got != 0 {
		t.Errorf("forward the tick after the swing = %v, want 0", got)
	}
	if got := tapForward(102, 100); got != 1 {
		t.Errorf("forward two ticks after the swing = %v, want 1", got)
	}
	if got := tapForward(5, 0); got != 1 {
		t.Errorf("forward before any swing = %v, want 1", got)
	}
}

func TestCanSprintNeedsKnownFoodAboveTheGamesFloor(t *testing.T) {
	for _, c := range []struct {
		hunger float32
		known  bool
		want   bool
	}{{20, true, true}, {7, true, true}, {6, true, false}, {0, true, false}, {20, false, false}} {
		if got := canSprint(belief.Snapshot{Hunger: c.hunger, HungerKnown: c.known}); got != c.want {
			t.Errorf("canSprint(hunger %v, known %v) = %v, want %v", c.hunger, c.known, got, c.want)
		}
	}
}

func TestEatClearScalesTheEnginesEatingDistance(t *testing.T) {
	if got := eatClear(3); got != 3*pvpEatClearFactor || got <= 3 {
		t.Errorf("eatClear(3) = %v, want beyond the engine's own 3", got)
	}
}

func TestFallingCritNeedsAFallNotAJumpOrStanding(t *testing.T) {
	r := newWarrior(t, "striker", start)
	if fallingCrit(r.a.Pose()) {
		t.Fatal("a unit standing on the ground counts as falling")
	}
	var rising, falling bool
	if err := r.a.Tick(act.Input{Jump: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40 && !r.a.Pose().OnGround || i == 0; i++ {
		p := r.a.Pose()
		switch {
		case p.Rise() > 0:
			rising = true
			if fallingCrit(p) {
				t.Fatal("a rising unit counts as falling")
			}
		case p.Rise() < 0:
			falling = true
			if !fallingCrit(p) {
				t.Fatal("a falling unit does not count as a critical hit")
			}
		}
		if err := r.a.Tick(act.Input{}); err != nil {
			t.Fatal(err)
		}
	}
	if !rising || !falling {
		t.Fatalf("the jump never rose (%v) and fell (%v)", rising, falling)
	}
}
