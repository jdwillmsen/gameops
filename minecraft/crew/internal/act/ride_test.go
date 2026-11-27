package act

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// seat stands for belief's ride state.
type seat struct{ id int64 }

func (s *seat) vehicle() int64 { return s.id }

func TestSeatedTickHoldsTheServersPositionAndTurnsTheVehicle(t *testing.T) {
	// Over a gap: the walking model would have the unit fall.
	a, s := newAct(world{floor: -50}, Pose{Feet: mgl32.Vec3{3, 10, 4}})
	st := &seat{id: 42}
	a.SetVehicle(st.vehicle)
	for i := 0; i < 3; i++ {
		if err := a.Tick(Input{Yaw: 90, Pitch: 5}); err != nil {
			t.Fatal(err)
		}
	}
	for i, in := range s.inputs(t) {
		if in.Position != (mgl32.Vec3{3, 10 + eyeHeight, 4}) || in.Delta != (mgl32.Vec3{}) {
			t.Errorf("input %d claimed %v moving %v, want the seat", i, in.Position, in.Delta)
		}
		if !hasFlag(in, packet.InputFlagClientPredictedVehicle) || hasFlag(in, packet.InputFlagVerticalCollision) {
			t.Errorf("input %d: vehicle flag %v, collision flag %v", i, hasFlag(in, packet.InputFlagClientPredictedVehicle), hasFlag(in, packet.InputFlagVerticalCollision))
		}
		if v, ok := in.ClientPredictedVehicle.Value(); !ok || v != 42 {
			t.Errorf("input %d vehicle = %v, %v", i, v, ok)
		}
		if r, ok := in.VehicleRotation.Value(); !ok || r != (mgl32.Vec2{5, 90}) {
			t.Errorf("input %d vehicle rotation = %v, %v", i, r, ok)
		}
		if in.Tick != firstTick+uint64(i) {
			t.Errorf("input %d tick = %d", i, in.Tick)
		}
	}
	if a.Riding() != 42 {
		t.Errorf("riding = %d, want 42", a.Riding())
	}
}

func TestStandingTickNamesNoVehicle(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	a.SetVehicle((&seat{}).vehicle)
	_ = a.Tick(Input{})
	in := s.inputs(t)[0]
	if hasFlag(in, packet.InputFlagClientPredictedVehicle) || !hasFlag(in, packet.InputFlagVerticalCollision) {
		t.Error("a standing unit reported a vehicle or no ground")
	}
	if _, ok := in.ClientPredictedVehicle.Value(); ok {
		t.Error("a standing unit named a vehicle")
	}
	if _, ok := in.VehicleRotation.Value(); ok {
		t.Error("a standing unit sent a vehicle rotation")
	}
}

func TestMountAndDismountDropTheCarriedVelocity(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	st := &seat{}
	a.SetVehicle(st.vehicle)
	for i := 0; i < 5; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	a.Apply(&packet.SetActorMotion{EntityRuntimeID: 7, Velocity: mgl32.Vec3{0.4, 0.3, 0.4}})
	st.id = 42
	_ = a.Tick(Input{})
	if p := a.Pose(); p.Carry != (mgl32.Vec2{}) || p.Rise() != 0 {
		t.Errorf("mounted with carry %v rise %v", p.Carry, p.Rise())
	}
	a.Apply(&packet.SetActorMotion{EntityRuntimeID: 7, Velocity: mgl32.Vec3{0.4, 0, 0.4}})
	st.id = 0
	_ = a.Tick(Input{})
	in := s.inputs(t)
	if d := in[len(in)-1].Delta; d.X() != 0 || d.Z() != 0 {
		t.Errorf("first step off the seat moved %v, want no carry", d)
	}
	if a.Riding() != 0 {
		t.Errorf("riding = %d after dismount", a.Riding())
	}
}
