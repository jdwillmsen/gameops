package trainreport

import (
	"fmt"
	"math"
	"sort"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Verdicts so tests and users can read the outcome without parsing numbers.
const (
	VerdictNoPairs = "no paired fights recorded"
	VerdictTooFew  = "not enough paired fights to separate the profiles"
	VerdictABetter = "profile A better"
	VerdictBBetter = "profile B better"
	VerdictTie     = "no clear difference"
)

// CompareProfiles pairs scorecards of the two named profiles by equal Seed
// and scores the paired differences A - B. Seed 0 means the log carried no
// seed, which pairs with everything and would fake matches, so such fights
// are counted unpaired rather than used.
func CompareProfiles(cards []tactics.Scorecard, profileA, profileB string) *Compare {
	type side struct{ bySeed map[int64][]float64 }
	sides := map[string]*side{
		profileA: {bySeed: map[int64][]float64{}},
		profileB: {bySeed: map[int64][]float64{}},
	}
	unpaired := 0
	for _, c := range cards {
		if c.Outcome == tactics.OutcomeAborted {
			continue
		}
		s, ok := sides[c.Profile]
		if !ok {
			continue
		}
		if c.Seed == 0 {
			unpaired++
			continue
		}
		s.bySeed[c.Seed] = append(s.bySeed[c.Seed], c.Score())
	}

	// One seed is one arena setup, so each side contributes at most one
	// fight per seed; runs beyond the first on either side stay unpaired.
	// Seeds appearing on only one side are unpaired too.
	seen := map[int64]bool{}
	for seed := range sides[profileA].bySeed {
		seen[seed] = true
	}
	for seed := range sides[profileB].bySeed {
		seen[seed] = true
	}
	seeds := make([]int64, 0, len(seen))
	for seed := range seen {
		seeds = append(seeds, seed)
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i] < seeds[j] })

	aVals, bVals, diffs := []float64{}, []float64{}, []float64{}
	for _, seed := range seeds {
		av, bv := sides[profileA].bySeed[seed], sides[profileB].bySeed[seed]
		if len(av) == 0 || len(bv) == 0 {
			unpaired += len(av) + len(bv)
			continue
		}
		if len(av) > 1 {
			unpaired += len(av) - 1
		}
		if len(bv) > 1 {
			unpaired += len(bv) - 1
		}
		pairAv, pairBv := av[0], bv[0]
		aVals = append(aVals, pairAv)
		bVals = append(bVals, pairBv)
		diffs = append(diffs, pairAv-pairBv)
	}

	cmp := &Compare{
		ProfileA: profileA,
		ProfileB: profileB,
		Pairs:    len(diffs),
		Unpaired: unpaired,
	}
	if len(diffs) == 0 {
		cmp.Verdict = VerdictNoPairs
		return cmp
	}
	meanA, _, _ := ci95Values(aVals)
	meanB, _, _ := ci95Values(bVals)
	cmp.MeanA = round3(meanA)
	cmp.MeanB = round3(meanB)
	meanDiff, lo, hi := ci95Values(diffs)
	cmp.MeanDiff = round3(meanDiff)
	cmp.DiffLo = round3(lo)
	cmp.DiffHi = round3(hi)
	if len(diffs) < 2 {
		cmp.Verdict = VerdictTooFew
		return cmp
	}
	sd := stdev(diffs)
	if sd == 0 {
		cmp.Verdict = VerdictTooFew
		return cmp
	}
	t := meanDiff / (sd / math.Sqrt(float64(len(diffs))))
	df := float64(len(diffs) - 1)
	cmp.T = round3(t)
	cmp.P = round3(tTwoSidedP(t, df))
	switch {
	case cmp.P < 0.05 && meanDiff > 0:
		cmp.Verdict = VerdictABetter
	case cmp.P < 0.05 && meanDiff < 0:
		cmp.Verdict = VerdictBBetter
	default:
		cmp.Verdict = VerdictTie
	}
	cmp.Verdict = fmt.Sprintf("%s (p=%.4f)", cmp.Verdict, cmp.P)
	return cmp
}

// stdev is the sample standard deviation; a single value or a flat set has
// no spread to measure.
func stdev(vs []float64) float64 {
	if len(vs) < 2 {
		return 0
	}
	mean, _, _ := ci95Values(vs)
	ss := 0.0
	for _, v := range vs {
		d := v - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(vs)-1))
}
