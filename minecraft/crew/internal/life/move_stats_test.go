package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
)

func TestFightStatsCarryTheMovementCounters(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	c := r.l.MoveCounters()
	r.a.CountMovement(c)
	for range 4 {
		if err := r.a.Tick(act.Input{Forward: 1, Sprint: true}); err != nil {
			t.Fatal(err)
		}
	}
	r.a.Apply(&packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{9, 9, 9}, OnGround: true})

	s, _ := r.l.Stats()
	if s.Total.SprintTicks != 4 || s.Total.MovementCorrections != 1 {
		t.Fatalf("total = %+v, want 4 sprint ticks and 1 correction", s.Total)
	}
	if s.Current.SprintTicks != 4 || s.Current.MovementCorrections != 1 {
		t.Fatalf("current = %+v: no fight has started, so it holds what the unit has done", s.Current)
	}
}

func TestMovementCountersRestartWithEachFight(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.a.CountMovement(r.l.MoveCounters())
	for range 3 {
		_ = r.a.Tick(act.Input{Forward: 1, Sprint: true})
	}
	r.l.fightStarted()
	for range 2 {
		_ = r.a.Tick(act.Input{Forward: 1, Sprint: true})
	}
	s, _ := r.l.Stats()
	if s.Total.SprintTicks != 5 || s.Current.SprintTicks != 2 {
		t.Fatalf("stats = %+v, want 5 in total and 2 since the fight began", s)
	}
}
