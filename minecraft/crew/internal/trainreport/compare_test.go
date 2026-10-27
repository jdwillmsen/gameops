package trainreport

import (
	"math"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// sc builds a scorecard whose Score() the test can recompute itself, so the
// compare checks never depend on hand-derived constants.
func sc(profile string, seed int64, outcome string, seconds float64, deaths int) tactics.Scorecard {
	return tactics.Scorecard{
		TS: "2026-10-27T00:00:00Z", Family: tactics.FamilyDefault, Profile: profile,
		Outcome: outcome, Seconds: seconds, Deaths: map[string]int{"w1": deaths}, Seed: seed,
	}
}

func diffsOf(a, b []tactics.Scorecard, profA, profB string) []float64 {
	values := map[string][]float64{profA: {}, profB: {}}
	for _, c := range a {
		values[c.Profile] = append(values[c.Profile], c.Score())
	}
	for _, c := range b {
		values[c.Profile] = append(values[c.Profile], c.Score())
	}
	var d []float64
	for i := range a {
		d = append(d, values[profA][i]-values[profB][i])
	}
	return d
}

func TestTPValue(t *testing.T) {
	cases := []struct {
		name   string
		t, df  float64
		want   float64
		smallP bool
	}{
		{"t zero", 0, 4, 1, false},
		{"df4 table edge", 2.776, 4, 0.05, false},
		{"df9 table edge", 2.262, 9, 0.05, false},
		{"huge t", 20, 4, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := tTwoSidedP(c.t, c.df)
			if math.Abs(p-c.want) > 0.005 {
				t.Fatalf("p(%v, df=%v)=%v want ~%v", c.t, c.df, p, c.want)
			}
			if c.smallP && p >= 0.05 {
				t.Fatalf("want p<0.05 got %v", p)
			}
		})
	}
}

func TestTCrit95(t *testing.T) {
	// The textbook t* for df=4; the bisection agrees because both sides use
	// the same incomplete beta — the check pins the table value.
	if got := tcrit95(4); math.Abs(got-2.776) > 0.01 {
		t.Fatalf("tcrit95(4)=%v want 2.776", got)
	}
	if got := tcrit95(1000); math.Abs(got-1.96) > 0.02 {
		t.Fatalf("tcrit95(1000)=%v want ~1.96", got)
	}
}

func TestComparePairedBySeed(t *testing.T) {
	alpha := []tactics.Scorecard{
		sc("alpha", 1, tactics.OutcomeWin, 30, 2),
		sc("alpha", 2, tactics.OutcomeWin, 31, 3),
		sc("alpha", 3, tactics.OutcomeWin, 30, 4),
		sc("alpha", 4, tactics.OutcomeWin, 32, 3),
		sc("alpha", 5, tactics.OutcomeWin, 30, 2),
		sc("alpha", 6, tactics.OutcomeWin, 33, 3),
	}
	// Beta loses ground on the same six arena setups.
	beta := []tactics.Scorecard{
		sc("beta", 1, tactics.OutcomeWin, 33, 4),
		sc("beta", 2, tactics.OutcomeWin, 30, 5),
		sc("beta", 3, tactics.OutcomeLoss, 40, 3),
		sc("beta", 4, tactics.OutcomeWin, 40, 5),
		sc("beta", 5, tactics.OutcomeWin, 45, 4),
		sc("beta", 6, tactics.OutcomeWin, 31, 5),
	}
	cards := append(append([]tactics.Scorecard{}, alpha...), beta...)

	cmp := CompareProfiles(cards, "alpha", "beta")
	if cmp.Pairs != 6 || cmp.Unpaired != 0 {
		t.Fatalf("pairs=%d unpaired=%d", cmp.Pairs, cmp.Unpaired)
	}
	want := 0.0
	for _, d := range diffsOf(alpha, beta, "alpha", "beta") {
		want += d
	}
	want /= 6
	if math.Abs(cmp.MeanDiff-want) > 0.001 {
		t.Fatalf("mean diff %v want %v", cmp.MeanDiff, want)
	}
	if !strings.Contains(cmp.Verdict, "A better") || cmp.P >= 0.05 || cmp.DiffLo <= 0 {
		t.Fatalf("verdict %q cmp %+v", cmp.Verdict, cmp)
	}
	rev := CompareProfiles(cards, "beta", "alpha")
	if math.Abs(rev.MeanDiff+cmp.MeanDiff) > 0.001 || !strings.Contains(rev.Verdict, "B better") {
		t.Fatalf("mirror %+v vs %+v", rev, cmp)
	}
}

func TestCompareUnpairedFightsAreCounted(t *testing.T) {
	cards := []tactics.Scorecard{
		sc("alpha", 7, tactics.OutcomeWin, 30, 4),
		sc("beta", 7, tactics.OutcomeWin, 30, 4),
		sc("alpha", 8, tactics.OutcomeWin, 30, 4),
	}
	cmp := CompareProfiles(cards, "alpha", "beta")
	if cmp.Pairs != 1 || cmp.Unpaired != 1 {
		t.Fatalf("pairs=%d unpaired=%d", cmp.Pairs, cmp.Unpaired)
	}
	if cmp.Verdict != VerdictTooFew {
		t.Fatalf("one pair cannot separate: %q", cmp.Verdict)
	}
}

func TestCompareZeroSeedNeverPairs(t *testing.T) {
	cards := []tactics.Scorecard{
		sc("alpha", 0, tactics.OutcomeWin, 30, 4),
		sc("beta", 0, tactics.OutcomeWin, 30, 4),
	}
	cmp := CompareProfiles(cards, "alpha", "beta")
	if cmp.Pairs != 0 || cmp.Verdict != VerdictNoPairs {
		t.Fatalf("zero seeds must not pair: %+v", cmp)
	}
}

func TestCompareFlatDifferenceIsNotSignificant(t *testing.T) {
	cards := []tactics.Scorecard{
		sc("alpha", 1, tactics.OutcomeWin, 30, 4),
		sc("beta", 1, tactics.OutcomeWin, 30, 4),
		sc("alpha", 2, tactics.OutcomeWin, 30, 4),
		sc("beta", 2, tactics.OutcomeWin, 30, 4),
	}
	cmp := CompareProfiles(cards, "alpha", "beta")
	if cmp.Verdict != VerdictTooFew {
		t.Fatalf("zero spread must not claim significance: %q", cmp.Verdict)
	}
}

func TestCompareReadingTieHonest(t *testing.T) {
	// Nearly equal setups: the difference is noise-level, so no verdict.
	cards := []tactics.Scorecard{
		sc("alpha", 1, tactics.OutcomeWin, 30, 4), sc("beta", 1, tactics.OutcomeWin, 30, 4),
		sc("alpha", 2, tactics.OutcomeWin, 30, 4), sc("beta", 2, tactics.OutcomeWin, 30, 4),
		sc("alpha", 3, tactics.OutcomeWin, 29, 4), sc("beta", 3, tactics.OutcomeWin, 30, 4),
	}
	cmp := CompareProfiles(cards, "alpha", "beta")
	if strings.Contains(cmp.Verdict, "better") {
		t.Fatalf("noise must not be called better: %+v", cmp)
	}
	if !strings.HasPrefix(cmp.Verdict, VerdictTie) {
		t.Fatalf("verdict %q", cmp.Verdict)
	}
}
