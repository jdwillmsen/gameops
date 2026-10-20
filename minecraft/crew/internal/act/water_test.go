package act

import (
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// lake is the lab's pool: a basin of water two blocks deep, x 0..9 and z 0..8,
// with its bottom's top face at y=0 and the ground around it flush with the
// water's block tops at y=2.
type lake struct{}

func (lake) Liquid(p protocol.BlockPos) bool {
	return p[0] >= 0 && p[0] <= 9 && p[2] >= 0 && p[2] <= 8 && p[1] >= 0 && p[1] <= 1
}
func (l lake) Solid(p protocol.BlockPos) bool { return p[1] <= 1 && !l.Liquid(p) }

// Positions measured on the lab server, 2026-10-19: the pool's bottom is at y=62
// and the unit rests on it, then holds the jump key. Heights are above the
// bottom, after the Nth tick.
var measuredSwimUp = map[int]float32{
	1: 0.0350, 2: 0.0980, 3: 0.1834, 4: 0.2867, 5: 0.4044, 6: 0.5335,
	7: 0.6718, 8: 0.8174, 9: 0.9690, 10: 1.1252, 11: 1.2851, 13: 1.6135,
	14: 1.7408, 15: 1.8376, 16: 1.9101, 17: 1.9631, 18: 1.9366, 19: 1.8323,
}

func onBottom() Pose { return Pose{Feet: mgl32.Vec3{4.5, 0, 4.5}, OnGround: true} }

func TestHoldingJumpOnThePoolFloorSwimsUpAsTheServerDoes(t *testing.T) {
	p := onBottom()
	for tick := 1; tick <= 19; tick++ {
		p, _ = p.advance(0, true, lake{})
		if want, ok := measuredSwimUp[tick]; ok {
			near(t, fmt.Sprintf("height after tick %d", tick), float64(p.Feet.Y()), float64(want), 1e-3)
		}
	}
}

func TestIdleInWaterSinksAtMostALittlePerTick(t *testing.T) {
	p := Pose{Feet: mgl32.Vec3{4.5, 1, 4.5}}
	for i := 0; i < 300; i++ {
		before := p.Feet.Y()
		p, _ = p.advance(0, false, lake{})
		if before-p.Feet.Y() > 0.05 {
			t.Fatalf("tick %d sank %.3f in one tick", i, before-p.Feet.Y())
		}
	}
	if !p.OnGround || p.Feet.Y() != 0 {
		t.Errorf("pose = %+v, want it resting on the bottom", p)
	}
}

func TestSwimmingSteersWithTheMeasuredWaterAcceleration(t *testing.T) {
	// Measured: eight jump ticks to leave the floor, then forward along +x.
	// After 2 and 8 forward ticks the unit had moved 0.0553 and 0.4665.
	p := onBottom()
	p.Yaw = -90
	for i := 0; i < 8; i++ {
		p, _ = p.advance(0, true, lake{})
	}
	x0 := p.Feet.X()
	for i := 1; i <= 8; i++ {
		p, _ = p.advance(1, false, lake{})
		switch i {
		case 2:
			near(t, "x after 2 forward ticks", float64(p.Feet.X()-x0), 0.0553, 2e-3)
		case 8:
			near(t, "x after 8 forward ticks", float64(p.Feet.X()-x0), 0.4665, 2e-3)
		}
	}
}

func TestWaterIsNotSolidToTheBody(t *testing.T) {
	// Dropped from above, the unit goes through the surface instead of landing on it.
	p := Pose{Feet: mgl32.Vec3{4.5, 4, 4.5}}
	deepest := p.Feet.Y()
	for i := 0; i < 80; i++ {
		p, _ = p.advance(0, false, lake{})
		deepest = min(deepest, p.Feet.Y())
	}
	if deepest != 0 {
		t.Errorf("lowest feet y = %.3f, want the pool bottom at 0", deepest)
	}
}

func TestSwimUpDoesNothingInShallowWater(t *testing.T) {
	// Below 0.4 of water over the feet a held jump gives no upward push.
	shallow := Pose{Feet: mgl32.Vec3{4.5, 1.6, 4.5}}
	held, _ := shallow.advance(0, true, lake{})
	idle, _ := shallow.advance(0, false, lake{})
	if held.Feet != idle.Feet || held.Rise() != idle.Rise() {
		t.Errorf("held %+v, idle %+v: a jump in shallow water should change nothing", held, idle)
	}
}

func TestSwimUpFlagsAreSentOnlyWhenSwimmingUp(t *testing.T) {
	a, s := newAct(lake{}, Pose{Feet: mgl32.Vec3{4.5, 0, 4.5}})
	_ = a.Tick(Input{Jump: true})
	_ = a.Tick(Input{})
	ins := s.inputs(t)
	for _, f := range []int{packet.InputFlagJumping, packet.InputFlagJumpDown, packet.InputFlagWantUp} {
		if !hasFlag(ins[0], f) || hasFlag(ins[1], f) {
			t.Errorf("flag %d: swimming up %v, idle %v, want true and false", f, hasFlag(ins[0], f), hasFlag(ins[1], f))
		}
	}
}

func TestPushingIntoABlockEdgeLevelWithTheSurfaceClimbsOut(t *testing.T) {
	p := Pose{Feet: mgl32.Vec3{9.5, 1.5, 4.5}, Yaw: -90}
	for i := 0; i < 30; i++ {
		p, _ = p.advance(1, false, lake{})
	}
	if p.Feet.Y() < 2 || p.Feet.X() < 9.9 {
		t.Errorf("pose = %+v, want the unit up on the bank", p)
	}
}

func TestAWallTooHighToClimbIsNotClimbed(t *testing.T) {
	// The bank stands 3 blocks over the surface here.
	w := walled{}
	p := Pose{Feet: mgl32.Vec3{9.5, 1.5, 4.5}, Yaw: -90}
	for i := 0; i < 60; i++ {
		p, _ = p.advance(1, false, w)
	}
	if p.Feet.Y() > 2.5 {
		t.Errorf("feet y = %.2f, want the unit held below a tall wall", p.Feet.Y())
	}
}

// walled is the lake with a bank 5 blocks high along x=10.
type walled struct{ lake }

func (w walled) Solid(p protocol.BlockPos) bool {
	return w.lake.Solid(p) || (p[0] >= 10 && p[1] <= 5)
}

func TestACorrectionInWaterTakesTheServersHorizontalDeltaAsTheDraggedCarry(t *testing.T) {
	// Measured: after a steady swim the server's delta (0.0783) was within
	// 1.5% of the unit's own post-drag carry (0.0795), and 21% under the step
	// it had just taken (0.0993). So the delta already includes the drag.
	a, s := newAct(lake{}, Pose{Feet: mgl32.Vec3{4.5, 1, 4.5}})
	a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: mgl32.Vec3{4.5, 1 + eyeHeight, 4.5},
		Delta:    mgl32.Vec3{0.0783, -0.0248, 0},
	})
	_ = a.Tick(Input{})
	near(t, "next step x", float64(s.inputs(t)[0].Delta.X()), 0.0783, 1e-5)
}

func TestACorrectionInWaterTakesTheServersStepAsTheNextOne(t *testing.T) {
	a, _ := newAct(lake{}, Pose{Feet: mgl32.Vec3{4.5, 0, 4.5}})
	a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: mgl32.Vec3{4.5, 0.6 + eyeHeight, 4.5},
		Delta:    mgl32.Vec3{0.01, 0.1, 0},
	})
	p := a.Pose()
	if p.Rise() != 0.1 {
		t.Errorf("rise = %v, want the server's 0.1 taken as is: drag and sinking are already in it", p.Rise())
	}
}

func TestWalkingOnTheBankIsTheOrdinaryWalk(t *testing.T) {
	bank := Pose{Feet: mgl32.Vec3{20.5, 2, 4.5}, Yaw: -90, OnGround: true}
	dry := Pose{Feet: mgl32.Vec3{20.5, 0, 4.5}, Yaw: -90, OnGround: true}
	for i := 0; i < 10; i++ {
		bank, _ = bank.advance(1, false, lake{})
		dry, _ = dry.advance(1, false, flat())
	}
	if bank.Feet.X() != dry.Feet.X() || bank.Feet.Y() != 2 {
		t.Errorf("bank %+v, dry %+v: a walk away from the water should not know about it", bank, dry)
	}
}

func TestWaterDepthIsMeasuredFromTheFeetToTheSurface(t *testing.T) {
	near(t, "depth at the bottom", float64(WaterDepth(lake{}, mgl32.Vec3{4.5, 0, 4.5})), 1+surfaceLevel, 1e-5)
	near(t, "depth at the top layer", float64(WaterDepth(lake{}, mgl32.Vec3{4.5, 1.5, 4.5})), 1+surfaceLevel-1.5, 1e-5)
	if d := WaterDepth(lake{}, mgl32.Vec3{4.5, 1 + surfaceLevel + 0.01, 4.5}); d != 0 {
		t.Errorf("depth above the surface = %v, want 0", d)
	}
	if d := WaterDepth(flat(), mgl32.Vec3{0, 0, 0}); d != 0 {
		t.Errorf("a world without water has depth %v", d)
	}
}
