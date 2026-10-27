package arena

import (
	"encoding/json"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// AppendScorecard adds the scorecard as one JSON line with the same torn-line
// care as the boss log, so a crash mid-write costs one fight, not the file.
func AppendScorecard(path string, s tactics.Scorecard) error {
	if s.Deaths == nil {
		s.Deaths = map[string]int{}
	}
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return appendJSONLine(path, line)
}

// ReadScorecards loads the whole log and counts the lines it skipped as
// malformed. With skipAborted the trainer's view is returned: aborted fights
// measured nothing and are dropped without being counted as malformed.
func ReadScorecards(path string, skipAborted bool) (cards []tactics.Scorecard, skipped int, err error) {
	all, skipped, err := readJSONLines[tactics.Scorecard](path)
	if err != nil || !skipAborted {
		return all, skipped, err
	}
	for _, c := range all {
		if c.Outcome != tactics.OutcomeAborted {
			cards = append(cards, c)
		}
	}
	return cards, skipped, nil
}
