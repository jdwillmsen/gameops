package arena

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Outcomes of one boss-test run.
const (
	BossKilled  = "killed"
	BossWipe    = "wipe"
	BossTimeout = "timeout"
	// BossAborted marks a fight stopped because a player outside the crew
	// came online; it measured nothing.
	BossAborted = "aborted_player_online"
)

// BossRecord is one line of the run log. Deaths holds one count per warrior
// plus "total", so a later run with other warriors stays readable.
type BossRecord struct {
	TS      string         `json:"ts"`
	Boss    string         `json:"boss"`
	Effects []string       `json:"effects"`
	Outcome string         `json:"outcome"`
	Seconds float64        `json:"seconds"`
	Deaths  map[string]int `json:"deaths"`
	Run     int            `json:"run"`
}

// AppendBossRecord adds the record as one JSON line, creating the log's
// directory on first use.
func AppendBossRecord(path string, r BossRecord) error {
	if r.Effects == nil {
		r.Effects = []string{}
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// ReadBossRecords loads the whole log. A log that does not exist yet is an
// empty history, not an error.
func ReadBossRecords(path string) ([]BossRecord, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []BossRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r BossRecord
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
}

// BossSummary is one boss and effect set across its logged runs.
type BossSummary struct {
	Boss          string   `json:"boss"`
	Effects       []string `json:"effects"`
	Runs          int      `json:"runs"`
	Killed        int      `json:"killed"`
	MedianSeconds float64  `json:"median_seconds"`
	MedianDeaths  float64  `json:"median_deaths"`
}

// SummarizeBoss groups runs by boss and exact effect list, ordered by boss
// then effects so the output is stable. Aborted runs are left out: their time
// and deaths say nothing about the boss.
func SummarizeBoss(recs []BossRecord) []BossSummary {
	type group struct {
		sum     BossSummary
		seconds []float64
		deaths  []float64
	}
	groups := map[string]*group{}
	var keys []string
	for _, r := range recs {
		if r.Outcome == BossAborted {
			continue
		}
		key := r.Boss + "\x00" + strings.Join(r.Effects, "\x00")
		g := groups[key]
		if g == nil {
			effects := append([]string{}, r.Effects...)
			g = &group{sum: BossSummary{Boss: r.Boss, Effects: effects}}
			groups[key] = g
			keys = append(keys, key)
		}
		g.sum.Runs++
		if r.Outcome == BossKilled {
			g.sum.Killed++
		}
		g.seconds = append(g.seconds, r.Seconds)
		g.deaths = append(g.deaths, float64(r.Deaths["total"]))
	}
	sort.Strings(keys)
	out := make([]BossSummary, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		g.sum.MedianSeconds = median(g.seconds)
		g.sum.MedianDeaths = median(g.deaths)
		out = append(out, g.sum)
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}
