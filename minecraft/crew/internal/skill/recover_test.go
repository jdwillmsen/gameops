package skill

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// ground is a drop source whose items the unit picks up the moment it stands
// within pickup range of them, as the server does.
type ground struct {
	r     *rig
	items []belief.Dropped
	next  int
	// keep leaves items where they lie, as when the server never picks them up.
	keep bool
}

func (g *ground) drop(at mgl32.Vec3, item int32, count int) {
	g.items = append(g.items, belief.Dropped{ID: uint64(len(g.items) + 10), Item: item, Count: count, Pos: at})
}

func (g *ground) DroppedNear(pos mgl32.Vec3, radius float64) []belief.Dropped {
	feet := g.r.Pose().Feet
	var rest, out []belief.Dropped
	for _, d := range g.items {
		if !g.keep && math.Hypot(float64(feet.X()-d.Pos.X()), float64(feet.Z()-d.Pos.Z())) <= 1.5 && math.Abs(float64(feet.Y()-d.Pos.Y())) <= 1.5 {
			g.r.give(10+g.next, d.Item, d.Count)
			g.next++
			continue
		}
		rest = append(rest, d)
		if math.Hypot(float64(d.Pos.X()-pos.X()), float64(d.Pos.Z()-pos.Z())) <= radius {
			out = append(out, d)
		}
	}
	g.items = rest
	return out
}

func recoverRig(t *testing.T) (*rig, simWorld, *ground) {
	t.Helper()
	w := flatWorld(40)
	r := newRig(t, w, feetAt(2, 1, 2))
	r.send(&packet.MovePlayer{EntityRuntimeID: 1, Position: feetAt(2, 1, 2).Add(mgl32.Vec3{0, 1.62, 0})})
	return r, w, &ground{r: r}
}

func TestRecoverWalksBackAndCollectsTheDrops(t *testing.T) {
	r, w, g := recoverRig(t)
	g.drop(feetAt(22, 1, 19), idOakLog, 3)
	g.drop(feetAt(18, 1, 21), idCoal, 2)
	s := NewRecover(w, r.self, g, feetAt(20, 1, 20), 0, 5)
	if err := s.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(s, 1500)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("oak_log") != 3 || r.have("coal") != 2 {
		t.Errorf("recovered %d logs and %d coal, want 3 and 2", r.have("oak_log"), r.have("coal"))
	}
	if len(g.items) != 0 {
		t.Errorf("%d items left lying", len(g.items))
	}
}

func TestRecoverEndsWhenNoItemIsInView(t *testing.T) {
	r, w, g := recoverRig(t)
	s := NewRecover(w, r.self, g, feetAt(10, 1, 10), 0, 4)
	st, err := r.run(s, 1500)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, protocol.BlockPos{10, 1, 10}, 1.6)
}

func TestRecoverLeavesAnItemItCannotReach(t *testing.T) {
	r, w, g := recoverRig(t)
	// A sealed stone box the item lies in.
	w.fill("stone", 24, 1, 14, 26, 3, 16)
	g.drop(feetAt(25, 1, 15), idOakLog, 1)
	g.drop(feetAt(18, 1, 20), idCoal, 1)
	s := NewRecover(w, r.self, g, feetAt(20, 1, 20), 0, 0)
	st, err := r.run(s, 2000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("coal") != 1 {
		t.Error("the reachable item was not picked up after the unreachable one was left")
	}
}

func TestRecoverSweepIsBoundedWhenItemsStayDown(t *testing.T) {
	r, w, g := recoverRig(t)
	g.keep = true
	g.drop(feetAt(21, 1, 20), idOakLog, 1)
	s := NewRecover(w, r.self, g, feetAt(20, 1, 20), 0, 0)
	st, err := r.run(s, recoverGiveUpTicks)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := s.(*Recover).ticks; got > 200+recoverSweepTicks+recoverItemTicks {
		t.Errorf("swept for %d ticks", got)
	}
}

func TestRecoverGivesUpAfterFiveMinutes(t *testing.T) {
	r, w, g := recoverRig(t)
	s := NewRecover(w, r.self, g, feetAt(20, 1, 20), 0, 1)
	s.(*Recover).ticks = recoverGiveUpTicks
	if _, err := s.Step(context.Background(), r); !errors.Is(err, ErrRecoverGaveUp) {
		t.Fatalf("err = %v", err)
	}
}

func TestRecoverIsReadyOnlyWhereTheDropsCanBeReached(t *testing.T) {
	r, w, g := recoverRig(t)
	w[protocol.BlockPos{30, 0, 30}] = "lava"
	w[protocol.BlockPos{31, 1, 31}] = "fire"
	cases := []struct {
		name string
		at   mgl32.Vec3
		dim  int32
		want error
	}{
		{"fine", feetAt(20, 1, 20), 0, nil},
		{"far", feetAt(2+100, 1, 2), 0, ErrRecoverTooFar},
		{"other dimension", feetAt(20, 1, 20), 1, ErrRecoverDimension},
		{"void", feetAt(20, -70, 20), 0, ErrRecoverHazard},
		{"lava", feetAt(30, 1, 30), 0, ErrRecoverHazard},
		{"fire", feetAt(31, 1, 31), 0, ErrRecoverHazard},
	}
	for _, c := range cases {
		err := NewRecover(w, r.self, g, c.at, c.dim, 1).Ready(r.self())
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	// A column the world does not know is not walked to.
	delete(w, protocol.BlockPos{20, 0, 20})
	if err := NewRecover(w, r.self, g, feetAt(20, 1, 20), 0, 1).Ready(r.self()); !errors.Is(err, ErrRecoverNotLoaded) {
		t.Errorf("unloaded: err = %v", err)
	}
}

func TestRecoverResumesAfterAFight(t *testing.T) {
	r, w, g := recoverRig(t)
	g.drop(feetAt(22, 1, 22), idOakLog, 2)
	s := NewRecover(w, r.self, g, feetAt(20, 1, 20), 0, 2)
	if interrupt(t, r, s, 30) {
		t.Fatal("finished before the interruption")
	}
	st, err := r.run(s, 1500)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.have("oak_log") != 2 {
		t.Errorf("logs = %d, want 2", r.have("oak_log"))
	}
}
