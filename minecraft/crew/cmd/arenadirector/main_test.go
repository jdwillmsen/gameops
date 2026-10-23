package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

func TestApplyRallyOverrides(t *testing.T) {
	bookAfter := arena.Duration{Duration: 20 * time.Second}
	bookEvery := arena.Duration{Duration: 25 * time.Second}
	tests := []struct {
		name         string
		after, every time.Duration
		wantAfter    time.Duration
		wantEvery    time.Duration
	}{
		{"flags unset keep the book", -1, -1, 20 * time.Second, 25 * time.Second},
		{"zero disables", 0, -1, 0, 25 * time.Second},
		{"both set", 5 * time.Second, 7 * time.Second, 5 * time.Second, 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := &arena.WaveBook{RallyAfter: &bookAfter, RallyEvery: &bookEvery}
			applyRallyOverrides(book, tt.after, tt.every)
			if book.RallyAfter.Duration != tt.wantAfter {
				t.Errorf("rally_after = %v, want %v", book.RallyAfter.Duration, tt.wantAfter)
			}
			if book.RallyEvery.Duration != tt.wantEvery {
				t.Errorf("rally_every = %v, want %v", book.RallyEvery.Duration, tt.wantEvery)
			}
		})
	}
}

func TestPrintSummaryOfAMissingLogIsAnEmptyList(t *testing.T) {
	var out strings.Builder
	if err := printSummary(&out, io.Discard, filepath.Join(t.TempDir(), "none.jsonl")); err != nil {
		t.Fatalf("printSummary: %v", err)
	}
	if out.String() != "[]\n" {
		t.Errorf("summary = %q, want []", out.String())
	}
}

func TestPrintSummaryReadsTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	for _, seconds := range []float64{100, 200} {
		err := arena.AppendBossRecord(path, arena.BossRecord{Boss: "warden", Outcome: arena.BossKilled,
			Seconds: seconds, Deaths: map[string]int{"total": 4}, Run: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	if err := printSummary(&out, io.Discard, path); err != nil {
		t.Fatalf("printSummary: %v", err)
	}
	want := `[{"boss":"warden","effects":[],"runs":2,"killed":2,"median_seconds":150,"median_deaths":4}]` + "\n"
	if out.String() != want {
		t.Errorf("summary = %s\nwant %s", out.String(), want)
	}
}

func TestPrintSummaryWarnsAboutSkippedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte("{\"boss\":\"war\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, warn strings.Builder
	if err := printSummary(&out, &warn, path); err != nil {
		t.Fatalf("printSummary: %v", err)
	}
	if out.String() != "[]\n" || !strings.Contains(warn.String(), "skipped 1 malformed line") {
		t.Errorf("summary = %q, warning = %q", out.String(), warn.String())
	}
}

func TestDefaultLogPathIsUnderTheStateDirectory(t *testing.T) {
	t.Setenv("HOME", "/home/crewtest")
	got, err := defaultLogPath()
	if err != nil || got != "/home/crewtest/.local/state/arena-runs.jsonl" {
		t.Errorf("defaultLogPath = %q, %v", got, err)
	}
}
