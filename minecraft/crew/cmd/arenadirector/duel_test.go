package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestParseDuelReadsFlagsAndDefaults(t *testing.T) {
	cfg, err := parseDuel([]string{"--lab", "lab", "--layout", "l.json", "--red", "Rusty, Slate", "--blue", "Tinder,Agate",
		"--red-profile", "cand", "--kits", "vanguard/t5,ranger/t5", "--scores", "/tmp/s.jsonl"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.duel
	if !reflect.DeepEqual(d.Red, []string{"Rusty", "Slate"}) || !reflect.DeepEqual(d.Blue, []string{"Tinder", "Agate"}) ||
		d.RedProfile != "cand" || d.BlueProfile != "default" || d.Rounds != 2 || d.RoundTimeout != 2*time.Minute ||
		d.MaxWall != 30*time.Minute || cfg.scores != "/tmp/s.jsonl" || len(d.Kits) != 2 {
		t.Errorf("config = %+v", cfg)
	}
}

func TestParseDuelRefusesAnyTargetButTheCrewLab(t *testing.T) {
	for _, lab := range []string{"", "fwb", "k8s:jdwillmsen-prd/minecraft-fwb-prd-0", "k8s:jdwillmsen-prd/other-0"} {
		_, err := parseDuel([]string{"--lab", lab, "--layout", "l.json"}, io.Discard)
		if err == nil {
			t.Errorf("lab %q accepted", lab)
		}
	}
	if _, err := parseDuel([]string{"--lab", "lab"}, io.Discard); err == nil {
		t.Error("a duel without a layout accepted")
	}
}

func TestParseDuelDefaultScorePathIsUnderTheStateDirectory(t *testing.T) {
	t.Setenv("HOME", "/home/crewtest")
	cfg, err := parseDuel([]string{"--lab", "lab", "--layout", "l.json"}, io.Discard)
	if err != nil || cfg.scores != "/home/crewtest/.local/state/duel-scorecards.jsonl" {
		t.Errorf("scores = %q, %v", cfg.scores, err)
	}
}

func TestFileSinkAppendsCards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "cards.jsonl")
	s := fileSink{path: path}
	for _, side := range []string{arena.SideRed, arena.SideBlue} {
		if err := s.Record(tactics.Scorecard{Side: side}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readLines(t, path); len(got) != 2 {
		t.Errorf("lines = %v", got)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
