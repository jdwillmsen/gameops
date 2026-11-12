package life

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func nightSnap() belief.Snapshot {
	return belief.Snapshot{Spawned: true, PositionKnown: true, Position: [3]float32{10.5, 65.62, 10.5}}
}

func TestShouldShelter(t *testing.T) {
	home := &protocol.BlockPos{10, 64, 10}
	far := &protocol.BlockPos{40, 64, 10}
	phase := func(p string) belief.Env { return belief.Env{DayPhase: p} }
	cases := []struct {
		name string
		mod  func(*belief.Snapshot)
		env  belief.Env
		bed  bool
		home *protocol.BlockPos
		want bool
	}{
		{"night in the open", nil, phase(belief.PhaseNight), false, nil, true},
		{"dusk in the open", nil, phase(belief.PhaseDusk), false, nil, true},
		{"day", nil, phase(belief.PhaseDay), false, nil, false},
		{"dawn", nil, phase(belief.PhaseDawn), false, nil, false},
		{"time unknown", nil, phase(""), false, nil, false},
		{"has a bed", nil, phase(belief.PhaseNight), true, nil, false},
		{"already in the shelter", nil, phase(belief.PhaseNight), false, home, false},
		{"shelter is far away", nil, phase(belief.PhaseNight), false, far, true},
		{"shelter on another level", func(s *belief.Snapshot) { s.Position[1] += 8 }, phase(belief.PhaseNight), false, home, true},
		{"dead", func(s *belief.Snapshot) { s.Dead = true }, phase(belief.PhaseNight), false, nil, false},
		{"not spawned", func(s *belief.Snapshot) { s.Spawned = false }, phase(belief.PhaseNight), false, nil, false},
		{"nether", func(s *belief.Snapshot) { s.Dimension = 1 }, phase(belief.PhaseNight), false, nil, false},
		{"creative", func(s *belief.Snapshot) { s.Creative = true }, phase(belief.PhaseNight), false, nil, false},
		{"position unknown, shelter named", func(s *belief.Snapshot) { s.PositionKnown = false }, phase(belief.PhaseNight), false, home, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := nightSnap()
			if c.mod != nil {
				c.mod(&s)
			}
			if got := ShouldShelter(s, c.env, c.bed, c.home); got != c.want {
				t.Errorf("ShouldShelter = %v, want %v", got, c.want)
			}
		})
	}
}

func TestShouldSleep(t *testing.T) {
	phase := func(p string) belief.Env { return belief.Env{DayPhase: p} }
	cases := []struct {
		name string
		mod  func(*belief.Snapshot)
		env  belief.Env
		bed  bool
		want bool
	}{
		{"night with a bed", nil, phase(belief.PhaseNight), true, true},
		{"dusk with a bed", nil, phase(belief.PhaseDusk), true, true},
		{"night with no bed", nil, phase(belief.PhaseNight), false, false},
		{"day", nil, phase(belief.PhaseDay), true, false},
		{"dawn", nil, phase(belief.PhaseDawn), true, false},
		{"time unknown", nil, phase(""), true, false},
		{"already asleep", func(s *belief.Snapshot) { s.Sleeping = true }, phase(belief.PhaseNight), true, false},
		{"dead", func(s *belief.Snapshot) { s.Dead = true }, phase(belief.PhaseNight), true, false},
		{"nether", func(s *belief.Snapshot) { s.Dimension = 1 }, phase(belief.PhaseNight), true, false},
		{"creative", func(s *belief.Snapshot) { s.Creative = true }, phase(belief.PhaseNight), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := nightSnap()
			if c.mod != nil {
				c.mod(&s)
			}
			if got := ShouldSleep(s, c.env, c.bed); got != c.want {
				t.Errorf("ShouldSleep = %v, want %v", got, c.want)
			}
		})
	}
}
