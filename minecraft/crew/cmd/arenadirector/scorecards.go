package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// scoreFlags are the options that turn a fight into a training scorecard.
// Nothing is recorded unless --scorecards names a file.
type scoreFlags struct {
	path    *string
	profile *string
	runID   *string
	side    *string
	seed    *int64
}

func addScoreFlags(fs *flag.FlagSet) scoreFlags {
	return scoreFlags{
		path:    fs.String("scorecards", "", "append one tactics scorecard per fight to this JSONL file (off by default)"),
		profile: fs.String("profile", "default", "name of the tactics profile the warriors fight with, recorded on the scorecard"),
		runID:   fs.String("run-id", "", "scorecard run id (default derived from the start time); boss runs add -<run>"),
		side:    fs.String("side", "", "side of a mirrored fight, recorded on the scorecard"),
		seed:    fs.Int64("seed", 0, "seed of the fight, recorded on the scorecard"),
	}
}

// recorder returns nil when scorecards are off.
func (s scoreFlags) recorder(scenario string, family tactics.Family, opponent string, warriors []string, now time.Time) *arena.Recorder {
	if *s.path == "" {
		return nil
	}
	id := *s.runID
	if id == "" {
		id = scenario + "-" + strconv.FormatInt(now.Unix(), 10)
	}
	return &arena.Recorder{Meta: arena.FightMeta{
		RunID:    id,
		Scenario: scenario,
		Family:   family,
		Opponent: opponent,
		Profile:  *s.profile,
		Side:     *s.side,
		Seed:     *s.seed,
		Warriors: warriors,
	}}
}

// writeScorecard appends the card. A telemetry failure is reported but never
// fails the fight, whose own log and output already stand.
func writeScorecard(warn io.Writer, path string, c tactics.Scorecard) {
	if err := arena.AppendScorecard(path, c); err != nil {
		fmt.Fprintf(warn, "arenadirector: scorecard not recorded: %v\n", err)
	}
}

// bossScorecard finishes the recorder's fight for one boss run. A multi-run
// test gives every run its own id.
func bossScorecard(rec *arena.Recorder, baseID string, runs int, r arena.BossRecord) tactics.Scorecard {
	rec.Meta.RunID = baseID
	if runs > 1 {
		rec.Meta.RunID = fmt.Sprintf("%s-%d", baseID, r.Run)
	}
	return rec.Finish(arena.BossScoreOutcome(r.Outcome), r.Seconds)
}

func runScorecard(rec *arena.Recorder, res arena.Result) tactics.Scorecard {
	return rec.Finish(arena.RunScoreOutcome(res.Outcome), res.DurationSeconds)
}
