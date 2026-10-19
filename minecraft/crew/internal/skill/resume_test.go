package skill

import (
	"context"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
)

// interrupt steps a skill n times, then does what the runner does when the
// unit must fight: stop the skill, let a few ticks pass with the unit pushed
// aside, and resume it.
func interrupt(t *testing.T, r *rig, s Skill, n int) (done bool) {
	t.Helper()
	for i := 0; i < n; i++ {
		st, err := s.Step(context.Background(), r)
		if st == Done {
			return true
		}
		if st == Failed {
			t.Fatalf("failed before the interruption: %v", err)
		}
	}
	if st, ok := s.(Stopper); ok {
		before := r.ticks
		st.Stop(r)
		if r.ticks-before > 1 {
			t.Fatalf("Stop sent %d inputs", r.ticks-before)
		}
	}
	p := r.Pose()
	r.send(&packet.CorrectPlayerMovePrediction{Position: p.Feet.Add(mgl32.Vec3{0.8, 1.62, 0.4}), OnGround: true})
	for i := 0; i < 3; i++ {
		if err := r.Tick(actIdle(r)); err != nil {
			t.Fatal(err)
		}
	}
	s.(Resumer).Resume()
	return false
}

func TestMineResumesAfterBeingInterruptedAtAnyPoint(t *testing.T) {
	for _, n := range []int{1, 15, 40, 90, 140, 200} {
		w := quarry()
		r := withPickaxe(newRig(t, w, feetAt(2, 1, 2)))
		m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 8, 0)
		interrupt(t, r, m, n)
		st, err := r.run(m, 2000)
		if st != Done || err != nil {
			t.Fatalf("interrupted at %d: status %v, err %v", n, st, err)
		}
		if got := r.have("cobblestone"); got != 8 {
			t.Errorf("interrupted at %d: cobblestone = %d, want 8", n, got)
		}
	}
}

func TestMineResumeReselectsTheToolAFightChanged(t *testing.T) {
	w := quarry()
	r := withPickaxe(newRig(t, w, feetAt(9, 3, 8)))
	m := NewMineBlock(w, r.self, "stone", "cobblestone", "pickaxe", 2, 0)
	for i := 0; i < 5; i++ {
		if _, err := m.Step(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	r.held = 7 // the fight put another stack in hand
	m.(Resumer).Resume()
	for i := 0; i < 5; i++ {
		if _, err := m.Step(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	if r.held != 0 {
		t.Errorf("held slot = %d, want the pickaxe back in 0", r.held)
	}
}

func TestDepositResumesAfterBeingInterruptedAtAnyPoint(t *testing.T) {
	for _, n := range []int{1, 20, 45, 60, 70, 80, 95} {
		w := chestWorld()
		r := depositRig(t, w, 1, 1)
		d := NewDepositChest(w, r.self, chestPos, "cobblestone", 16)
		if interrupt(t, r, d, n) {
			continue
		}
		st, err := r.run(d, 800)
		if st != Done || err != nil {
			t.Fatalf("interrupted at %d: status %v, err %v", n, st, err)
		}
		if got := r.stored(chestPos, idCobblestone); got != 16 {
			t.Errorf("interrupted at %d: stored %d, want 16", n, got)
		}
		if open, _, _ := r.Container(); open || !r.chestClosed {
			t.Errorf("interrupted at %d: the chest was left open", n)
		}
	}
}

func TestHaulResumesAfterBeingInterruptedAtAnyPoint(t *testing.T) {
	for _, n := range []int{5, 40, 80, 120, 160, 220} {
		w := haulWorld()
		r := haulRig(t, w)
		r.chestFor(srcA)[0] = slotStack{idCobblestone, 16, 700}
		h := NewHaul(w, r.self, []protocol.BlockPos{srcA}, storage, map[string]int{"cobblestone": 16})
		if interrupt(t, r, h, n) {
			continue
		}
		st, err := r.run(h, 4000)
		if st != Done || err != nil {
			t.Fatalf("interrupted at %d: status %v, err %v", n, st, err)
		}
		if got := r.stored(storage, idCobblestone); got != 16 {
			t.Errorf("interrupted at %d: stored %d, want 16", n, got)
		}
	}
}

func TestNavigateResumesToTheSameGoal(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 1))
	goal := protocol.BlockPos{10, 1, 8}
	nav := NewNavigate(w, r.self, goal, 0.8)
	interrupt(t, r, nav, 25)
	st, err := r.run(nav, 400)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	near(t, r, goal, 0.8)
}

func actIdle(r *rig) act.Input {
	p := r.Pose()
	return act.Input{Yaw: p.Yaw, Pitch: p.Pitch}
}
