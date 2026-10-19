package task

import (
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

type inputSink struct {
	mu     sync.Mutex
	inputs []*packet.PlayerAuthInput
}

func (s *inputSink) WritePacket(pk packet.Packet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := pk.(*packet.PlayerAuthInput); ok {
		s.inputs = append(s.inputs, in)
	}
	return nil
}

// floorAt is solid at and below its height, plus any extra blocks.
type floorAt struct {
	y     int32
	extra map[protocol.BlockPos]bool
}

func (f *floorAt) Solid(p protocol.BlockPos) bool { return p[1] <= f.y || f.extra[p] }

func TestIdleUnitFallsAndLandsWhenItsSupportIsRemoved(t *testing.T) {
	block := protocol.BlockPos{0, 5, 0}
	w := &floorAt{y: 0, extra: map[protocol.BlockPos]bool{block: true}}
	sink := &inputSink{}
	a := act.New(sink, 1, act.Pose{Feet: mgl32.Vec3{0.5, 6, 0.5}, OnGround: true}, w)
	h := newHarness(t, time.Minute, 1)
	h.r.SetActuator(a)

	h.tick(3)
	if y := a.Pose().Feet.Y(); y != 6 {
		t.Fatalf("feet y = %v on a block, want 6", y)
	}

	delete(w.extra, block)
	h.tick(2)
	if y := a.Pose().Feet.Y(); y >= 6 {
		t.Fatalf("feet y = %v after the block vanished, want it falling", y)
	}
	h.tick(60)
	if p := a.Pose(); p.Feet.Y() != 1 || !p.OnGround {
		t.Errorf("pose = %+v, want landed on the floor at y 1", p)
	}
	if n := uint64(len(sink.inputs)); n != 65 || a.Ticks() != n {
		t.Errorf("sent %d inputs, Ticks = %d, want 65", n, a.Ticks())
	}
	for i := 1; i < len(sink.inputs); i++ {
		if sink.inputs[i].Tick != sink.inputs[i-1].Tick+1 {
			t.Fatalf("tick %d after %d", sink.inputs[i].Tick, sink.inputs[i-1].Tick)
		}
	}
}

func TestIdleUnitSendsOneInputPerStep(t *testing.T) {
	h := newHarness(t, time.Minute, 1)
	h.tick(7)
	if h.act.ticks != 7 {
		t.Errorf("ticks = %d, want 7", h.act.ticks)
	}
}

func TestRunnerSendsOneInputPerStepWhateverTheSkillDoes(t *testing.T) {
	h := newHarness(t, time.Minute, 100)
	h.r.newSkills = func(Template) ([]skill.Skill, error) {
		return []skill.Skill{&fakeSkill{name: "mine_block", steps: 100, ticks: true}}, nil
	}
	first, err := h.r.Submit(tpl())
	if err != nil {
		t.Fatal(err)
	}
	h.tick(10)
	if h.act.ticks != 10 {
		t.Errorf("ticks = %d with a ticking skill, want 10", h.act.ticks)
	}

	// A skill that waits without ticking still gets its step's input.
	h.r.newSkills = func(Template) ([]skill.Skill, error) {
		return []skill.Skill{&fakeSkill{name: "deposit_chest", steps: 100}}, nil
	}
	if _, err := h.r.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.r.Submit(tpl()); err != nil {
		t.Fatal(err)
	}
	before := h.act.ticks
	h.tick(10)
	if got := h.act.ticks - before; got != 10 {
		t.Errorf("ticks = %d with a waiting skill, want 10", got)
	}
}

func TestRunnerSendsNoInputWhileDeadOrUnspawned(t *testing.T) {
	h := newHarness(t, time.Minute, 1)
	h.snap.Dead = true
	h.tick(3)
	h.snap.Dead, h.snap.Spawned = false, false
	h.tick(3)
	if h.act.ticks != 0 {
		t.Errorf("ticks = %d, want none", h.act.ticks)
	}
	h.snap.Spawned = true
	h.tick(1)
	if h.act.ticks != 1 {
		t.Errorf("ticks = %d after respawn, want 1", h.act.ticks)
	}
}
