package arena

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func card(run, outcome string) tactics.Scorecard {
	return tactics.Scorecard{TS: "2026-10-27T12:00:00Z", RunID: run, Scenario: tactics.ScenarioMob,
		Family: tactics.FamilyZombie, Opponent: "zombie", Profile: "default", Outcome: outcome,
		Seconds: 42.5, Kills: 3, Deaths: map[string]int{"Rusty": 1}, Seed: 7}
}

func TestScorecardLogRoundTripsAndCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cards.jsonl")
	want := []tactics.Scorecard{card("a", tactics.OutcomeWin), card("b", tactics.OutcomeLoss)}
	for _, c := range want {
		if err := AppendScorecard(path, c); err != nil {
			t.Fatalf("AppendScorecard: %v", err)
		}
	}
	got, skipped, err := ReadScorecards(path, false)
	if err != nil || skipped != 0 {
		t.Fatalf("ReadScorecards = %v skipped %d", err, skipped)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read %+v, want %+v", got, want)
	}
}

func TestScorecardLogWritesEmptyDeathsAsAnObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cards.jsonl")
	c := card("a", tactics.OutcomeWin)
	c.Deaths = nil
	if err := AppendScorecard(path, c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"deaths":{}`) {
		t.Errorf("line = %s, want deaths as {}", raw)
	}
}

func TestScorecardLogOfAMissingFileIsEmpty(t *testing.T) {
	got, skipped, err := ReadScorecards(filepath.Join(t.TempDir(), "none.jsonl"), true)
	if err != nil || skipped != 0 || len(got) != 0 {
		t.Errorf("got %v, %d, %v; want an empty history", got, skipped, err)
	}
}

func TestScorecardLogSurvivesATornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cards.jsonl")
	if err := AppendScorecard(path, card("a", tactics.OutcomeWin)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"ts":"2026-10-27T12:01:00Z","runId":"torn","scen`)
	f.Close()
	if err := AppendScorecard(path, card("c", tactics.OutcomeLoss)); err != nil {
		t.Fatal(err)
	}
	got, skipped, err := ReadScorecards(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(got) != 2 || got[0].RunID != "a" || got[1].RunID != "c" {
		t.Errorf("got %+v, skipped %d; want a and c with the torn line skipped", got, skipped)
	}
}

func TestReadScorecardsCanIgnoreAbortedFights(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cards.jsonl")
	for _, c := range []tactics.Scorecard{
		card("a", tactics.OutcomeWin), card("b", tactics.OutcomeAborted), card("c", tactics.OutcomeTimeout),
	} {
		if err := AppendScorecard(path, c); err != nil {
			t.Fatal(err)
		}
	}
	all, _, _ := ReadScorecards(path, false)
	kept, skipped, err := ReadScorecards(path, true)
	if err != nil || skipped != 0 {
		t.Fatal(err, skipped)
	}
	if len(all) != 3 || len(kept) != 2 || kept[0].RunID != "a" || kept[1].RunID != "c" {
		t.Errorf("all %d, kept %+v; want the aborted fight dropped only when asked", len(all), kept)
	}
}
