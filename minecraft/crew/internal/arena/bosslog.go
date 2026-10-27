package arena

import (
	"bufio"
	"encoding/json"
	"errors"
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
	Event   string         `json:"event,omitempty"`
	Effects []string       `json:"effects"`
	Outcome string         `json:"outcome"`
	Seconds float64        `json:"seconds"`
	Deaths  map[string]int `json:"deaths"`
	Run     int            `json:"run"`
}

// AppendBossRecord adds the record as one JSON line, creating the log's
// directory on first use. A log whose last write was cut short gets a newline
// first, so the new record does not join the torn line.
func AppendBossRecord(path string, r BossRecord) error {
	if r.Effects == nil {
		r.Effects = []string{}
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return appendJSONLine(path, line)
}

// appendJSONLine writes one line to a JSONL log, creating its directory.
func appendJSONLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	torn, err := endsTorn(f)
	if err != nil {
		return errors.Join(err, f.Close())
	}
	if torn {
		line = append([]byte{'\n'}, line...)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// endsTorn reports whether f is non-empty and does not end in a newline.
func endsTorn(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// ReadBossRecords loads the whole log and counts the lines it skipped as
// malformed: one torn write must not hide every other run. A log that does
// not exist yet is an empty history, not an error.
func ReadBossRecords(path string) (recs []BossRecord, skipped int, err error) {
	return readJSONLines[BossRecord](path)
}

// readJSONLines decodes every line of a JSONL log into T, counting the lines
// it could not decode.
func readJSONLines[T any](path string) (recs []T, skipped int, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r T
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			skipped++
			continue
		}
		recs = append(recs, r)
	}
	return recs, skipped, sc.Err()
}

// BossSummary is one boss and effect set across its logged runs.
type BossSummary struct {
	Boss          string   `json:"boss"`
	Event         string   `json:"event,omitempty"`
	Effects       []string `json:"effects"`
	Runs          int      `json:"runs"`
	Killed        int      `json:"killed"`
	MedianSeconds float64  `json:"median_seconds"`
	MedianDeaths  float64  `json:"median_deaths"`
}

// SummarizeBoss groups runs by boss, spawn event and exact effect list, ordered by boss
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
		key := r.Boss + "\x00" + r.Event + "\x00" + strings.Join(r.Effects, "\x00")
		g := groups[key]
		if g == nil {
			effects := append([]string{}, r.Effects...)
			g = &group{sum: BossSummary{Boss: r.Boss, Event: r.Event, Effects: effects}}
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
