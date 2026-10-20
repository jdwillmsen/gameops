package life

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
)

// flood fills a rectangle of the island two blocks deep with water, so its
// surface is level with the grass around it, as the lab's pool is.
func (r *rig) flood(x0, z0, x1, z1 int32) {
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			r.w.over[protocol.BlockPos{x, 0, z}] = "water"
			r.w.over[protocol.BlockPos{x, -1, z}] = "water"
		}
	}
}

func (r *rig) inWater() bool { return act.WaterDepth(r.l.body, r.a.Pose().Feet) > 0 }

// until steps the rig until stop says so, up to max ticks, and returns how many it took.
func (r *rig) until(max int, stop func() bool) int {
	r.t.Helper()
	for i := 0; i < max; i++ {
		if stop() {
			return i
		}
		r.step()
	}
	return max
}

func TestAUnitInTheMiddleOfAPoolSwimsToTheShoreAndStaysOut(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{14.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	if !r.inWater() {
		t.Fatal("the unit did not start in the water")
	}
	took := r.until(200, func() bool { return !r.inWater() })
	if took >= 200 {
		t.Fatalf("still in the water after %d ticks at %v", took, r.feet())
	}
	r.run(40)
	f := r.feet()
	if r.inWater() || f.Y() < 1-0.01 {
		t.Errorf("unit ended at %v, want it standing on the shore", f)
	}
	if !r.logged("swim_started") || !r.logged("swim_ended") {
		t.Errorf("log = %v, want swim_started and swim_ended", r.log.lines)
	}
}

func TestAUnitOnThePoolFloorRisesAndDoesNotWalkAlongTheBottom(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{14.5, -1, 14.5})
	r.flood(10, 10, 19, 19)
	maxY := float32(-1)
	for i := 0; i < 30; i++ {
		r.step()
		maxY = max(maxY, r.feet().Y())
	}
	if maxY < 0 {
		t.Errorf("highest feet y = %.2f in 30 ticks, want the unit to rise towards the surface", maxY)
	}
}

func TestTheUnitTakesTheNearestShore(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{18.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	r.until(200, func() bool { return !r.inWater() })
	r.run(40)
	// Standing on the very edge of the shore counts: the body only has to rest on it.
	if f := r.feet(); r.inWater() || f.Y() < 0.99 || f.X() < 19.5 {
		t.Errorf("ended at %v, want out on the east shore, 1 block away", f)
	}
}

func TestTheUnitKeepsMovingAlongTheWaterInsteadOfTreadingOnTheSpot(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{14.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	last, still := r.feet(), 0
	for i := 0; i < 200 && r.inWater(); i++ {
		r.step()
		now := r.feet()
		if math.Abs(float64(now.X()-last.X()))+math.Abs(float64(now.Z()-last.Z())) < 1e-3 {
			still++
		} else {
			still = 0
		}
		if still > 10 {
			t.Fatalf("no horizontal progress for %d ticks at %v: the unit is treading water", still, now)
		}
		last = now
	}
}

func TestADryUnitIsNotSwimmingAndLogsNothingAboutIt(t *testing.T) {
	r := newRig(t, defaults, start)
	r.flood(10, 10, 19, 19)
	r.run(100)
	if r.l.Swimming(r.a.Pose()) || r.logged("swim_started") {
		t.Error("a unit on dry land was treated as swimming")
	}
}

func TestSwimmingSendsExactlyOneInputPerTick(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{14.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	// step itself fails the test when a tick sends other than one input.
	r.run(80)
}
