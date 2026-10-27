package trainreport

import (
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func fixture(lines ...string) []tactics.Scorecard {
	r, torn, err := read(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		panic(err)
	}
	if torn != 0 {
		panic("fixture unexpectedly torn")
	}
	return r
}

func TestLoadTornTailIsSkipped(t *testing.T) {
	r, torn, err := read(strings.NewReader(strings.Join([]string{
		`{"ts":"2026-10-27T00:00:00Z","family":"zombie","profile":"base","outcome":"win","seconds":40}`,
		`{"ts":"2026-10-27T00:10:00Z","family":"zombie","profile":"base","outcome":"wi`,
		``,
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if torn != 1 || len(r) != 1 {
		t.Fatalf("torn=%d cards=%d", torn, len(r))
	}
}

func TestLoadBlankLinesAreNoise(t *testing.T) {
	r, torn, err := read(strings.NewReader("\n\n" + `{"family":"zombie","profile":"base","outcome":"win"}` + "\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if torn != 0 || len(r) != 1 {
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

func TestBuildTable(t *testing.T) {
	cases := []struct {
		name        string
		lines       []string
		aborted     int
		rows        int
		checkRow    int
		wantProfile string
		wantFights  int
		wantWins    int
		wantBest    string
		wantEmpty   bool
	}{
		{
			name: "normal two profiles one family",
			lines: []string{
				`{"ts":"2026-10-27T00:01:00Z","family":"zombie","profile":"base","outcome":"win","seconds":40,"deaths":{"w1":1}}`,
				`{"ts":"2026-10-27T00:02:00Z","family":"zombie","profile":"aggr","outcome":"loss","seconds":61,"deaths":{"w1":2}}`,
			},
			rows: 2, checkRow: 0, wantProfile: "base", wantFights: 1, wantWins: 1,
			wantBest: "base", // base wins (100+10) vs aggr (-50)
		},
		{
			name:    "all aborted means no rows",
			lines:   []string{`{"outcome":"aborted"}`, `{"outcome":"aborted"}`},
			aborted: 2, wantEmpty: true, wantBest: "",
		},
		{
			name:      "empty log",
			lines:     nil,
			wantEmpty: true, wantBest: "",
		},
		{
			name: "same family same fights orders by mean score",
			lines: []string{
				`{"ts":"2026-10-27T00:01:00Z","family":"zombie","profile":"weak","outcome":"loss","seconds":10}`,
				`{"ts":"2026-10-27T00:01:30Z","family":"zombie","profile":"strong","outcome":"win","seconds":10}`,
				`{"ts":"2026-10-27T00:02:00Z","family":"zombie","profile":"strong","outcome":"win","seconds":10}`,
			},
			rows: 2, checkRow: 0, wantProfile: "strong", wantFights: 2, wantWins: 2,
			wantBest: "strong",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cards := fixture(tc.lines...)
			rep := Build(cards, Options{})
			if rep.FightsRead != len(tc.lines) || rep.AbortedSkipped != tc.aborted {
				t.Fatalf("counts: read=%d aborted=%d", rep.FightsRead, rep.AbortedSkipped)
			}
			if tc.wantEmpty {
				if len(rep.Rows) != 0 {
					t.Fatalf("want empty rows, got %+v", rep.Rows)
				}
				return
			}
			if len(rep.Rows) != tc.rows {
				t.Fatalf("rows=%d want %d", len(rep.Rows), tc.rows)
			}
			r := rep.Rows[tc.checkRow]
			if r.Profile != tc.wantProfile || r.Fights != tc.wantFights || r.Wins != tc.wantWins {
				t.Fatalf("row %+v", r)
			}
			if r.WinRate <= 0 && tc.wantWins > 0 {
				t.Fatalf("win rate %+v", r)
			}
			if rep.BestProfile != tc.wantBest {
				t.Fatalf("best=%q want %q", rep.BestProfile, tc.wantBest)
			}
		})
	}
}

func TestBuildConfidenceIntervalTracksSpread(t *testing.T) {
	cards := fixture(
		`{"ts":"2026-10-27T00:01:00Z","family":"zombie","profile":"spy","outcome":"win","seconds":30}`,
		`{"ts":"2026-10-27T00:02:00Z","family":"zombie","profile":"spy","outcome":"win","seconds":31}`,
		`{"ts":"2026-10-27T00:03:00Z","family":"zombie","profile":"spy","outcome":"win","seconds":32}`,
	)
	r := Build(cards, Options{}).Rows[0]
	if !(r.ScoreLo < r.MeanScore && r.MeanScore < r.ScoreHi) {
		t.Fatalf("CI %v..%v around %v", r.ScoreLo, r.MeanScore, r.ScoreHi)
	}
	identical := fixture(
		`{"ts":"2026-10-27T00:01:00Z","family":"zombie","profile":"flat","outcome":"loss","seconds":10,"deaths":{"u":1}}`,
	)
	flat := Build(identical, Options{}).Rows[0]
	if flat.ScoreLo != flat.ScoreHi {
		t.Fatalf("single flat fight should have no width: %+v", flat)
	}
}

func TestRenderEmptyState(t *testing.T) {
	var sb strings.Builder
	Render(&sb, Build(nil, Options{}), nil)
	if sb.String() != "no fights recorded\n" {
		t.Fatalf("empty state %q", sb.String())
	}
}

func TestRenderAllAbortedIsEmpty(t *testing.T) {
	var sb strings.Builder
	Render(&sb, Build(fixture(`{"outcome":"aborted"}`), Options{}), nil)
	if sb.String() != "no fights recorded\n" {
		t.Fatalf("all-aborted state %q", sb.String())
	}
}
