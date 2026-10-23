package life

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// fakeSquad is the squad the warrior engine is fed in tests: one leader,
// one standing order, and the callouts it heard.
type fakeSquad struct {
	leaderID string
	leader   mgl32.Vec3
	leaderOK bool
	order    Order
	callouts []CalloutKind
}

func (f *fakeSquad) Leader() (string, mgl32.Vec3, bool) { return f.leaderID, f.leader, f.leaderOK }
func (f *fakeSquad) Orders() Order                      { return f.order }
func (f *fakeSquad) Callout(c Callout)                  { f.callouts = append(f.callouts, c.Kind) }

func (f *fakeSquad) heard(k CalloutKind) bool {
	for _, c := range f.callouts {
		if c == k {
			return true
		}
	}
	return false
}

// newWarrior rigs a warrior of the given role. The squad is a leaderless
// one until a test wires one in.
func newWarrior(t *testing.T, role string, feet mgl32.Vec3) *rig {
	t.Helper()
	cfg := defaults
	cfg.Role = role
	return newRig(t, cfg, feet)
}

func (r *rig) setSquad(t *testing.T, f *fakeSquad) {
	t.Helper()
	r.l.SetSquad(f)
}

// uses is the item-use transactions sent on a hotbar slot.
func (s *sink) uses(slot int) int {
	n := 0
	for _, pk := range s.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemTransactionData); ok && int(d.HotBarSlot) == slot {
				n++
			}
		}
	}
	return n
}

// releases is the item-release transactions sent on a hotbar slot.
func (s *sink) releases(slot int) int {
	n := 0
	for _, pk := range s.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.ReleaseItemTransactionData); ok && int(d.HotBarSlot) == slot {
				n++
			}
		}
	}
	return n
}

// startUsingInputs counts the inputs that began an item use.
func (s *sink) startUsingInputs() int {
	n := 0
	for _, in := range s.inputs() {
		if in.InputData.Load(packet.InputFlagStartUsingItem) {
			n++
		}
	}
	return n
}

func spawnZombie(r *rig, id uint64, at mgl32.Vec3) { r.spawn(id, "minecraft:zombie", at) }

func TestFocusPrefersLowestHealthThenEntityID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		healthOf map[uint64]float32
		wantType string
		want     uint64
	}{
		{name: "equal health falls to the id", healthOf: nil, want: 50},
		{name: "lower health wins over the id", healthOf: map[uint64]float32{50: 12, 51: 3}, want: 51},
		{name: "unknown health counts as full", healthOf: map[uint64]float32{50: 18}, want: 50},
		{name: "the focus order outranks health", healthOf: map[uint64]float32{50: 2, 51: 18}, wantType: "skeleton", want: 51},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ents := belief.NewEntities()
			ents.Apply(addActor(50, "minecraft:zombie", mgl32.Vec3{22, 1, 20}))
			ents.Apply(addActor(51, "minecraft:skeleton", mgl32.Vec3{23, 1, 20}))
			ents.Apply(addActor(60, "minecraft:cow", mgl32.Vec3{21, 1, 20}))
			ents.Apply(addPlayer(61, mgl32.Vec3{20, 1, 19}))
			ents.Apply(addActor(62, "minecraft:creeper", mgl32.Vec3{20, 1, 18}))
			ents.Apply(addActor(63, "minecraft:zombie", mgl32.Vec3{22, 9, 20}))
			ents.Apply(addActor(64, "minecraft:zombie", mgl32.Vec3{22, 1, 35}))
			q := focusQuery{anchor: start, self: start, wantType: tc.wantType}
			if tc.healthOf != nil {
				h := tc.healthOf
				q.healthOf = func(id uint64) (float32, bool) {
					v, ok := h[id]
					return v, ok
				}
			}
			best, ok := focusTarget(ents, q)
			if !ok {
				t.Fatal("no focus found")
			}
			if best.ID != tc.want {
				t.Fatalf("focus = %d, want %d", best.ID, tc.want)
			}
		})
	}
}

// addActor and addPlayer build the packets a belief feed is seeded with.
func addActor(id uint64, typ string, at mgl32.Vec3) *packet.AddActor {
	return &packet.AddActor{EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: typ, Position: at}
}

func addPlayer(id uint64, at mgl32.Vec3) *packet.AddPlayer {
	return &packet.AddPlayer{EntityRuntimeID: id, AbilityData: protocol.AbilityData{EntityUniqueID: int64(id)}, Position: at}
}

func TestProvokedMobsAreFocusedOnlyWhileSuspect(t *testing.T) {
	ents := belief.NewEntities()
	ents.Apply(addActor(70, "minecraft:enderman", mgl32.Vec3{21, 1, 20}))
	q := focusQuery{anchor: start, self: start}
	if _, ok := focusTarget(ents, q); ok {
		t.Fatal("focused an enderman that had done nothing")
	}
	q.attacker, q.attackerUntil = 70, 10
	q.tick = 5
	best, ok := focusTarget(ents, q)
	if !ok || best.ID != 70 {
		t.Fatalf("focus = %d ok=%v, want the suspect enderman", best.ID, ok)
	}
	q.tick = 11
	if _, ok := focusTarget(ents, q); ok {
		t.Fatal("kept focusing the enderman after the suspect window")
	}
}

func TestThreeWarriorsAgreeOnTheSharedTargetWithoutMessages(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true}
	var targets []uint64
	for i, at := range []mgl32.Vec3{{22.5, 1, 18.5}, {18.5, 1, 22.5}, {23.5, 1, 23.5}} {
		r := newWarrior(t, "striker", at)
		r.setSquad(t, squad)
		spawnZombie(r, 52, mgl32.Vec3{22, 1, 22})
		spawnZombie(r, 50, mgl32.Vec3{21, 1, 21})
		spawnZombie(r, 51, mgl32.Vec3{22, 1, 21.5})
		r.player(60, mgl32.Vec3{21.5, 1, 20.5})
		if !r.step() {
			t.Fatalf("unit %d did not engage", i)
		}
		targets = append(targets, r.l.target.ID)
	}
	for _, id := range targets {
		if id != 50 {
			t.Fatalf("targets = %v, want all three on the lowest id 50", targets)
		}
	}
}

func TestWarriorFocusesThreatsInLeaderReachBeyondItsOwn(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true}

	w := newWarrior(t, "striker", start)
	w.setSquad(t, squad)
	spawnZombie(w, 50, mgl32.Vec3{27.5, 1, 20.5}) // 7 from the leader, past the old 6-block threat range
	if !w.step() {
		t.Fatal("warrior ignored a threat near its leader")
	}
	if w.l.target.ID != 50 {
		t.Fatalf("target = %d, want 50", w.l.target.ID)
	}

	d := newRig(t, defaults, start)
	spawnZombie(d, 50, mgl32.Vec3{27.5, 1, 20.5})
	if d.step() {
		t.Error("reactive defence engaged a mob beyond its threat range")
	}
}

func TestUnknownRoleLeavesTheReactiveDefenceInCharge(t *testing.T) {
	r := newRig(t, Config{Wander: true, Radius: 8, Defend: true, Eat: true, Role: "wizard"}, start)
	if r.l.warrior != nil {
		t.Fatal("a role string that names no warrior built one")
	}
	spawnZombie(r, 50, mgl32.Vec3{21.5, 1, 20.5})
	r.run(5)
	if !r.l.engaged || len(r.out.attacks()) == 0 {
		t.Fatal("the reactive defence did not answer a zombie at arm's length")
	}
}

func TestHoldOrderKeepsTheWarriorAtPosition(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderHold}}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})
	for i := 0; i < 60; i++ {
		r.step()
		if d := dist(r.feet(), start); d > 1.0 {
			t.Fatalf("tick %d: hold order walked %.1f blocks", i, d)
		}
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks on a mob the hold order kept the unit from", n)
	}
}

func TestRetreatOrderBacksTheWarriorOff(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderRetreat}}
	r := newWarrior(t, "vanguard", start)
	r.setSquad(t, squad)
	r.give(0, 301, 1)
	z := mgl32.Vec3{22.5, 1, 20.5}
	spawnZombie(r, 50, z)
	for i := 0; i < 40; i++ {
		r.step()
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks under a retreat order", n)
	}
	if d := dist(r.feet(), z); d < 5 {
		t.Errorf("%.1f blocks from the mob after retreating, want ground gained", d)
	}
}

func TestFocusOrderPrefersTheNamedType(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderFocus, Target: "skeleton"}}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	spawnZombie(r, 50, mgl32.Vec3{21, 1, 20})
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{22, 1, 20})
	if !r.step() {
		t.Fatal("did not engage")
	}
	if r.l.target.ID != 51 {
		t.Fatalf("target = %d, want the ordered skeleton 51", r.l.target.ID)
	}
}

func TestStrikerLandsCriticalHitsWhileFalling(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1) // diamond sword
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})

	hits := 0
	for i := 0; i < 100; i++ {
		pose := r.a.Pose()
		before := len(r.out.attacks())
		if !r.step() {
			t.Fatalf("tick %d: not engaged", i)
		}
		if len(r.out.attacks()) > before {
			hits++
			if pose.OnGround || pose.Rise() >= 0 {
				t.Fatalf("attack %d at tick %d from on-ground or rising: the Striker must swing mid-fall", hits, i)
			}
		}
	}
	if hits < 4 {
		t.Fatalf("%d attacks in 100 ticks, want the cadence kept up by crit jumps", hits)
	}
}

func TestVanguardSwingsFromTheGroundAndShieldsAgainstShooters(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	r.give(1, 360, 1) // shield
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{28.5, 1, 20.5})

	hits := 0
	for i := 0; i < 60; i++ {
		pose := r.a.Pose()
		before := len(r.out.attacks())
		r.step()
		if len(r.out.attacks()) > before {
			hits++
			if !pose.OnGround {
				t.Fatalf("attack %d at tick %d left the ground: the Vanguard plants", hits, i)
			}
		}
	}
	if hits < 3 {
		t.Fatalf("%d attacks in 60 ticks, want the ground cadence", hits)
	}
	if n := r.out.uses(1); n < 2 {
		t.Errorf("%d shield raises against a live shooter, want it kept up between swings", n)
	}
	if n := r.out.releases(1); n < 2 {
		t.Errorf("%d shield lowers to swing, want one per swing", n)
	}
	if d := dist(r.feet(), start); d > 0.5 {
		t.Errorf("the Vanguard wandered %.1f blocks holding the front", d)
	}
}

func TestShieldRisesAgainstAnIncomingArrowAndLowersToSwing(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.give(1, 360, 1)
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})
	r.run(15) // the melee is under way

	// An arrow flies in from the east, closer every tick.
	arrow := mgl32.Vec3{24.5, 1.5, 20.5}
	r.spawn(70, "minecraft:arrow", arrow)
	raised := false
	for i := 0; i < 12; i++ {
		arrow = arrow.Sub(mgl32.Vec3{0.35, 0, 0})
		r.moveTo(70, arrow)
		r.step()
		if r.out.uses(1) > 0 {
			raised = true
		}
	}
	if !raised {
		t.Fatal("an arrow closing on the unit never raised the shield")
	}
	if r.out.startUsingInputs() == 0 {
		t.Error("the shield raise was not begun as an item use")
	}
	// The arrow is gone; the next swing must lower the shield first.
	arrow = arrow.Sub(mgl32.Vec3{40, 0, 0})
	r.moveTo(70, arrow)
	for i := 0; i < 40 && r.out.releases(1) == 0; i++ {
		r.step()
	}
	if n := r.out.releases(1); n == 0 {
		t.Fatal("the shield never came down to swing")
	}
}

func TestMeleeCirclesTheTargetInReach(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	z := mgl32.Vec3{22.5, 1, 20.5}
	spawnZombie(r, 50, z)

	swept, prev, have := 0.0, 0.0, false
	for i := 0; i < 80; i++ {
		if !r.step() {
			t.Fatalf("tick %d: not engaged", i)
		}
		f := r.feet()
		d := dist(f, z)
		if d < 1.2 || d > 3.3 {
			t.Fatalf("tick %d: %.1f blocks from the target, want the melee band held", i, d)
		}
		bearing := math.Atan2(float64(f.Z()-z.Z()), float64(f.X()-z.X())) * 180 / math.Pi
		if have {
			delta := bearing - prev
			for delta > 180 {
				delta -= 360
			}
			for delta < -180 {
				delta += 360
			}
			swept += math.Abs(delta)
		}
		prev, have = bearing, true
	}
	if swept < 60 {
		t.Errorf("the unit swept %.0f degrees around the target in 80 ticks, want circling", swept)
	}
}

func TestStrikerLeavesTheShootersLineOnTheLeader(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{28.5, 1, 20.5}, leaderOK: true}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	r.give(0, 303, 1)
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{12.5, 1, 20.5})
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})

	for i := 0; i < 40; i++ {
		if !r.step() {
			t.Fatalf("tick %d: not engaged", i)
		}
		if d := lineDistance(r.feet(), mgl32.Vec3{12.5, 1, 20.5}, mgl32.Vec3{28.5, 1, 20.5}); d > lineWidth {
			return
		}
	}
	t.Fatal("the Striker stayed on the skeleton's line to the leader")
}

func TestVanguardHoldsTheShootersLineWithItsShield(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{28.5, 1, 20.5}, leaderOK: true}
	r := newWarrior(t, "vanguard", start)
	r.setSquad(t, squad)
	r.give(0, 303, 1)
	r.give(1, 360, 1)
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{12.5, 1, 20.5})
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})

	for i := 0; i < 40; i++ {
		r.step()
		if d := dist(r.feet(), start); d > 0.6 {
			t.Fatalf("tick %d: the Vanguard left the front by %.1f blocks", i, d)
		}
	}
	if r.out.uses(1) == 0 {
		t.Error("the Vanguard never raised its shield against the shooter")
	}
}

func TestFlankCellStandsWideOfTheFront(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{14.5, 1, 20.5}, leaderOK: true}
	zombieAt := mgl32.Vec3{26.5, 1, 20.5}

	s := newWarrior(t, "striker", mgl32.Vec3{20.5, 1, 24.5})
	s.setSquad(t, squad)
	goal, ok := s.l.flankCell(s.a.Pose(), belief.Entity{ID: 50, Pos: zombieAt})
	if !ok {
		t.Fatal("no flank cell on open floor")
	}
	if off := goal.Z() - 20; off < 1 {
		t.Errorf("flank cell %v is on the leader's line on the target, want it to the side", goal)
	}
	if dx := abs32(goal.X() - 26); dx > 1 {
		t.Errorf("flank cell %v strayed %v blocks along the front", goal, dx)
	}

	v := newWarrior(t, "vanguard", mgl32.Vec3{20.5, 1, 24.5})
	v.setSquad(t, squad)
	if _, ok := v.l.flankCell(v.a.Pose(), belief.Entity{ID: 50, Pos: zombieAt}); ok {
		t.Error("the Vanguard was given a flank cell instead of the front")
	}
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func TestRangerShootsOnTheBowCadenceAndStaysOutOfMeleeWithRoom(t *testing.T) {
	r := newWarrior(t, "ranger", start)
	r.give(0, 303, 1)
	r.give(1, 361, 1)   // bow
	r.give(20, 362, 32) // arrows
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})

	for i := 0; i < 130; i++ {
		if !r.step() {
			t.Fatalf("tick %d: not engaged", i)
		}
	}
	draws, shots := r.out.uses(1), r.out.releases(1)
	if draws < 3 {
		t.Fatalf("%d bow draws in 130 ticks, want the cadence of one a second and a half", draws)
	}
	if shots != draws {
		t.Fatalf("%d draws but %d releases: a draw was never fired", draws, shots)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d melee attacks from a Ranger with arrows and room", n)
	}
	if d := dist(r.feet(), mgl32.Vec3{28.5, 1, 20.5}); d < 6 {
		t.Errorf("the Ranger closed to %.1f blocks with nothing threatening it", d)
	}
	if r.out.startUsingInputs() == 0 {
		t.Error("a bow draw was not begun as an item use")
	}
}

func TestRangerGivesGroundWhenAMobClosesInsideKiteRange(t *testing.T) {
	r := newWarrior(t, "ranger", start)
	r.give(0, 303, 1)
	r.give(1, 361, 1)
	r.give(20, 362, 32)
	z := mgl32.Vec3{26.5, 1, 20.5}
	spawnZombie(r, 50, z)

	closedTo := math.MaxFloat64
	for i := 0; i < 120; i++ {
		z = z.Sub(mgl32.Vec3{0.1, 0, 0}) // a zombie walking in
		r.moveTo(50, z)
		r.step()
		if d := dist(r.feet(), z); d < closedTo {
			closedTo = d
		}
	}
	// A walking mob presses all game; kiting holds it off, not out.
	if closedTo < 3.5 {
		t.Errorf("the mob closed to %.1f blocks, want the kite to hold it around five", closedTo)
	}
	if n := r.out.releases(1); n < 2 {
		t.Errorf("%d arrows fired while kiting, want the shooting to carry on", n)
	}
	if dx := r.feet().X() - start.X(); dx > -1 {
		t.Errorf("the Ranger held its ground (%.1f along x) instead of giving it", dx)
	}
}

func TestRangerWithoutArrowsFallsBackToMelee(t *testing.T) {
	r := newWarrior(t, "ranger", start)
	r.give(0, 303, 1)
	r.give(1, 361, 1) // a bow and no arrows
	spawnZombie(r, 50, mgl32.Vec3{24.5, 1, 20.5})
	for i := 0; i < 80 && len(r.out.attacks()) == 0; i++ {
		r.step()
	}
	if len(r.out.attacks()) == 0 {
		t.Fatal("a Ranger without arrows never swung")
	}
	if r.out.uses(1) != 0 {
		t.Error("drew a bow with no arrows to fire")
	}
}

func TestCorneredRangerMeleesInsteadOfBackingOffForever(t *testing.T) {
	wall := func(r *rig) {
		for z := int32(18); z <= 22; z++ {
			for y := int32(1); y <= 2; y++ {
				r.w.over[protocol.BlockPos{19, y, z}] = "stone"
			}
		}
	}
	for _, tc := range []struct {
		name string
		at   mgl32.Vec3
	}{
		{name: "a mob at arm's length", at: mgl32.Vec3{21.5, 1, 20.5}},
		{name: "a mob inside bow range with the wall behind", at: mgl32.Vec3{24.5, 1, 20.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newWarrior(t, "ranger", start)
			r.give(0, 303, 1)
			r.give(1, 361, 1)   // bow
			r.give(20, 362, 32) // arrows
			wall(r)
			spawnZombie(r, 50, tc.at)
			for i := 0; i < 80 && len(r.out.attacks()) == 0; i++ {
				r.step()
			}
			if n := len(r.out.attacks()); n == 0 {
				t.Fatalf("a cornered Ranger never swung at a zombie %.1f blocks away", dist(r.feet(), tc.at))
			}
		})
	}
}

func TestHurtWarriorEatsAGoldenAppleBehindTheLine(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.give(3, 363, 2) // golden apples
	r.setHealth(10)
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})

	for i := 0; i < 12; i++ {
		if !r.step() {
			t.Fatalf("tick %d: not engaged", i)
		}
	}
	if r.out.uses(3) != 1 {
		t.Fatalf("%d uses of the apple, want the meal started", r.out.uses(3))
	}
	// The server takes a bite; the meal winds down.
	r.give(3, 363, 0)
	for i := 0; i < 10 && !r.ate(); i++ {
		r.step()
	}
	if !r.ate() {
		t.Fatal("the meal never finished")
	}
	if d := dist(r.feet(), mgl32.Vec3{28.5, 1, 20.5}); d < 7 {
		t.Errorf("the unit left cover to fight at %.1f blocks while eating", d)
	}
}

// ate says whether a meal completed with an eat log.
func (r *rig) ate() bool {
	for _, l := range r.log.lines {
		if l.event == "eat" {
			return true
		}
	}
	return false
}

func TestHurtWarriorRegroupsTowardTheLeaderWhenPressured(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{10.5, 1, 20.5}, leaderOK: true}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	r.give(0, 303, 1)
	r.give(3, 363, 2)
	r.setHealth(10)
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})

	arrived, ateUnderPressure, foughtBeforeArrival := false, false, false
	for i := 0; i < 140; i++ {
		attacks := len(r.out.attacks())
		r.step()
		// The mob stays on the unit's heels all the way: this is a chase.
		r.moveTo(50, r.feet().Add(mgl32.Vec3{1.2, 0, 0}))
		atLeader := dist(r.feet(), mgl32.Vec3{10.5, 1, 20.5}) <= regroupWithin+0.5
		if r.out.uses(3) > 0 && !atLeader {
			ateUnderPressure = true
		}
		if len(r.out.attacks()) > attacks && !atLeader {
			foughtBeforeArrival = true
		}
		if atLeader {
			arrived = true
		}
	}
	if !arrived {
		t.Fatalf("the unit never reached the leader: %.1f blocks away", dist(r.feet(), mgl32.Vec3{10.5, 1, 20.5}))
	}
	if ateUnderPressure {
		t.Error("ate while pressed before reaching the leader")
	}
	if foughtBeforeArrival {
		t.Error("turned to fight before reaching the leader")
	}
	if !squad.heard(CalloutLowHealth) || !squad.heard(CalloutNeedHeal) {
		t.Errorf("callouts = %v, want low_health and need_heal", squad.callouts)
	}
}

func TestHurtWarriorAbortsTheMealWhenAnEnemyClosesIn(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.give(3, 363, 2)
	r.setHealth(10)
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})

	for i := 0; i < 10; i++ {
		r.step()
	}
	if r.out.uses(3) != 1 {
		t.Fatal("the meal never started")
	}
	// The zombie teleports in: the meal is dropped, not finished.
	r.moveTo(50, mgl32.Vec3{21.5, 1, 20.5})
	for i := 0; i < 5; i++ {
		r.step()
	}
	if r.ate() {
		t.Fatal("a meal pressed by an enemy completed")
	}
	if r.out.releases(3) == 0 {
		t.Error("the interrupted meal was not let go")
	}
}

func TestHurtWarriorFightsWhenAnEnemyIsInsideHealSafeButBeyondPressRange(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.give(3, 363, 2) // golden apples
	r.setHealth(10)
	// 2.0 blocks: past pressRange, so the unit wants to eat, but inside
	// healSafe, so the meal aborts the tick it starts. It must fight instead
	// of restarting the meal every tick.
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})

	for i := 0; i < 40 && len(r.out.attacks()) == 0; i++ {
		r.step()
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Fatalf("no attacks in 40 ticks with a zombie %.1f blocks away", dist(r.feet(), mgl32.Vec3{22.5, 1, 20.5}))
	}
	if r.l.warrior.healWant {
		t.Error("healWant stayed set with an enemy inside healSafe")
	}
	if n := r.out.uses(3); n != 0 {
		t.Errorf("%d apple uses with an enemy inside healSafe, want the fight instead", n)
	}
}

func TestNeedHealCalloutWhenThereIsNothingToEat(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	r.give(0, 303, 1)
	r.setHealth(10)
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})
	r.run(20)
	if !squad.heard(CalloutLowHealth) || !squad.heard(CalloutNeedHeal) {
		t.Fatalf("callouts = %v, want low_health and need_heal with an empty hotbar", squad.callouts)
	}
	if n := len(squad.callouts); n > 4 {
		t.Errorf("%d callouts in 20 ticks, want the hold-down to keep it to a couple", n)
	}
	// Nothing to eat, not near death: the fight goes on.
	for i := 0; i < 100 && len(r.out.attacks()) == 0; i++ {
		r.step()
	}
	if n := len(r.out.attacks()); n == 0 {
		t.Error("a hungry warrior stopped fighting")
	}
}

func TestTargetDownCalloutAfterLandingHits(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	r.give(0, 303, 1)
	spawnZombie(r, 50, mgl32.Vec3{22.5, 1, 20.5})
	for i := 0; i < 15; i++ {
		r.step()
	}
	if len(r.out.attacks()) == 0 {
		t.Fatal("never struck the target, so nothing is known about it")
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.run(5)
	if !squad.heard(CalloutTargetDown) {
		t.Fatalf("callouts = %v, want target_down for a struck target that vanished", squad.callouts)
	}
}

func TestWarriorsNeverTargetPlayers(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true}
	for _, role := range []string{"vanguard", "striker", "ranger"} {
		t.Run(role, func(t *testing.T) {
			r := newWarrior(t, role, start)
			r.setSquad(t, squad)
			r.give(0, 303, 1)
			r.give(1, 361, 1)
			r.give(20, 362, 32)
			r.player(60, mgl32.Vec3{21.5, 1, 20.5})
			r.player(61, mgl32.Vec3{20.5, 1, 22})
			r.run(100)
			if r.l.target.Player {
				t.Fatal("a warrior targeted a player")
			}
			if n := len(r.out.attacks()); n != 0 {
				t.Fatalf("%d attacks, some may have landed on players", n)
			}
		})
	}
}

func TestACreeperStillBacksAWarriorOff(t *testing.T) {
	r := newWarrior(t, "striker", start)
	r.give(0, 301, 1)
	c := mgl32.Vec3{22.5, 1, 20.5}
	r.spawn(80, "minecraft:creeper", c)
	for i := 0; i < 40; i++ {
		r.step()
		c = c.Add(mgl32.Vec3{-0.03, 0, 0})
		r.moveTo(80, c)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks on a creeper", n)
	}
	if d := dist(r.feet(), c); d < 3 {
		t.Errorf("%.1f blocks from the creeper after backing away", d)
	}
}
