package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

const layoutFile = "../../lab/colosseum/layout.json"

func requestJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(mobRequest("a"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExitCodes(t *testing.T) {
	ctl := t.TempDir()
	os.WriteFile(filepath.Join(ctl, tacticsctl.TrainingFile), []byte("on"), 0o644)
	prof := t.TempDir()
	base := []string{"--layout", layoutFile, "--dry-run", "--profile-dir", prof, "--control-dir", ctl}
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  int
	}{
		{"real world is refused", append([]string{"--lab", "fwb-minecraft"}, base...), requestJSON(t), 2},
		{"training off is refused", []string{"--lab", "crew-lab", "--layout", layoutFile, "--dry-run", "--profile-dir", prof, "--control-dir", t.TempDir()}, requestJSON(t), 2},
		{"no lab", base, requestJSON(t), 2},
		{"garbled request", append([]string{"--lab", "crew-lab"}, base...), "{nope", 2},
		{"status url without a name", append([]string{"--lab", "crew-lab", "--status-url", "http://crew/state"}, base...), requestJSON(t), 2},
		{"an FWB compose file", append([]string{"--lab", "crew-lab", "--compose", "fwb/compose.yaml"}, base...), requestJSON(t), 2},
		{"bad emit", append([]string{"--lab", "crew-lab", "--emit", "some"}, base...), requestJSON(t), 2},
		{"a profile dir without a dry run", []string{"--lab", "crew-lab", "--layout", layoutFile, "--profile-dir", prof, "--control-dir", ctl}, requestJSON(t), 2},
		{"no sink at all", []string{"--lab", "crew-lab", "--layout", layoutFile, "--control-dir", ctl}, requestJSON(t), 2},
		{"a dry run still scores nothing", append([]string{"--lab", "crew-lab"}, base...), requestJSON(t), 2},
	}
	for _, tc := range cases {
		var out, errOut bytes.Buffer
		got := realMain(context.Background(), tc.args, strings.NewReader(tc.stdin), &out, &errOut)
		if got != tc.want {
			t.Errorf("%s: exit %d, want %d (stderr: %s)", tc.name, got, tc.want, errOut.String())
		}
		if out.Len() != 0 {
			t.Errorf("%s: printed %q, want nothing on stdout", tc.name, out.String())
		}
	}
}

func TestFileSinkDropsAndWithdrawsProfiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	s := FileSink{Dir: dir}
	p := candidate("cand-1")
	if err := s.Push(context.Background(), []string{"Rusty", "Slate"}, p); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "Rusty.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := tactics.Parse(raw)
	if err != nil || got.Name != "cand-1" || got.Default.KiteBackoff != 7 {
		t.Errorf("dropped profile = %+v, %v", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("dir holds %d files, want the two profiles and no temp files", len(entries))
	}
	for range 2 {
		if err := s.Clear(context.Background(), []string{"Rusty", "Slate"}); err != nil {
			t.Fatalf("Clear: %v", err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("dir still holds %d files after Clear", len(entries))
	}
}

func TestFileSinkRefusesPathTricks(t *testing.T) {
	s := FileSink{Dir: t.TempDir()}
	for _, name := range []string{"../evil", "a/b", "", ".hidden", "x y"} {
		if err := s.Push(context.Background(), []string{name}, candidate("c")); err == nil {
			t.Errorf("unit %q was accepted", name)
		}
	}
}
