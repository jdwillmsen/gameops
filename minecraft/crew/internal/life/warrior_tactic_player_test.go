package life

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Item ids from the shared test item table.
const (
	idIronAxe      = 302
	idDiamondSword = 303
	idShield       = 360
	idBow          = 361
	idArrow        = 362
	idGoldenApple  = 363
)

const pvpFoe = uint64(60)

// pvpRig is a warrior fighting a fake player. The engine never targets a
// player by itself, so the rig hands the fight its target the way a
// targeting policy would, and steps the fight one tick at a time.
type pvpRig struct {
	*rig
}

func usingPlayerTactic(name string) tactics.Profile {
	pr := tactics.NewProfile("pvp " + name)
	pr.Tactics = map[tactics.Family]string{tactics.FamilyPlayer: name}
	return pr
}

func newPvpRig(t *testing.T, role, tactic string, foe mgl32.Vec3) *pvpRig {
	t.Helper()
	return newPvpRigAt(t, role, tactic, start, foe)
}

func newPvpRigAt(t *testing.T, role, tactic string, unit, foe mgl32.Vec3) *pvpRig {
	t.Helper()
	r := newWarrior(t, role, unit)
	if tactic != "" {
		if err := r.l.SetProfile(usingPlayerTactic(tactic)); err != nil {
			t.Fatal(err)
		}
	}
	r.player(pvpFoe, foe)
	// A fight begins with a swing ready, as Engaged leaves it.
	r.l.tick = 100
	r.l.lastAttack = r.l.tick - attackEvery
	return &pvpRig{r}
}

// tick runs one fight tick against the player and checks the one-input rule.
func (r *pvpRig) tick() {
	r.t.Helper()
	r.l.tick++
	e, ok := r.l.ents.Get(pvpFoe)
	if !ok {
		r.t.Fatal("the player is gone")
	}
	r.l.target = e
	before := len(r.out.inputs())
	if err := r.l.Fight(context.Background(), r.a, r.tracker.Snapshot()); err != nil {
		r.t.Fatal(err)
	}
	if n := len(r.out.inputs()) - before; n != 1 {
		r.t.Fatalf("tick %d sent %d inputs, want exactly 1", r.ticks, n)
	}
	r.ticks++
}

func (r *pvpRig) runTicks(n int) {
	r.t.Helper()
	for i := 0; i < n; i++ {
		r.tick()
	}
}

func (r *pvpRig) foe() mgl32.Vec3 {
	e, _ := r.l.ents.Get(pvpFoe)
	return e.Pos
}

func (r *pvpRig) gap() float64 { return horizontal(r.feet(), r.foe()) }

// heldInAttacks lists the item network id each attack on the player quoted.
func (r *pvpRig) heldInAttacks() []int32 {
	var out []int32
	for _, d := range r.out.attacks() {
		out = append(out, d.HeldItem.Stack.NetworkID)
	}
	return out
}

func kitSword(r *pvpRig) { r.give(0, idDiamondSword, 1) }

func TestPlayerTacticsAreRegisteredForThePlayerFamily(t *testing.T) {
	got := TacticNames(tactics.FamilyPlayer)
	want := []string{"player/axe-shield", "player/strafe"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TacticNames(player) = %v, want %v", got, want)
	}
}

func TestStrafeFightsAPlayerInReachRoundTheRing(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.runTicks(100)

	if len(r.out.attacks()) < 4 {
		t.Errorf("%d attacks in 100 ticks against a player in reach, want a steady swing", len(r.out.attacks()))
	}
	for _, id := range r.heldInAttacks() {
		if id != idDiamondSword {
			t.Errorf("attack quoted item %d, want the sword", id)
		}
	}
	// The foe never moved, so strafing is the unit travelling round it.
	foe := r.foe()
	swept := math.Atan2(float64(r.feet().Z()-foe.Z()), float64(r.feet().X()-foe.X()))
	start0 := math.Atan2(float64(start.Z()-foe.Z()), float64(start.X()-foe.X()))
	if d := math.Abs(math.Remainder(swept-start0, 2*math.Pi)); d < math.Pi/6 {
		t.Errorf("the unit swept only %.2f rad round the player in 100 ticks", d)
	}
	if g := r.gap(); g > pvpReach+0.5 {
		t.Errorf("the unit ended %.2f blocks from the player, outside reach", g)
	}
}

func TestStrafeHoldsTheRingAgainstAPlayerWhoBacksOff(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	for i := 0; i < 80; i++ {
		foe := r.foe()
		r.moveTo(pvpFoe, foe.Add(mgl32.Vec3{0.06, 0, 0}))
		r.tick()
	}
	if g := r.gap(); g > pvpReach+1.5 {
		t.Errorf("the unit fell %.2f blocks behind a player backing off slowly", g)
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the unit never swung while keeping up")
	}
}

func TestStrafeSwingsFromTheFallNotTheGround(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	var airborneSwings, groundSwings int
	for i := 0; i < 80; i++ {
		before := len(r.out.attacks())
		pre := r.a.Pose()
		r.tick()
		if len(r.out.attacks()) > before {
			if fallingCrit(pre) {
				airborneSwings++
			} else {
				groundSwings++
			}
		}
	}
	if airborneSwings == 0 {
		t.Fatalf("no swing came from a fall (%d from the ground)", groundSwings)
	}
	if airborneSwings < groundSwings {
		t.Errorf("%d swings from a fall, %d from the ground: the crit jump is not what carries the fight", airborneSwings, groundSwings)
	}
}

func TestStrafeNeverSwingsFasterThanTheHitWindow(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	var swingAt []int
	for i := 0; i < 120; i++ {
		before := len(r.out.attacks())
		r.tick()
		if len(r.out.attacks()) > before {
			swingAt = append(swingAt, i)
		}
	}
	for i := 1; i < len(swingAt); i++ {
		if gap := swingAt[i] - swingAt[i-1]; gap < pvpHitGap {
			t.Errorf("swings %d ticks apart, inside the %d-tick hit window", gap, pvpHitGap)
		}
	}
}

func TestStrafeBlocksOnTheOpponentsRhythm(t *testing.T) {
	r := newPvpRig(t, "vanguard", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.give(1, idShield, 1)
	// The foe has just hit the unit, as noteHurt would record.
	r.l.attacker, r.l.attackerPlayer, r.l.attackerUntil = pvpFoe, true, r.l.tick+attackerTicks
	hurtAt := r.l.attackerUntil - attackerTicks

	upAt, swingDue := map[uint64]bool{}, map[uint64]bool{}
	for i := 0; i < pvpBlockMemory; i++ {
		// The unit's own swing outranks the shield, so a tick that owes one
		// is not a miss.
		swingDue[r.l.tick+1] = hitReady(r.l.tick+1, r.l.lastAttack)
		r.tick()
		if r.l.warrior.shield {
			upAt[r.l.tick] = true
		}
	}
	var predicted, covered int
	for now := hurtAt + 1; now < hurtAt+pvpBlockMemory; now++ {
		if blockNow(now, hurtAt) && !swingDue[now] {
			predicted++
			if upAt[now] {
				covered++
			}
		}
	}
	if predicted == 0 || covered != predicted {
		t.Errorf("shield up on %d of %d predicted-blow ticks the unit was free for, want all", covered, predicted)
	}
	for now := range upAt {
		if now > hurtAt+pvpBlockMemory+pvpHitGap {
			t.Errorf("shield still up at %d, long after the opponent's rhythm went stale", now)
		}
	}
	if r.out.uses(1) == 0 {
		t.Error("the shield was never raised")
	}
}

func TestStrafeNeverRaisesTheShieldWithNoBlowToPredictFrom(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.give(1, idShield, 1)
	r.runTicks(60)
	if n := r.out.uses(1); n != 0 {
		t.Errorf("shield raised %d times against a player who never landed a blow", n)
	}
}

func TestStrafeSwitchesBowAtRangeAndSwordUpClose(t *testing.T) {
	r := newPvpRig(t, "ranger", "player/strafe", mgl32.Vec3{34.5, 1, 20.5})
	kitSword(r)
	r.give(1, idBow, 1)
	r.give(2, idArrow, 16)
	r.runTicks(40)
	if r.out.uses(1) == 0 || r.out.releases(1) == 0 {
		t.Fatalf("no arrow drawn and released at range (uses %d, releases %d)", r.out.uses(1), r.out.releases(1))
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d sword swings at a player 14 blocks off", n)
	}

	r.moveTo(pvpFoe, mgl32.Vec3{22.9, 1, 20.5})
	r.runTicks(60)
	if len(r.out.attacks()) == 0 {
		t.Error("the sword never came out once the player was in reach")
	}
	if r.l.warrior.drawing {
		t.Error("a bow was left drawn in the melee")
	}
}

func TestStrafeFallsBackToTheSwordWithoutArrows(t *testing.T) {
	r := newPvpRig(t, "ranger", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.give(1, idBow, 1)
	r.runTicks(60)
	if r.out.uses(1) != 0 {
		t.Error("drew a bow with no arrow")
	}
	if len(r.out.attacks()) == 0 {
		t.Error("never swung the sword")
	}
}

func TestHurtUnitBacksAwayToEatAGoldenAppleThenEats(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.0, 1, 20.5})
	kitSword(r)
	r.give(1, idGoldenApple, 3)
	r.setHealth(8)
	startGap := r.gap()

	var ateAt = -1
	for i := 0; i < 80 && ateAt < 0; i++ {
		r.tick()
		if r.l.warrior.healing {
			ateAt = i
		}
	}
	if ateAt < 0 {
		t.Fatalf("never began a meal; ended %.2f blocks from the player", r.gap())
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d swings while backing away to eat", n)
	}
	if g := r.gap(); g < eatClear(tactics.Defaults().HealSafe)-0.5 || g <= startGap {
		t.Errorf("began eating %.2f blocks off (started %.2f), want clear of the player", g, startGap)
	}
}

func TestHurtUnitFightsOnWhenTheOpponentWillNotLetItEat(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.0, 1, 20.5})
	kitSword(r)
	r.give(1, idGoldenApple, 3)
	r.setHealth(8)

	var fledSwings, afterSwings int
	for i := 0; i < pvpFleeLimit+120; i++ {
		// The player stays on the unit's heels.
		to := r.feet().Sub(r.foe())
		to[1] = 0
		if to.Len() > 2 {
			r.moveTo(pvpFoe, r.feet().Sub(to.Normalize().Mul(2)))
		}
		before := len(r.out.attacks())
		r.tick()
		swung := len(r.out.attacks()) - before
		if i < pvpFleeLimit {
			fledSwings += swung
		} else {
			afterSwings += swung
		}
		if r.l.warrior.healing {
			t.Fatalf("began a meal at tick %d with the player on its heels", i)
		}
	}
	if fledSwings != 0 {
		t.Errorf("%d swings while still running", fledSwings)
	}
	if afterSwings == 0 {
		t.Error("the unit never turned and fought after the retreat failed")
	}
}

func TestHurtUnitWithNothingToEatJustFights(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.setHealth(8)
	r.runTicks(60)
	if len(r.out.attacks()) == 0 {
		t.Error("a hurt unit with no food never swung")
	}
}

func TestAxeShieldOpensWithTheAxeThenUsesTheSwordThenAxesAgain(t *testing.T) {
	r := newPvpRig(t, "striker", "player/axe-shield", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.give(1, idIronAxe, 1)
	axe, sword := int32(idIronAxe), int32(idDiamondSword)
	r.runTicks(260)

	held := r.heldInAttacks()
	if len(held) < 12 {
		t.Fatalf("only %d attacks in 260 ticks: %v", len(held), held)
	}
	if held[0] != axe {
		t.Fatalf("opened with item %d, want the axe %d", held[0], axe)
	}
	if held[1] != sword || held[2] != sword || held[3] != sword {
		t.Errorf("the swings after the axe blow were %v, want the sword while the shield is down", held[1:4])
	}
	axes := 0
	for _, id := range held {
		if id == axe {
			axes++
		}
	}
	if axes < 2 {
		t.Errorf("only %d axe blows over 260 ticks; the disable runs out after %d", axes, shieldDisableTicks)
	}
	if axes*3 > len(held) {
		t.Errorf("%d of %d blows were with the axe; the sword should carry the fight", axes, len(held))
	}
}

func TestAxeShieldWithoutAnAxeIsTheStrafeFight(t *testing.T) {
	r := newPvpRig(t, "striker", "player/axe-shield", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.runTicks(80)
	if len(r.out.attacks()) == 0 {
		t.Fatal("never swung")
	}
	for _, id := range r.heldInAttacks() {
		if id != idDiamondSword {
			t.Errorf("swung item %d with no axe in the kit", id)
		}
	}
}

func TestStrafeAloneNeverReachesForTheAxe(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	r.give(1, idIronAxe, 1)
	r.runTicks(120)
	for _, id := range r.heldInAttacks() {
		if id == idIronAxe {
			t.Fatal("player/strafe swung the axe")
		}
	}
}

func TestPlayerTacticsDeclineWhatIsNotTheirs(t *testing.T) {
	zombie := belief.Entity{ID: 70, Type: "zombie", Pos: mgl32.Vec3{22.5, 1, 20.5}}
	player := belief.Entity{ID: pvpFoe, Type: "player", Pos: mgl32.Vec3{22.5, 1, 20.5}, Player: true}
	above := player
	above.Pos = mgl32.Vec3{22.5, 9, 20.5}
	// A player-typed id that the belief does not flag as one.
	impostor := player
	impostor.Player = false

	for _, tc := range []struct {
		name   string
		target belief.Entity
		family tactics.Family
		order  Order
		mode   mode
	}{
		{name: "a mob", target: zombie, family: tactics.FamilyZombie},
		{name: "a player on another floor", target: above, family: tactics.FamilyPlayer},
		{name: "an entity not flagged as a player", target: impostor, family: tactics.FamilyPlayer},
		{name: "a hold order", target: player, family: tactics.FamilyPlayer, order: Order{Kind: OrderHold}},
		{name: "a regroup order", target: player, family: tactics.FamilyPlayer, order: Order{Kind: OrderRegroup}},
		{name: "a retreat order", target: player, family: tactics.FamilyPlayer, order: Order{Kind: OrderRetreat}},
		{name: "a retreat mode", target: player, family: tactics.FamilyPlayer, mode: modeRetreat},
	} {
		for _, name := range []string{"player/strafe", "player/axe-shield"} {
			t.Run(tc.name+" "+name, func(t *testing.T) {
				r := newPvpRig(t, "striker", "", mgl32.Vec3{22.5, 1, 20.5})
				kitSword(r)
				r.setSquad(t, &fakeSquad{order: tc.order})
				r.l.mode = tc.mode
				tac, ok := tacticNamed(name, tactics.FamilyPlayer)
				if !ok {
					t.Fatalf("%s is not registered", name)
				}
				before := len(r.out.got)
				handled, err := tac.Engage(context.Background(), &Fight{
					Life: r.l, Actuator: r.a, Pose: r.a.Pose(), Target: tc.target,
					Snapshot: r.tracker.Snapshot(), Params: tactics.Defaults(), Family: tc.family,
				})
				if err != nil || handled {
					t.Errorf("Engage = %v, %v, want declined without error", handled, err)
				}
				if len(r.out.got) != before {
					t.Errorf("a declined tick sent %d packets", len(r.out.got)-before)
				}
			})
		}
	}
}

func TestPlayerTacticsDoNotTouchAMobFightEvenWhenNamed(t *testing.T) {
	base := replay(t, "striker", strikerOnAZombie, func(*Life) {})
	got := replay(t, "striker", strikerOnAZombie, func(l *Life) {
		pr := usingPlayerTactic("player/strafe")
		pr.Tactics[tactics.FamilyZombie] = "player/strafe"
		if err := l.SetProfile(pr); err != nil {
			t.Fatal(err)
		}
	})
	if !reflect.DeepEqual(got, base) {
		t.Errorf("naming a player tactic for zombies changed the zombie fight: %d packets, want %d", len(got), len(base))
	}
}

func TestTheDefaultProfileNeverRunsAPlayerTactic(t *testing.T) {
	run := func(tactic string) []any {
		r := newPvpRig(t, "striker", tactic, mgl32.Vec3{22.9, 1, 20.5})
		kitSword(r)
		r.runTicks(60)
		got := make([]any, len(r.out.got))
		for i, pk := range r.out.got {
			got[i] = pk
		}
		return got
	}
	withTactics(t)
	base := run("")

	stand := &standTactic{name: "player/stand", family: tactics.FamilyPlayer}
	withTactics(t, stand, pvpTactic{name: "player/strafe"})
	if got := run(""); !reflect.DeepEqual(got, base) {
		t.Errorf("registering player tactics changed the default fight: %d packets, want %d", len(got), len(base))
	}
	if stand.ticks != 0 {
		t.Errorf("a player tactic took %d ticks under the default profile", stand.ticks)
	}
	if got := run("player/strafe"); reflect.DeepEqual(got, base) {
		t.Error("naming the tactic changed nothing; the default comparison proves nothing")
	}
}

func TestStrafeDoesNotStepOntoLavaOrOffALedge(t *testing.T) {
	r := newPvpRigAt(t, "striker", "player/strafe", mgl32.Vec3{20.5, 1, 21.5}, mgl32.Vec3{20.5, 1, 19.5})
	kitSword(r)
	// Lava to the south and a void to the north: the ring round the player
	// crosses both, and the unit must stay on the floor between.
	for x := int32(0); x < 40; x++ {
		for z := int32(22); z < 40; z++ {
			r.w.over[protocol.BlockPos{x, 0, z}] = "lava"
		}
		for z := int32(0); z <= 17; z++ {
			for y := int32(-2); y <= 0; y++ {
				r.w.over[protocol.BlockPos{x, y, z}] = "air"
			}
		}
	}
	for i := 0; i < 200; i++ {
		r.tick()
		if !r.a.Pose().OnGround {
			continue
		}
		c := cellOf(r.feet())
		name, ok := r.w.Block(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()})
		if !ok || !safeFloor(name) || name == "air" {
			t.Fatalf("tick %d: unit at %v stands over %q", i, r.feet(), name)
		}
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the unit never fought from the strip of floor it was left")
	}
}

func TestStrafeFlipsTheOrbitWhenABodyBlocksIt(t *testing.T) {
	r := newPvpRig(t, "striker", "player/strafe", mgl32.Vec3{22.9, 1, 20.5})
	kitSword(r)
	// Walls close both sides of the first orbit direction.
	for _, c := range []protocol.BlockPos{{21, 1, 22}, {21, 2, 22}, {22, 1, 22}, {22, 2, 22}, {23, 1, 22}, {23, 2, 22}} {
		r.w.over[c] = "stone"
	}
	r.runTicks(120)
	if len(r.out.attacks()) == 0 {
		t.Error("never swung while the orbit was walled off")
	}
	if g := r.gap(); g > pvpReach+0.5 {
		t.Errorf("the unit ended %.2f blocks from the player", g)
	}
}
