package trainreport

import (
	"math"
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// ProgressBuckets groups the named profile's fights by family into time
// buckets of the given width (0 = one bucket for everything) so a human can
// see whether the profile is improving while training runs. Scorecards
// without a parseable timestamp join no bucket.
func ProgressBuckets(cards []tactics.Scorecard, profile string, bucketMs int64) []Progress {
	type key struct {
		fam string
		b   int64
	}
	byKey := make(map[key][]float64)
	order := []string{}
	seen := map[string]bool{}
	for _, c := range cards {
		if c.Profile != profile || c.Outcome == tactics.OutcomeAborted {
			continue
		}
		ts, err := time.Parse(time.RFC3339, c.TS)
		if err != nil {
			continue
		}
		ms := ts.UnixMilli()
		// Width 0 reads as one bucket over everything; bucket 0 labels the
		// single slice with an empty start.
		if bucketMs > 0 {
			ms -= ms % bucketMs
		} else {
			ms = 0
		}
		fam := string(c.Family)
		if !seen[fam] {
			seen[fam] = true
			order = append(order, fam)
		}
		byKey[key{fam, ms}] = append(byKey[key{fam, ms}], c.Score())
	}
	var out []Progress
	for _, fam := range order {
		p := Progress{Profile: profile, Family: fam}
		var starts []int64
		for k := range byKey {
			if k.fam == fam {
				starts = append(starts, k.b)
			}
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
		for _, st := range starts {
			vs := byKey[key{fam, st}]
			mean, _, _ := ci95Values(vs)
			start := ""
			if bucketMs > 0 {
				start = time.UnixMilli(st).UTC().Format(time.RFC3339)
			}
			p.Buckets = append(p.Buckets, Bucket{
				Start:     start,
				Fights:    len(vs),
				MeanScore: round3(mean),
			})
		}
		out = append(out, p)
	}
	return out
}

// BestProfile names the profile with the highest mean score, ties broken by
// more fights, then by name so the choice is stable across reruns.
func BestProfile(cards []tactics.Scorecard) string {
	type agg struct {
		n   int
		sum float64
	}
	byProf := map[string]*agg{}
	var order []string
	for _, c := range cards {
		if c.Outcome == tactics.OutcomeAborted {
			continue
		}
		a := byProf[c.Profile]
		if a == nil {
			a = &agg{}
			byProf[c.Profile] = a
			order = append(order, c.Profile)
		}
		a.n++
		a.sum += c.Score()
	}
	best, bestN, bestMean := "", 0, 0.0
	for _, name := range order {
		a := byProf[name]
		m := a.sum / float64(a.n)
		if best == "" || m > bestMean ||
			(m == bestMean && a.n > bestN) ||
			(m == bestMean && a.n == bestN && name < best) {
			best, bestN, bestMean = name, a.n, m
		}
	}
	return best
}

// round3 keeps JSON drift down; input is generated scores, so three decimals
// carry the message to humans and tests alike.
func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
