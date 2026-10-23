package arena

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rec(boss string, effects []string, outcome string, seconds float64, total int) BossRecord {
	return BossRecord{TS: "2026-10-23T12:00:00Z", Boss: boss, Effects: effects, Outcome: outcome,
		Seconds: seconds, Deaths: map[string]int{"Rusty": total, "total": total}, Run: 1}
}

func TestAppendBossRecordCreatesTheDirectoryAndAppendsLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "arena-runs.jsonl")
	first := rec("warden", nil, BossKilled, 189, 5)
	second := rec("warden", []string{"effect give @e[name=W] weakness 99 4"}, BossWipe, 12.5, 3)
	for _, r := range []BossRecord{first, second} {
		if err := AppendBossRecord(path, r); err != nil {
			t.Fatalf("AppendBossRecord: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2:\n%s", len(lines), data)
	}
	want := `{"ts":"2026-10-23T12:00:00Z","boss":"warden","effects":[],"outcome":"killed","seconds":189,"deaths":{"Rusty":5,"total":5},"run":1}`
	if lines[0] != want {
		t.Errorf("first line = %s\nwant %s", lines[0], want)
	}
	got, err := ReadBossRecords(path)
	if err != nil || len(got) != 2 || got[1].Outcome != BossWipe || got[1].Effects[0] != second.Effects[0] {
		t.Errorf("ReadBossRecords = %+v, %v", got, err)
	}
}

func TestReadBossRecordsOfAMissingLogIsEmpty(t *testing.T) {
	got, err := ReadBossRecords(filepath.Join(t.TempDir(), "none.jsonl"))
	if err != nil || len(got) != 0 {
		t.Errorf("ReadBossRecords = %v, %v; want empty", got, err)
	}
}

func TestReadBossRecordsNamesTheBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte("{\"boss\":\"warden\"}\nnot json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBossRecords(path); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("ReadBossRecords error = %v, want it to name line 2", err)
	}
}

func TestSummarizeGroupsByBossAndEffects(t *testing.T) {
	weak := []string{"effect give @e[name=W] weakness 99 4"}
	recs := []BossRecord{
		rec("warden", nil, BossKilled, 189, 5),
		rec("warden", nil, BossKilled, 100, 10),
		rec("warden", nil, BossTimeout, 300, 6),
		rec("warden", weak, BossKilled, 123, 6),
		rec("ender_dragon", nil, BossWipe, 40, 3),
	}
	got := SummarizeBoss(recs)
	want := []BossSummary{
		{Boss: "ender_dragon", Effects: []string{}, Runs: 1, Killed: 0, MedianSeconds: 40, MedianDeaths: 3},
		{Boss: "warden", Effects: []string{}, Runs: 3, Killed: 2, MedianSeconds: 189, MedianDeaths: 6},
		{Boss: "warden", Effects: weak, Runs: 1, Killed: 1, MedianSeconds: 123, MedianDeaths: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SummarizeBoss =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMedianOfAnEvenCountAveragesTheMiddle(t *testing.T) {
	if m := median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Errorf("median = %v, want 2.5", m)
	}
	if m := median(nil); m != 0 {
		t.Errorf("median of nothing = %v, want 0", m)
	}
}

func TestSummarizeOfNothingIsAnEmptyList(t *testing.T) {
	if got := SummarizeBoss(nil); got == nil || len(got) != 0 {
		t.Errorf("SummarizeBoss(nil) = %#v, want a non-nil empty list", got)
	}
}
