package act

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func sprintFlags(in *packet.PlayerAuthInput) (down, on, start, stop bool) {
	return hasFlag(in, packet.InputFlagSprintDown), hasFlag(in, packet.InputFlagSprinting),
		hasFlag(in, packet.InputFlagStartSprinting), hasFlag(in, packet.InputFlagStopSprinting)
}

func TestSprintStepsAtTheSprintSpeed(t *testing.T) {
	p, info := Pose{OnGround: true}.advance(1, false, true, flat())
	near(t, "first sprint step", float64(info.delta[2]), sprintAccel, 1e-6)
	if !p.Sprinting() {
		t.Fatal("not sprinting after a forward sprint tick")
	}

	walk, sprint := Pose{OnGround: true}, Pose{OnGround: true}
	var wd, sd float32
	for range 40 {
		var wi, si stepInfo
		walk, wi = walk.advance(1, false, false, flat())
		sprint, si = sprint.advance(1, false, true, flat())
		wd, sd = wi.delta[2], si.delta[2]
	}
	// Steady state of v = (v + a) * 0.546.
	near(t, "walk steady step", float64(wd), walkAccel/(1-groundCarry), 2e-3)
	near(t, "sprint steady step", float64(sd/wd), sprintScale, 1e-3)
}

func TestSprintJumpAddsTheBoostAlongTheFacing(t *testing.T) {
	p, info := Pose{OnGround: true, Yaw: 90}.advance(1, true, true, flat())
	if !info.takeoff {
		t.Fatal("no takeoff")
	}
	near(t, "boost along facing", float64(-info.delta[0]), sprintAccel+sprintJumpBoost, 1e-5)

	_, plain := Pose{OnGround: true, Yaw: 90}.advance(1, true, false, flat())
	near(t, "plain jump has no boost", float64(-plain.delta[0]), walkAccel, 1e-5)

	// Airborne the sprint carries, with the sprint air control.
	_, air := p.advance(1, false, true, flat())
	_, airWalk := p.advance(1, false, false, flat())
	near(t, "air control gain", float64(airWalk.delta[0]-air.delta[0]), airSprintAccel-airAccel, 1e-6)
}

func TestSprintEndsOnACollisionAndStaysEndedWhileHeld(t *testing.T) {
	w := world{floor: -1, extra: map[protocol.BlockPos]bool{{0, 0, 2}: true, {0, 1, 2}: true}}
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})

	var ins []*packet.PlayerAuthInput
	for range 12 {
		if err := a.Tick(Input{Forward: 1, Sprint: true}); err != nil {
			t.Fatal(err)
		}
		ins = s.inputs(t)
	}
	down, on, start, _ := sprintFlags(ins[0])
	if !down || !on || !start {
		t.Errorf("first tick down/on/start = %v/%v/%v, want all set", down, on, start)
	}
	_, on, start, _ = sprintFlags(ins[1])
	if !on || start {
		t.Errorf("second tick sprinting=%v start=%v, want sprinting without a second start", on, start)
	}
	var stops, starts int
	for _, in := range ins {
		_, _, st, sp := sprintFlags(in)
		if st {
			starts++
		}
		if sp {
			stops++
		}
	}
	if starts != 1 || stops != 1 {
		t.Errorf("starts=%d stops=%d across a run into a wall, want one each", starts, stops)
	}
	last := ins[len(ins)-1]
	if _, on, _, _ := sprintFlags(last); on {
		t.Error("still flagged sprinting against the wall while the input is held")
	}

	if err := a.Tick(Input{Forward: 1}); err != nil {
		t.Fatal(err)
	}
	a.pose.Feet = mgl32.Vec3{0.5, 0, 0.5}
	if err := a.Tick(Input{Forward: 1, Sprint: true}); err != nil {
		t.Fatal(err)
	}
	ins = s.inputs(t)
	if _, on, start, _ := sprintFlags(ins[len(ins)-1]); !on || !start {
		t.Errorf("releasing and pressing sprint again: sprinting=%v start=%v, want a fresh start", on, start)
	}
}

func TestSprintEndsWhenTheUnitStopsPushingForward(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	for _, in := range []Input{{Forward: 1, Sprint: true}, {Sprint: true}, {Sprint: true}, {Forward: 1, Sprint: true}} {
		if err := a.Tick(in); err != nil {
			t.Fatal(err)
		}
	}
	ins := s.inputs(t)
	want := []struct{ down, on, start, stop bool }{
		{true, true, true, false},
		{false, false, false, true},
		{false, false, false, false},
		{true, true, true, false},
	}
	for i, w := range want {
		d, on, st, sp := sprintFlags(ins[i])
		if d != w.down || on != w.on || st != w.start || sp != w.stop {
			t.Errorf("tick %d down/on/start/stop = %v/%v/%v/%v, want %v/%v/%v/%v", i, d, on, st, sp, w.down, w.on, w.start, w.stop)
		}
	}
}

func TestSprintIsDroppedInWater(t *testing.T) {
	p := Pose{OnGround: true, sprinting: true}
	next, _ := p.advance(1, false, true, lake{})
	if next.Sprinting() {
		t.Error("still sprinting in water")
	}
}

func TestACorrectionKeepsWhatTheServerWasToldAboutTheSprint(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	if err := a.Tick(Input{Forward: 1, Sprint: true}); err != nil {
		t.Fatal(err)
	}
	a.Apply(&packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{3, eyeHeight, 3}, OnGround: true, Tick: 999})
	if err := a.Tick(Input{}); err != nil {
		t.Fatal(err)
	}
	ins := s.inputs(t)
	if _, _, _, stop := sprintFlags(ins[1]); !stop {
		t.Error("no stop sent after a correction wiped the pose")
	}
}

func TestWithoutSprintNoSprintFlagIsEverSet(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	for i := range 60 {
		if err := a.Tick(Input{Forward: float32(i%3) - 1, Yaw: float32(i), Jump: i%7 == 0}); err != nil {
			t.Fatal(err)
		}
	}
	for i, in := range s.inputs(t) {
		if d, on, st, sp := sprintFlags(in); d || on || st || sp {
			t.Fatalf("tick %d carries a sprint flag (%v/%v/%v/%v) though none was asked for", i, d, on, st, sp)
		}
	}
}

// The digest was taken from the actuator before sprinting existed, over a run
// that walks, backs up, turns, jumps and runs into a wall: an input that never
// sets Sprint must keep producing the very same packets.
func TestInputsWithoutSprintAreByteForByteWhatTheyWereBeforeSprinting(t *testing.T) {
	w := world{floor: -1, extra: map[protocol.BlockPos]bool{{0, 0, 9}: true, {0, 1, 9}: true}}
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	for i := range 400 {
		if err := a.Tick(Input{Forward: float32(i%5) - 1, Yaw: float32(i % 90), Jump: i%11 == 0}); err != nil {
			t.Fatal(err)
		}
	}
	h := sha256.New()
	for _, in := range s.inputs(t) {
		fmt.Fprintf(h, "%v|%v|%v|%v|%v\n", in.Position, in.Delta, in.InputData, in.MoveVector, in.Tick)
	}
	const want = "42d3a09f614a34c5eadea010c74afd1e7601706c0df6a42c747502b137c0d5bc"
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Errorf("packet digest = %s, want %s", got, want)
	}
}
