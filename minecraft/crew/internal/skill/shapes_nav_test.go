package skill

import (
	"context"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// statedWorld is a simWorld whose blocks can carry states, which the
// collision shapes read.
type statedWorld struct {
	simWorld
	props map[protocol.BlockPos]map[string]any
}

func newStated(size int32) statedWorld {
	return statedWorld{simWorld: flatWorld(size), props: map[protocol.BlockPos]map[string]any{}}
}

func (w statedWorld) State(p protocol.BlockPos) (string, map[string]any, bool) {
	n, ok := w.simWorld[p]
	return n, w.props[p], ok
}

func (w statedWorld) put(x, y, z int32, name string, props map[string]any) {
	p := protocol.BlockPos{x, y, z}
	w.simWorld[p] = name
	if props != nil {
		w.props[p] = props
	}
}

// rigOn is newRig for a world with states: the unit predicts against them.
func rigOn(t *testing.T, w statedWorld, feet mgl32.Vec3) *rig {
	r := newRig(t, w.simWorld, feet)
	r.Actuator = act.New(r, 1, act.Pose{Feet: feet, OnGround: true}, path.NewSolid(w))
	return r
}

// A line of connected fences with one gate in it: a closed gate stops the
// walk, an open one lets it through.
func TestNavigateThroughAFenceLineNeedsTheGateOpen(t *testing.T) {
	for _, open := range []bool{false, true} {
		w := newStated(12)
		for x := int32(0); x < 12; x++ {
			conn := map[string]any{"minecraft:connection_east": x < 11, "minecraft:connection_west": x > 0}
			w.put(x, 1, 6, "oak_fence", conn)
		}
		w.put(5, 1, 6, "fence_gate", map[string]any{"minecraft:cardinal_direction": "north", "open_bit": open})
		r := rigOn(t, w, feetAt(5, 1, 2))
		goal := protocol.BlockPos{5, 1, 9}
		st, err := r.run(NewNavigate(w, r.self, goal, 0.8), 600)
		switch {
		case open && (st != Done || err != nil):
			t.Errorf("open gate: status %v, err %v", st, err)
		case !open && st != Failed:
			t.Errorf("closed gate: status %v, want the walk refused", st)
		}
		if !open && r.jumps > 0 {
			t.Errorf("closed gate: jumped %d times at a fence", r.jumps)
		}
	}
}

// A carpet on the threshold of a doorway two high: the unit steps onto it
// under the lintel and walks on.
func TestNavigateStepsOntoADoormatUnderALintel(t *testing.T) {
	w := newStated(12)
	for x := int32(0); x < 12; x++ {
		for y := int32(1); y <= 3; y++ {
			w.put(x, y, 6, "stone", nil)
		}
	}
	w.put(5, 1, 6, "white_carpet", nil)
	w.put(5, 2, 6, "air", nil)
	w.put(5, 3, 6, "air", nil)
	// The lintel is the stone at y=3 above the doorway row, replacing the
	// air that would be the third block of clearance.
	w.put(5, 3, 6, "stone", nil)
	r := rigOn(t, w, feetAt(5, 1, 2))
	goal := protocol.BlockPos{5, 1, 9}
	st, err := r.run(NewNavigate(w, r.self, goal, 0.8), 400)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.jumps != 0 {
		t.Errorf("%d jumps through a doorway with a doormat", r.jumps)
	}
}

// A slab tucked under a lintel leaves too little room to stand on, and the
// walk must be refused rather than tried.
func TestNavigateRefusesALedgeThatLeavesNoHeadroom(t *testing.T) {
	w := newStated(12)
	for x := int32(0); x < 12; x++ {
		for y := int32(1); y <= 3; y++ {
			w.put(x, y, 6, "stone", nil)
		}
	}
	w.put(5, 1, 6, "stone_slab", nil)
	w.put(5, 2, 6, "air", nil)
	w.put(5, 3, 6, "stone", nil)
	r := rigOn(t, w, feetAt(5, 1, 2))
	st, _ := r.run(NewNavigate(w, r.self, protocol.BlockPos{5, 1, 9}, 0.8), 400)
	if st != Failed {
		t.Errorf("status %v, want the walk refused", st)
	}
}

// walk steps a skill like rig.run, but reports a skill still running as
// Running instead of ending the test.
func walk(r *rig, s Skill, maxTicks int) (Status, error) {
	for i := 0; i < maxTicks; i++ {
		st, err := s.Step(context.Background(), r)
		if st != Running {
			return st, err
		}
	}
	return Running, nil
}

// fuzzWorld lays slabs, stairs, fences, carpets, snow and low ceilings on a
// flat floor at random.
func fuzzWorld(rng *rand.Rand) statedWorld {
	const size = 14
	w := newStated(size)
	for x := int32(0); x < size; x++ {
		for z := int32(0); z < size; z++ {
			if rng.Intn(100) >= 22 {
				continue
			}
			switch rng.Intn(8) {
			case 0:
				w.put(x, 1, z, "stone_slab", map[string]any{"minecraft:vertical_half": "bottom"})
			case 1:
				w.put(x, 1, z, "stone_slab", map[string]any{"minecraft:vertical_half": "top"})
			case 2:
				w.put(x, 1, z, "oak_stairs", map[string]any{"weirdo_direction": int32(rng.Intn(4)), "upside_down_bit": rng.Intn(4) == 0, "minecraft:corner": "none"})
			case 3:
				w.put(x, 1, z, "oak_fence", nil)
			case 4:
				w.put(x, 1, z, "white_carpet", nil)
			case 5:
				w.put(x, 1, z, "snow_layer", map[string]any{"height": int32(1 + rng.Intn(5))})
			case 6:
				w.put(x, 3, z, "stone", nil)
			case 7:
				w.put(x, 1, z, "stone", nil)
			}
		}
	}
	// Fences join their neighbours, as the server would have set them.
	for p, n := range w.simWorld {
		if n != "oak_fence" {
			continue
		}
		conn := map[string]any{}
		for key, d := range map[string][2]int32{
			"minecraft:connection_east": {1, 0}, "minecraft:connection_west": {-1, 0},
			"minecraft:connection_south": {0, 1}, "minecraft:connection_north": {0, -1},
		} {
			q := protocol.BlockPos{p.X() + d[0], p.Y(), p.Z() + d[1]}
			conn[key] = w.simWorld[q] == "oak_fence" || w.simWorld[q] == "stone"
		}
		w.props[p] = conn
	}
	return w
}

// The planner and the actuator must agree: a goal the planner calls reachable
// is one the unit, walking its plan against the same shapes, arrives at.
func TestNavigateReachesWhatThePlannerCallsReachable(t *testing.T) {
	worlds := 100
	if n, err := strconv.Atoi(os.Getenv("FUZZ_WORLDS")); err == nil {
		worlds = n
	}
	failures := 0
	for seed := int64(0); seed < int64(worlds); seed++ {
		rng := rand.New(rand.NewSource(seed))
		w := fuzzWorld(rng)
		var start, goal protocol.BlockPos
		var plan []protocol.BlockPos
		found := false
		for try := 0; try < 40 && !found; try++ {
			start = protocol.BlockPos{int32(rng.Intn(14)), 1, int32(rng.Intn(14))}
			goal = protocol.BlockPos{int32(rng.Intn(14)), 1, int32(rng.Intn(14))}
			if _, ok := path.Footing(w, start); !ok {
				continue
			}
			if _, ok := path.Footing(w, goal); !ok || start == goal {
				continue
			}
			var err error
			if plan, err = path.Find(w, start, goal, 0); err == nil {
				found = true
			}
		}
		if !found {
			continue
		}
		y, _ := path.Footing(w, start)
		r := rigOn(t, w, mgl32.Vec3{float32(start.X()) + 0.5, y, float32(start.Z()) + 0.5})
		st, err := walk(r, NewNavigate(w, r.self, goal, 0.8), 900)
		if st != Done || err != nil {
			failures++
			t.Errorf("seed %d: %v to %v (%d steps) ended %v, err %v at %v", seed, start, goal, len(plan), st, err, r.Pose().Feet)
		}
	}
	if failures > 0 {
		t.Logf("%d of %d worlds failed", failures, worlds)
	}
}
