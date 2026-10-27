package arena

import (
	"encoding/json"
	"io"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// ScoreSink receives one scorecard per side per round. The fight recorder
// owns the real implementation; the duel only needs somewhere to hand cards.
//
// The console can see who stood and who fell, so a card carries outcome,
// seconds, kills and deaths. DamageDealt and DamageTaken stay zero: only the
// units' own telemetry measures them, and the recorder merges them in.
type ScoreSink interface {
	Record(tactics.Scorecard) error
}

// JSONLSink writes each card as one JSON line.
type JSONLSink struct{ W io.Writer }

func (s JSONLSink) Record(c tactics.Scorecard) error {
	return json.NewEncoder(s.W).Encode(c)
}

// duelSide is one side of a round as the scorecard needs it.
type duelSide struct {
	side    string
	profile string
	names   []string
	dead    map[string]bool
}

func (s duelSide) alive() int {
	n := 0
	for _, name := range s.names {
		if !s.dead[name] {
			n++
		}
	}
	return n
}

// scorecard builds the card for this side. Opponent is the other side's
// profile, so the trainer can tell a mirror match from a real opponent.
func (s duelSide) scorecard(runID string, start time.Time, seconds float64, outcome string, foe duelSide) tactics.Scorecard {
	deaths := make(map[string]int, len(s.names))
	for _, n := range s.names {
		deaths[n] = 0
		if s.dead[n] {
			deaths[n] = 1
		}
	}
	return tactics.Scorecard{
		TS:       start.UTC().Format(time.RFC3339),
		RunID:    runID,
		Scenario: tactics.ScenarioDuel,
		Family:   tactics.FamilyPlayer,
		Opponent: foe.profile,
		Profile:  s.profile,
		Side:     s.side,
		Outcome:  outcome,
		Seconds:  seconds,
		Kills:    len(foe.names) - foe.alive(),
		Deaths:   deaths,
	}
}
