package main

import (
	"flag"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func parseScoreFlags(t *testing.T, args ...string) scoreFlags {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sf := addScoreFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return sf
}

var start = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func TestScorecardsAreOffByDefault(t *testing.T) {
	sf := parseScoreFlags(t)
	if rec := sf.recorder(tactics.ScenarioBoss, tactics.FamilyBoss, "warden", nil, start); rec != nil {
		t.Errorf("recorder = %+v, want none without --scorecards", rec)
	}
}

func TestScoreFlagsFillTheRecorderMeta(t *testing.T) {
	sf := parseScoreFlags(t, "--scorecards", "/tmp/x.jsonl", "--profile", "champ", "--side", "B", "--seed", "11")
	rec := sf.recorder(tactics.ScenarioWave, tactics.FamilyZombie, "gauntlet-t3", []string{"Rusty"}, start)
	m := rec.Meta
	if m.RunID != "wave-"+strconv.FormatInt(start.Unix(), 10) || m.Profile != "champ" || m.Side != "B" || m.Seed != 11 ||
		m.Scenario != tactics.ScenarioWave || m.Family != tactics.FamilyZombie || m.Opponent != "gauntlet-t3" {
		t.Errorf("meta = %+v", m)
	}
}

func TestBossScorecardRunIDs(t *testing.T) {
	tests := []struct {
		runs int
		want string
	}{{1, "fight"}, {3, "fight-2"}}
	for _, tt := range tests {
		rec := &arena.Recorder{Meta: arena.FightMeta{Scenario: tactics.ScenarioBoss}}
		rec.FightStart(start)
		rec.MobsKilled(1)
		c := bossScorecard(rec, "fight", tt.runs, arena.BossRecord{Run: 2, Outcome: arena.BossKilled, Seconds: 12})
		if c.RunID != tt.want || c.Outcome != tactics.OutcomeWin || c.Seconds != 12 || c.Kills != 1 {
			t.Errorf("runs=%d card = %+v, want id %q", tt.runs, c, tt.want)
		}
	}
}

func TestRunScorecardMapsTheResult(t *testing.T) {
	rec := &arena.Recorder{}
	rec.FightStart(start)
	c := runScorecard(rec, arena.Result{Outcome: arena.OutcomeWipe, DurationSeconds: 61})
	if c.Outcome != tactics.OutcomeLoss || c.Seconds != 61 {
		t.Errorf("card = %+v", c)
	}
}

func TestWriteScorecardAppendsAndOnlyWarnsOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cards.jsonl")
	var warn strings.Builder
	writeScorecard(&warn, path, tactics.Scorecard{RunID: "a", Outcome: tactics.OutcomeWin})
	cards, _, err := arena.ReadScorecards(path, false)
	if err != nil || len(cards) != 1 || warn.Len() != 0 {
		t.Fatalf("cards %v err %v warn %q", cards, err, warn.String())
	}
	writeScorecard(&warn, dir, tactics.Scorecard{RunID: "b"})
	if !strings.Contains(warn.String(), "scorecard not recorded") {
		t.Errorf("warning = %q, want a note that the card was lost", warn.String())
	}
}

func TestStatusURLFeedsTheRecorderAndIsOffByDefault(t *testing.T) {
	if rec := parseScoreFlags(t, "--scorecards", "/tmp/x.jsonl").recorder("wave", tactics.FamilyZombie, "o", nil, start); rec.Damage != nil {
		t.Errorf("damage source %v without --status-url", rec.Damage)
	}
	sf := parseScoreFlags(t, "--scorecards", "/tmp/x.jsonl", "--status-url", "http://crew-control/units/{name}/v1/state")
	rec := sf.recorder("wave", tactics.FamilyZombie, "o", []string{"Rusty"}, start)
	if src, ok := rec.Damage.(*arena.HTTPDamage); !ok || src.URL != "http://crew-control/units/{name}/v1/state" {
		t.Errorf("damage source = %#v", rec.Damage)
	}

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addScoreFlags(fs)
	if err := fs.Parse([]string{"--status-url", "http://crew-control/state"}); err == nil {
		t.Error("a status url without {name} was accepted")
	}
}
