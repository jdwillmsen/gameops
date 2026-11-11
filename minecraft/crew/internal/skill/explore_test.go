package skill

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// windowWorld shows only the blocks near the unit, as a server that streams
// chunks around the player does; the rig's own world stays whole.
type windowWorld struct {
	simWorld
	r     *rig
	reach int32
}

func (w windowWorld) Block(p protocol.BlockPos) (string, bool) {
	f := w.r.Pose().Feet
	if abs32(p.X()-int32(f.X())) > w.reach || abs32(p.Z()-int32(f.Z())) > w.reach {
		return "", false
	}
	return w.simWorld.Block(p)
}

func exploreRig(t *testing.T, w simWorld, x, y, z float32) (*rig, windowWorld) {
	r := newRig(t, w, feetAt(x, y, z))
	return r, windowWorld{simWorld: w, r: r, reach: 56}
}

func fromTree(r *rig, tree protocol.BlockPos) float64 {
	f := r.Pose().Feet
	dx, dz := float64(f.X())-float64(tree.X())-0.5, float64(f.Z())-float64(tree.Z())-0.5
	return dx*dx + dz*dz
}

func TestExploreFindsATreeBeyondTheLoadedChunks(t *testing.T) {
	w := simWorld{}
	w.platform(0, 170, 0, 41)
	w.fill("oak_log", 110, 1, 20, 110, 5, 20)
	r, ww := exploreRig(t, w, 5, 1, 20)
	box := Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{170, 20, 41}}
	e := NewExplore(ww, r.self, "oak_log", box, nil)
	if _, known := ww.Block(protocol.BlockPos{110, 1, 20}); known {
		t.Fatal("the tree is loaded at the start; the test would prove nothing")
	}
	st, err := r.run(e, 30000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if d := fromTree(r, protocol.BlockPos{110, 1, 20}); d > 16*16 {
		t.Errorf("stopped %.0f blocks from the tree", d)
	}
}

func TestExploreSkipsWaypointsWithNoLoadedGround(t *testing.T) {
	w := simWorld{}
	// Nothing south of z=20: those waypoints have no ground to stand on.
	for x := int32(0); x <= 170; x++ {
		for z := int32(20); z <= 41; z++ {
			w[protocol.BlockPos{x, 0, z}] = "dirt"
			for y := int32(1); y <= 6; y++ {
				w[protocol.BlockPos{x, y, z}] = "air"
			}
		}
	}
	w.fill("oak_log", 120, 1, 30, 120, 5, 30)
	r, ww := exploreRig(t, w, 5, 1, 30)
	box := Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{170, 20, 41}}
	st, err := r.run(NewExplore(ww, r.self, "oak_log", box, nil), 30000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestExploreFailsCleanlyWhenNoneExists(t *testing.T) {
	w := simWorld{}
	w.platform(0, 90, 0, 41)
	r, ww := exploreRig(t, w, 5, 1, 20)
	box := Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{90, 20, 41}}
	st, err := r.run(NewExplore(ww, r.self, "oak_log", box, nil), 30000)
	if st != Failed || err == nil || err.Error() != "no oak_log found in search area" {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestExploreDoneAtOnceWhenTheBlockIsNear(t *testing.T) {
	w := flatWorld(30)
	w.fill("oak_log", 8, 1, 8, 8, 4, 8)
	r, ww := exploreRig(t, w, 3, 1, 3)
	box := Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{29, 20, 29}}
	st, err := r.run(NewExplore(ww, r.self, "oak_log", box, nil), 100)
	if st != Done || err != nil || r.ticks != 0 {
		t.Fatalf("status %v, err %v, %d ticks", st, err, r.ticks)
	}
}

// A sighting outside the area the unit is allowed to cut is not a find.
func TestExploreIgnoresSightingsOutsideTheAcceptedArea(t *testing.T) {
	w := flatWorld(60)
	w.fill("oak_log", 50, 1, 50, 50, 4, 50)
	r, ww := exploreRig(t, w, 3, 1, 3)
	box := Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{59, 20, 59}}
	accept := Area{Min: protocol.BlockPos{0, 0, 0}, Max: protocol.BlockPos{20, 20, 20}}
	st, err := r.run(NewExplore(ww, r.self, "oak_log", box, &accept), 30000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "found in search area") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestExploreWaypointsStayInsideTheBoxAndTheLab(t *testing.T) {
	cases := map[string]struct {
		box    Area
		origin protocol.BlockPos
		want   int
	}{
		"inside":     {Area{Min: protocol.BlockPos{-100, 0, -100}, Max: protocol.BlockPos{100, 20, 100}}, protocol.BlockPos{0, 64, 0}, 7 * 7},
		"past lab":   {Area{Min: protocol.BlockPos{300, 0, 300}, Max: protocol.BlockPos{700, 20, 700}}, protocol.BlockPos{310, 64, 310}, 6 * 6},
		"past -lab":  {Area{Min: protocol.BlockPos{-900, 0, -900}, Max: protocol.BlockPos{-300, 20, -300}}, protocol.BlockPos{-490, 64, -490}, 6 * 6},
		"one column": {Area{Min: protocol.BlockPos{5, 0, 5}, Max: protocol.BlockPos{5, 20, 5}}, protocol.BlockPos{5, 64, 5}, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := NewExplore(nil, nil, "oak_log", c.box, nil).(*Explore)
			e.origin = c.origin
			pts := e.grid()
			for _, p := range pts {
				if p.cell.X() < e.box.Min.X() || p.cell.X() > e.box.Max.X() || p.cell.Z() < e.box.Min.Z() || p.cell.Z() > e.box.Max.Z() ||
					p.cell.X() < -500 || p.cell.X() > 499 || p.cell.Z() < -500 || p.cell.Z() > 499 {
					t.Fatalf("waypoint %v is outside the box %v", p.cell, e.box)
				}
			}
			if len(pts) != c.want {
				t.Errorf("%d waypoints, want %d", len(pts), c.want)
			}
		})
	}
}
