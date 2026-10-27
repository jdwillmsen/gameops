// Package trainreport reads a JSONL training log of tactics.Scorecard and
// summarises how the fought profiles did, per family and over time. It is
// lab-only reporting: everything derives from scorecards already produced
// elsewhere; it starts no fights and touches no live server.
package trainreport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Row is one family × profile line of the table.
type Row struct {
	Family      string  `json:"family"`
	Profile     string  `json:"profile"`
	Fights      int     `json:"fights"`
	Wins        int     `json:"wins"`
	WinRate     float64 `json:"winRate"`
	MeanScore   float64 `json:"meanScore"`
	ScoreLo     float64 `json:"scoreLo"`
	ScoreHi     float64 `json:"scoreHi"`
	MeanDeaths  float64 `json:"meanDeaths"`
	MeanSeconds float64 `json:"meanSeconds"`
}

// Bucket is one time slice of the best profile's per-family progress.
type Bucket struct {
	Start     string  `json:"start"`
	Fights    int     `json:"fights"`
	MeanScore float64 `json:"meanScore"`
}

// Progress tracks one profile's mean score per family per bucket.
type Progress struct {
	Profile string   `json:"profile"`
	Family  string   `json:"family"`
	Buckets []Bucket `json:"buckets"`
}

// Compare is the paired verdict between two profiles.
type Compare struct {
	ProfileA string  `json:"profileA"`
	ProfileB string  `json:"profileB"`
	Pairs    int     `json:"pairs"`
	Unpaired int     `json:"unpaired"`
	MeanA    float64 `json:"meanA"`
	MeanB    float64 `json:"meanB"`
	MeanDiff float64 `json:"meanDiff"`
	DiffLo   float64 `json:"diffLo"`
	DiffHi   float64 `json:"diffHi"`
	T        float64 `json:"t"`
	P        float64 `json:"p"`
	Verdict  string  `json:"verdict"`
}

// Report holds everything the table and the --json dump need.
type Report struct {
	FightsRead     int        `json:"fightsRead"`
	AbortedSkipped int        `json:"abortedSkipped"`
	TornLines      int        `json:"tornLines"`
	Rows           []Row      `json:"rows"`
	BestProfile    string     `json:"bestProfile,omitempty"`
	Progress       []Progress `json:"progress,omitempty"`
	Compare        *Compare   `json:"comparison,omitempty"`
}

// Options tune the report.
type Options struct {
	BucketMs int64 // width of a progress bucket, 0 = all fights one bucket
}

// Load reads scorecards from a JSONL path, would-have-been-records and all.
func Load(path string) ([]tactics.Scorecard, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	return read(f)
}

func read(r io.Reader) ([]tactics.Scorecard, int, error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	// A torn tail is a record the trainer was still writing when it died, so
	// only the final non-empty line may fail to decode; anywhere else the log
	// is corrupt and stops the report.
	var lines []string
	for s.Scan() {
		if text := strings.TrimSpace(s.Text()); text != "" {
			lines = append(lines, text)
		}
	}
	if err := s.Err(); err != nil {
		return nil, 0, fmt.Errorf("trainreport: reading input: %v", err)
	}

	var cards []tactics.Scorecard
	torn := 0
	for i, text := range lines {
		var c tactics.Scorecard
		if err := json.Unmarshal([]byte(text), &c); err != nil {
			if i == len(lines)-1 {
				torn++
				continue
			}
			return nil, 0, fmt.Errorf("trainreport: record %d of %d: %v", i+1, len(lines), err)
		}
		cards = append(cards, c)
	}
	return cards, torn, nil
}

// Build aggregates scored fights into the report: rows, the best profile
// (chosen on scored fights only) and its progress. Aborted fights carry no
// signal, mirroring Score() itself, so they are dropped upstream of all of
// it.
func Build(cards []tactics.Scorecard, opt Options) *Report {
	kept := scoreOnly(cards)
	rep := &Report{
		FightsRead:     len(cards),
		AbortedSkipped: len(cards) - len(kept),
		Rows:           rows(kept),
	}
	if len(kept) == 0 {
		return rep
	}
	rep.BestProfile = BestProfile(kept)
	rep.Progress = ProgressBuckets(kept, rep.BestProfile, opt.BucketMs)
	return rep
}

func scoreOnly(cards []tactics.Scorecard) []tactics.Scorecard {
	var kept []tactics.Scorecard
	for _, c := range cards {
		if c.Outcome == tactics.OutcomeAborted {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func rows(cards []tactics.Scorecard) []Row {
	type key struct{ fam, prof string }
	type agg struct {
		key    key
		n      int
		wins   int
		sum    float64
		sumsq  float64
		deaths int
		secs   float64
	}
	byKey := make(map[key]*agg)
	var order []key
	for _, c := range cards {
		k := key{fam: string(c.Family), prof: c.Profile}
		a := byKey[k]
		if a == nil {
			a = &agg{key: k}
			byKey[k] = a
			order = append(order, k)
		}
		s := c.Score()
		a.n++
		if c.Outcome == tactics.OutcomeWin {
			a.wins++
		}
		a.sum += s
		a.sumsq += s * s
		a.deaths += c.TotalDeaths()
		a.secs += c.Seconds
	}
	rs := make([]Row, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		m := a.sum / float64(a.n)
		lo, hi := ci95(m, a.sumsq, a.n)
		r := Row{
			Family: k.fam, Profile: k.prof,
			Fights: a.n, Wins: a.wins, WinRate: float64(a.wins) / float64(a.n),
			MeanScore: m, ScoreLo: lo, ScoreHi: hi,
			MeanDeaths:  float64(a.deaths) / float64(a.n),
			MeanSeconds: a.secs / float64(a.n),
		}
		rs = append(rs, r)
	}
	sortRows(rs)
	return rs
}

// sortRows keeps the table reading like the board: families together, the
// busiest profiles first so the interesting rows are at the top.
func sortRows(rs []Row) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Family != rs[j].Family {
			return rs[i].Family < rs[j].Family
		}
		if rs[i].Fights != rs[j].Fights {
			return rs[i].Fights > rs[j].Fights
		}
		if rs[i].MeanScore != rs[j].MeanScore {
			return rs[i].MeanScore > rs[j].MeanScore
		}
		return rs[i].Profile < rs[j].Profile
	})
}
