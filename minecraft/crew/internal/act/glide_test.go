package act

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// airborne is a unit falling from height, as one that has just left a ledge.
func airborne(w Solid, y float32) (*Actuator, *sink) {
	s := &sink{}
	a := New(s, 7, Pose{Feet: mgl32.Vec3{0.5, y, 0.5}, vy: -0.1}, w)
	a.pose.OnGround = false
	return a, s
}

func glideFlags(in *packet.PlayerAuthInput) (start, stop, jump bool) {
	return hasFlag(in, packet.InputFlagStartGliding), hasFlag(in, packet.InputFlagStopGliding),
		hasFlag(in, packet.InputFlagJumping) && hasFlag(in, packet.InputFlagJumpDown) && hasFlag(in, packet.InputFlagStartJumping)
}

func glideFor(t *testing.T, a *Actuator, n int, in Input) {
	t.Helper()
	for range n {
		if err := a.Tick(in); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGlideStartsWithTheJumpAndGlideFlagsAndKeepsGoingWithoutThem(t *testing.T) {
	a, s := airborne(flat(), 300)
	glideFor(t, a, 6, Input{Glide: true, Pitch: 10})
	ins := s.inputs(t)
	if !a.Pose().Gliding() {
		t.Fatal("not gliding after six glide ticks")
	}
	// The server only starts a glide on a jump press in the air: the start
	// flag alone is ignored.
	start, _, jump := glideFlags(ins[0])
	if !start || !jump {
		t.Errorf("first tick start/jump = %v/%v, want both", start, jump)
	}
	for i, in := range ins[4:] {
		if start, stop, jump := glideFlags(in); start || stop || jump {
			t.Errorf("tick %d still carries a glide edge: start=%v stop=%v jump=%v", i+4, start, stop, jump)
		}
	}
}

func TestGlideNeedsTheAirUnderTheUnit(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	glideFor(t, a, 3, Input{Glide: true, Pitch: 10})
	if a.Pose().Gliding() {
		t.Fatal("glided from the ground")
	}
	for _, in := range s.inputs(t) {
		if start, _, _ := glideFlags(in); start {
			t.Fatal("asked the server to glide from the ground")
		}
	}
}

func TestGlideSettlesAtTheMeasuredCruiseSpeed(t *testing.T) {
	a, _ := airborne(flat(), 5000)
	glideFor(t, a, 400, Input{Glide: true, Pitch: 10})
	v := a.Pose().Velocity()
	// Measured on the server: 1.65-1.68 blocks per tick at pitch +10.
	if speed := v.Len(); speed < 1.55 || speed > 1.8 {
		t.Errorf("cruise speed %.3f b/t, want about 1.67", speed)
	}
}

func TestGlideStepsThePositionByTheVelocityItSent(t *testing.T) {
	a, s := airborne(flat(), 5000)
	glideFor(t, a, 30, Input{Glide: true, Pitch: 10})
	before := a.Pose().Feet
	glideFor(t, a, 1, Input{Glide: true, Pitch: 10})
	ins := s.inputs(t)
	got := a.Pose().Feet.Sub(before)
	d := ins[len(ins)-1].Delta
	for i := range got {
		near(t, "delta axis", float64(d[i]), float64(got[i]), 1e-4)
	}
}

func TestDivingGainsSpeedAndClimbingLosesIt(t *testing.T) {
	dive, _ := airborne(flat(), 5000)
	glideFor(t, dive, 80, Input{Glide: true, Pitch: 45})
	climb, _ := airborne(flat(), 5000)
	glideFor(t, climb, 40, Input{Glide: true, Pitch: 10})
	glideFor(t, climb, 40, Input{Glide: true, Pitch: -40})
	if dive.Pose().Velocity().Len() <= climb.Pose().Velocity().Len() {
		t.Errorf("dive %.2f b/t not faster than climb %.2f b/t", dive.Pose().Velocity().Len(), climb.Pose().Velocity().Len())
	}
	if climb.Pose().Velocity().Y() <= dive.Pose().Velocity().Y() {
		t.Error("the climb does not rise against the dive")
	}
}

func rocketItems(a *Actuator) belief.Item {
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:firework_rocket", RuntimeID: 401}})
	return belief.Item{Slot: 2, Name: "firework_rocket", Count: 16}
}

func TestARocketWhileGlidingAddsTheBoostAlongTheLook(t *testing.T) {
	a, _ := airborne(flat(), 5000)
	rocket := rocketItems(a)
	glideFor(t, a, 12, Input{Glide: true, Pitch: -5})
	slow := a.Pose().Velocity().Len()
	if err := a.UseRocket(2, rocket); err != nil {
		t.Fatal(err)
	}
	if !a.Pose().Boosting() {
		t.Fatal("no boost after a rocket")
	}
	glideFor(t, a, 10, Input{Glide: true, Pitch: -5})
	if fast := a.Pose().Velocity().Len(); fast < slow+0.5 {
		t.Errorf("speed %.2f -> %.2f b/t, want at least half a block per tick more", slow, fast)
	}
}

func TestUseRocketSendsTheClickAirUseOfTheStack(t *testing.T) {
	a, s := airborne(flat(), 5000)
	rocket := rocketItems(a)
	glideFor(t, a, 3, Input{Glide: true})
	if err := a.UseRocket(2, rocket); err != nil {
		t.Fatal(err)
	}
	u := s.last(t).(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemTransactionData)
	if u.ActionType != protocol.UseItemActionClickAir || u.HotBarSlot != 2 || u.HeldItem.Stack.NetworkID != 401 {
		t.Errorf("use = %+v, want a click-air with the rocket from slot 2", u)
	}
}

func TestARocketOnTheGroundBoostsNothing(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	rocket := rocketItems(a)
	if err := a.UseRocket(2, rocket); err != nil {
		t.Fatal(err)
	}
	if a.Pose().Boosting() {
		t.Error("a boost on the ground")
	}
}

func TestTheBoostRunsOutAfterItsTicks(t *testing.T) {
	a, _ := airborne(flat(), 5000)
	rocket := rocketItems(a)
	glideFor(t, a, 5, Input{Glide: true})
	if err := a.UseRocket(2, rocket); err != nil {
		t.Fatal(err)
	}
	glideFor(t, a, rocketBoostTicks, Input{Glide: true})
	if a.Pose().Boosting() {
		t.Errorf("still boosting %d ticks after the rocket", rocketBoostTicks)
	}
}

func TestLettingGoOfTheGlideSendsStopAndFalls(t *testing.T) {
	a, s := airborne(flat(), 5000)
	glideFor(t, a, 20, Input{Glide: true, Pitch: 10})
	glideFor(t, a, 1, Input{})
	ins := s.inputs(t)
	if _, stop, _ := glideFlags(ins[len(ins)-1]); !stop {
		t.Error("no stop-gliding flag when the glide is let go")
	}
	if a.Pose().Gliding() {
		t.Error("still gliding")
	}
	if a.Pose().Velocity().Len() == 0 {
		t.Error("the unit lost all momentum on letting go")
	}
	glideFor(t, a, 1, Input{})
	ins = s.inputs(t)
	if _, stop, _ := glideFlags(ins[len(ins)-1]); stop {
		t.Error("stop-gliding sent twice")
	}
}

func TestLandingEndsTheGlideWithAStopFlag(t *testing.T) {
	a, s := airborne(flat(), 12)
	var stops int
	for range 600 {
		glideFor(t, a, 1, Input{Glide: true, Pitch: 20})
		ins := s.inputs(t)
		if _, stop, _ := glideFlags(ins[len(ins)-1]); stop {
			stops++
		}
		if a.Pose().OnGround {
			break
		}
	}
	if !a.Pose().OnGround || a.Pose().Gliding() {
		t.Fatalf("on ground %v gliding %v after a long glide to the floor", a.Pose().OnGround, a.Pose().Gliding())
	}
	if stops != 1 {
		t.Errorf("%d stop flags on landing, want 1", stops)
	}
	near(t, "landed height", float64(a.Pose().Feet.Y()), 0, 1e-3)
}

func TestAWallStopsTheGlideOnThatAxis(t *testing.T) {
	w := world{floor: -1, extra: map[protocol.BlockPos]bool{}}
	for y := int32(0); y < 400; y++ {
		for x := int32(-2); x <= 2; x++ {
			w.extra[protocol.BlockPos{x, y, 12}] = true
		}
	}
	a, _ := airborne(w, 300)
	glideFor(t, a, 200, Input{Glide: true, Pitch: 10})
	if z := a.Pose().Feet.Z(); z > 12-halfWidth+0.01 || z < 12-halfWidth-0.1 {
		t.Errorf("z=%.2f, want it resting on the wall face", z)
	}
	if vz := a.Pose().Velocity().Z(); vz > 0.01 {
		t.Errorf("still pushing into the wall at %.2f b/t", vz)
	}
}

func TestACorrectionWhileGlidingKeepsTheGlideAndTakesTheServersVelocity(t *testing.T) {
	a, _ := airborne(flat(), 5000)
	glideFor(t, a, 20, Input{Glide: true, Pitch: 10})
	moved := a.Pose().Feet.Add(mgl32.Vec3{3, 0, 0})
	a.Apply(&packet.CorrectPlayerMovePrediction{
		PredictionType: packet.PredictionTypePlayer,
		Position:       moved.Add(mgl32.Vec3{0, eyeHeight, 0}),
		Delta:          mgl32.Vec3{0.2, -0.3, 1.4},
		Tick:           a.Ticks() + firstTick - 1,
	})
	p := a.Pose()
	if !p.Gliding() {
		t.Fatal("a correction ended the glide")
	}
	if p.Feet != moved {
		t.Errorf("feet %v, want the corrected %v", p.Feet, moved)
	}
	want := mgl32.Vec3{0.2, -0.3, 1.4}
	for i := range want {
		near(t, "velocity axis", float64(p.Velocity()[i]), float64(want[i]), 1e-5)
	}
	if math.IsNaN(float64(p.Velocity().Len())) {
		t.Fatal("velocity is NaN")
	}
}
