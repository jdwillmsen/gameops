package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// raise fills a box of the island with a block.
func (r *rig) raise(x0, y0, z0, x1, y1, z1 int32, block string) {
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				r.w.over[protocol.BlockPos{x, y, z}] = block
			}
		}
	}
}

// onShore says the unit stands dry on solid ground.
func (r *rig) onShore() bool {
	p := r.a.Pose()
	return !r.inWater() && p.OnGround
}

func (r *rig) eventFields(event string) map[string]any {
	for _, l := range r.log.lines {
		if l.event == event {
			return l.fields
		}
	}
	return nil
}

func TestABankAboveTheSurfaceIsNotChosenBecauseTheBodyCannotClimbIt(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{11.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	// The west bank stands one block over the surface; the east one is flush.
	r.raise(2, 1, 2, 9, 1, 27, "dirt")
	took := r.until(600, func() bool { return !r.inWater() })
	r.run(40)
	if took >= 600 || !r.onShore() || r.feet().X() < 10 {
		t.Fatalf("after %d ticks the unit is at %v, want it out on a flush bank, not stuck on the high west one", took, r.feet())
	}
}

func TestAShoreTheBodyCannotReachIsGivenUpForAnother(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{11.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	// What the unit knows says the west bank is flush ground; the physics has
	// a wall there that it cannot see, as when a block changed unannounced.
	solid := &world{size: r.w.size, over: map[protocol.BlockPos]string{}}
	for k, v := range r.w.over {
		solid.over[k] = v
	}
	for z := int32(2); z <= 27; z++ {
		for y := int32(1); y <= 4; y++ {
			solid.over[protocol.BlockPos{9, y, z}] = "stone"
		}
	}
	r.a = act.New(r.out, selfID, act.Pose{Feet: mgl32.Vec3{11.5, -0.9, 14.5}, OnGround: true}, path.NewSolid(solid))
	took := r.until(900, func() bool { return !r.inWater() })
	r.run(40)
	if took >= 900 || !r.onShore() || r.feet().X() < 10 {
		t.Fatalf("after %d ticks the unit is at %v, want it out on another bank than the walled west one", took, r.feet())
	}
	f := r.eventFields("swim_stuck")
	if f == nil || f["reason"] != "stalled" {
		t.Errorf("swim_stuck fields = %v, want a stalled shore reported", f)
	}
}

func TestACeilingOverTheNearestShoreStillLetsTheUnitOut(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{11.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	// Two blocks of headroom over the west bank, no more.
	r.raise(2, 3, 2, 9, 3, 27, "stone")
	took := r.until(900, func() bool { return !r.inWater() })
	r.run(40)
	if took >= 900 || !r.onShore() {
		t.Fatalf("after %d ticks the unit is at %v, want it out of the water", took, r.feet())
	}
}

func TestAOneWideChannelIsSwamOutOf(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{14.5, -0.9, 14.5})
	r.flood(14, 10, 14, 19)
	took := r.until(600, func() bool { return !r.inWater() })
	r.run(40)
	if took >= 600 || !r.onShore() {
		t.Fatalf("after %d ticks the unit is at %v, want it out of the channel", took, r.feet())
	}
}

func TestAPoolWithNoShoreReportsItIsStuckWithWhereAndHowLong(t *testing.T) {
	r := newRig(t, defaults, mgl32.Vec3{14.5, -0.9, 14.5})
	r.flood(10, 10, 19, 19)
	r.raise(9, 0, 9, 20, 3, 9, "stone")
	r.raise(9, 0, 20, 20, 3, 20, "stone")
	r.raise(9, 0, 9, 9, 3, 20, "stone")
	r.raise(20, 0, 9, 20, 3, 20, "stone")
	r.run(120)
	if !r.inWater() {
		t.Fatalf("unit left a walled pool at %v", r.feet())
	}
	f := r.eventFields("swim_stuck")
	if f == nil || f["reason"] != "no_shore" {
		t.Fatalf("swim_stuck fields = %v, want reason no_shore", f)
	}
	for _, k := range []string{"x", "y", "z", "ticks"} {
		if _, ok := f[k]; !ok {
			t.Errorf("swim_stuck lacks %q: %v", k, f)
		}
	}
	n := 0
	for _, l := range r.log.lines {
		if l.event == "swim_stuck" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("swim_stuck logged %d times in one streak, want once", n)
	}
}
