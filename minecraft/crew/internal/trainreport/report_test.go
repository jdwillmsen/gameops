package trainreport

import (
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func fixture(t *testing.T, lines ...string) []tactics.Scorecard {
	t.Helper()
	r, torn, err := read(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if torn != 0 {
		t.Fatalf("fixture unexpectedly torn: %d", torn)
	}
	return r
}

func TestLoadTornTailIsSkipped(t *testing.T) {
	r, torn, err := read(strings.NewReader(strings.Join([]string{
		`{"ts":"2026-10-27T00:00:00Z","family":"zombie","profile":"base","outcome":"win","seconds":40}`,
		`{"ts":"2026-10-27T00:10:00Z","family":"zombie","profile":"base","outcome":"wi`,
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if torn != 1 || len(r) != 1 {
		t.Fatalf("torn=%d cards=%d", torn, len(r))
	}
}

func TestLoadBrokenMiddleLineIsAnError(t *testing.T) {
	_, _, err := read(strings.NewReader(strings.Join([]string{
		`{"family":"zombie","profile":"base","outcome":"win"}`,
		`{"family":zombie}`,
		`{"family":"zombie","profile":"other","outcome":"win"}`,
	}, "\n")))
	if err == nil || !strings.Contains(err.Error(), "record 2") {
		t.Fatalf("want structured error for record 2, got %v", err)
	}
}

func TestBuildDropsAbortedAndCountsRows(t *testing.T) {
	cards := []tactics.Scorecard{
		{Family: tactics.FamilyZombie, Profile: "base", Outcome: tactics.OutcomeWin, Seconds: 40},
		{Family: tactics.FamilyZombie, Profile: "base", Outcome: tactics.OutcomeAborted},
		{Family: tactics.FamilyZombie, Profile: "base", Outcome: tactics.OutcomeLoss, Seconds: 60},
	}
	rep := Build(cards, Options{})
	if rep.FightsRead != 3 || rep.AbortedSkipped != 1 {
		t.Fatalf("counts %+v", rep)
	}
	if len(rep.Rows) != 1 || rep.Rows[0].Fights != 2 || rep.Rows[0].Wins != 1 {
		t.Fatalf("rows %+v", rep.Rows)
	}
}
