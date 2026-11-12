package skill

import (
	"errors"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestAtHome(t *testing.T) {
	home := protocol.BlockPos{10, 64, 10}
	cases := []struct {
		name   string
		feet   [3]float32
		within float64
		want   bool
	}{
		{"on the cell", [3]float32{10.5, 64, 10.5}, 2, true},
		{"a block off", [3]float32{11.5, 64, 10.5}, 2, true},
		{"too far across", [3]float32{13.5, 64, 10.5}, 2, false},
		{"diagonal past the radius", [3]float32{12.5, 64, 12.5}, 2, false},
		{"one level up", [3]float32{10.5, 65, 10.5}, 2, true},
		{"on another level", [3]float32{10.5, 70, 10.5}, 2, false},
		{"in a pit below", [3]float32{10.5, 55, 10.5}, 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AtHome(c.feet, home, c.within); got != c.want {
				t.Errorf("AtHome = %v, want %v", got, c.want)
			}
		})
	}
}

func TestGoHomeIsDoneAtHome(t *testing.T) {
	r := newRig(t, flatWorld(12), feetAt(5, 1, 5))
	st, err := r.run(NewGoHome(r.world, r.self, protocol.BlockPos{5, 1, 6}, 2), 5)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestGoHomeWalksFarAcrossLoadedGroundInHops(t *testing.T) {
	w := flatWorld(120)
	r := newRig(t, w, feetAt(2, 1, 2))
	home := protocol.BlockPos{100, 1, 90}
	st, err := r.run(NewGoHome(r.world, r.self, home, 2), 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, home, 3)
}

// A unit that spawned with little of the world loaded waits for the rest and
// carries on, instead of giving the trip up.
func TestGoHomeWaitsForChunksToLoad(t *testing.T) {
	w := flatWorld(40)
	r := newRig(t, w, feetAt(2, 1, 2))
	loaded := false
	r.onTick = func() {
		if r.ticks >= 250 && !loaded {
			loaded = true
			w.platform(0, 119, 0, 119)
		}
	}
	home := protocol.BlockPos{100, 1, 90}
	st, err := r.run(NewGoHome(r.world, r.self, home, 2), 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if !loaded {
		t.Fatal("the world never grew, so nothing was waited for")
	}
	near(t, r, home, 3)
}

func TestGoHomeGivesUpWhenNoGroundEverLoads(t *testing.T) {
	r := newRig(t, flatWorld(12), feetAt(2, 1, 2))
	st, err := r.run(NewGoHome(r.world, r.self, protocol.BlockPos{300, 1, 300}, 2), 3000)
	if st != Failed || !errors.Is(err, errHomeNoGround) {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestGoHomeRefusesOtherDimensions(t *testing.T) {
	r := newRig(t, flatWorld(12), feetAt(2, 1, 2))
	s := r.self()
	s.Dimension = 1
	if err := NewGoHome(r.world, r.self, protocol.BlockPos{5, 1, 5}, 2).Ready(s); err == nil {
		t.Error("ready in the nether")
	}
}

func TestFindBedPrefersTheNearestBed(t *testing.T) {
	w := flatWorld(20)
	w[protocol.BlockPos{10, 1, 10}] = "red_bed"
	w[protocol.BlockPos{13, 1, 10}] = "blue_bed"
	w[protocol.BlockPos{9, 1, 9}] = "bedrock"
	r := newRig(t, w, feetAt(1, 1, 1))
	got, ok := FindBed(r.world, protocol.BlockPos{12, 1, 10})
	if !ok || got != (protocol.BlockPos{13, 1, 10}) {
		t.Errorf("FindBed = %v, %v; want the blue bed", got, ok)
	}
	if _, ok := FindBed(r.world, protocol.BlockPos{1, 1, 1}); ok {
		t.Error("found a bed where there is none")
	}
	if _, ok := FindBed(r.world, protocol.BlockPos{-500, 1, -500}); ok {
		t.Error("found a bed in unloaded ground")
	}
}

// A ledge in the home column far from the home cell's level is not home, so
// the trip does not end there.
func TestGoHomeDoesNotEndOnALedgeAboveTheHomeCell(t *testing.T) {
	w := flatWorld(30)
	// The home cell is a hole in the floor column; the nearest ground to it is
	// a ledge six levels up.
	home := protocol.BlockPos{20, 1, 20}
	w[protocol.BlockPos{20, 0, 20}] = "air"
	w[protocol.BlockPos{20, 6, 20}] = "stone"
	w[protocol.BlockPos{20, 7, 20}] = "air"
	w[protocol.BlockPos{20, 8, 20}] = "air"
	r := newRig(t, w, feetAt(2, 1, 2))
	g := NewGoHome(r.world, r.self, home, 2).(*GoHome)
	goal, final, ok := g.nextHop(r.Pose())
	if !ok {
		t.Fatal("no hop at all")
	}
	if final {
		t.Errorf("the final hop is %v, %d levels over home", goal, goal.Y()-home.Y())
	}
}

func TestGoHomeNeverHopsToTheCellItStandsIn(t *testing.T) {
	r := newRig(t, flatWorld(30), feetAt(10, 1, 10))
	// Home is a diagonal step away and not at home yet (within 0.5 only), so a
	// short hop toward it rounds to the feet cell.
	g := NewGoHome(r.world, r.self, protocol.BlockPos{11, 1, 11}, 0.5).(*GoHome)
	for _, hf := range []int{0, 1, 2, 3, 4, 5} {
		g.hopFails = hf
		if goal, _, ok := g.nextHop(r.Pose()); ok && goal.X() == 10 && goal.Z() == 10 {
			t.Errorf("hopFails %d: hop to the feet cell %v", hf, goal)
		}
	}
}
