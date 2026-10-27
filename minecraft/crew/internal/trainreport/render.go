package trainreport

import (
	"fmt"
	"io"
	"strings"
)

// Render writes the compact human view: the table, then the best profile's
// progress, then a --vs comparison when one was asked for. An empty log has
// one definitive line, not an empty frame.
func Render(w io.Writer, rep *Report, cmp *Compare) {
	if len(rep.Rows) == 0 {
		fmt.Fprintln(w, "no fights recorded")
		return
	}
	fmt.Fprintf(w, "%-10s %-18s %6s %5s %6s %-17s %7s %6s\n",
		"FAMILY", "PROFILE", "FIGHTS", "WINS", "WIN%", "SCORE 95%CI", "DEATHS", "SECS")
	for _, r := range rep.Rows {
		fmt.Fprintf(w, "%-10s %-18s %6d %5d %5.0f%% %15s %7.1f %6.1f\n",
			r.Family, trunc(r.Profile, 18), r.Fights, r.Wins, 100*r.WinRate,
			scoreCell(r), r.MeanDeaths, r.MeanSeconds)
	}
	fmt.Fprintf(w, "\n(%d fights read, %d aborted skipped)\n", rep.FightsRead, rep.AbortedSkipped)
	if cmp != nil {
		fmt.Fprintln(w)
		renderCompare(w, cmp)
	}
	if rep.BestProfile != "" {
		fmt.Fprintf(w, "\nBest profile: %s\n", rep.BestProfile)
		for _, p := range rep.Progress {
			fmt.Fprintf(w, "  %-10s", p.Family+":")
			if len(p.Buckets) == 0 {
				fmt.Fprintln(w, " no timestamped fights")
				continue
			}
			var parts []string
			for _, b := range p.Buckets {
				parts = append(parts, fmt.Sprintf("%s n=%d %s", shortTS(b.Start), b.Fights, fmt3(b.MeanScore)))
			}
			fmt.Fprintln(w, strings.Join(parts, " | "))
		}
	}
}

func renderCompare(w io.Writer, cmp *Compare) {
	fmt.Fprintf(w, "vs %s (A) vs %s (B)  pairs=%d unpaired=%d\n",
		trunc(cmp.ProfileA, 18), trunc(cmp.ProfileB, 18), cmp.Pairs, cmp.Unpaired)
	fmt.Fprintf(w, "mean A %s, mean B %s, diff A-B %s, 95%% CI [%s, %s], t=%s\n",
		fmt3(cmp.MeanA), fmt3(cmp.MeanB), fmt3(cmp.MeanDiff), fmt3(cmp.DiffLo), fmt3(cmp.DiffHi), fmt3(cmp.T))
	fmt.Fprintf(w, "verdict: %s\n", strings.TrimSpace(cmp.Verdict))
}

func scoreCell(r Row) string {
	if r.ScoreLo == r.ScoreHi {
		return fmt.Sprintf("%s (n=1)", fmt3(r.MeanScore))
	}
	return fmt.Sprintf("%s [%s,%s]", fmt3(r.MeanScore), fmt3(r.ScoreLo), fmt3(r.ScoreHi))
}

// trunc keeps long generated profile names from pushing the table off one
// line; correctness lives in --json, which prints the name in full.
func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "~"
}

func fmt3(v float64) string {
	return fmt.Sprintf("%.1f", v)
}

// shortTS prints the bucket start without its date; the date is the log's
// own context and the bucket sequence stays unique within a family.
func shortTS(ts string) string {
	if ts == "" {
		return "all"
	}
	const dateLen = len("2006-01-02")
	if len(ts) < dateLen+2 {
		return ts
	}
	return ts[dateLen+1:]
}
