package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

const layoutFile = "../../lab/colosseum/layout.json"

type exited struct{ code int }

// runCommand runs a command with exit replaced, and returns the code it
// ended with, or -1 if it returned normally. stderr is swallowed.
func runCommand(t *testing.T, fn func()) (code int) {
	t.Helper()
	prevExit := exit
	exit = func(c int) { panic(exited{c}) }
	prevErr := os.Stderr
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stderr = devnull
	defer func() {
		exit, os.Stderr = prevExit, prevErr
		devnull.Close()
		switch r := recover().(type) {
		case nil:
			code = -1
		case exited:
			code = r.code
		default:
			panic(r)
		}
	}()
	fn()
	return -1
}

// fakeLab puts a console behind newConsole whose player list is the given one.
func fakeLab(t *testing.T, players string) *arena.FakeConsole {
	t.Helper()
	console := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "list" {
			return players, nil
		}
		return "", nil
	}}
	prev := newConsole
	newConsole = func(string, string) (arena.Console, error) { return console, nil }
	t.Cleanup(func() { newConsole = prev })
	return console
}

const (
	crewOnly = "There are 2/10 players online:\nRusty, Slate"
	stranger = "There are 2/10 players online:\nRusty, Steve99"
)

func TestEveryCommandRefusesAnythingButTheCrewLab(t *testing.T) {
	console := fakeLab(t, crewOnly)
	cmds := map[string]struct {
		fn    func([]string)
		extra []string
	}{
		"run":    {run, []string{"--layout", layoutFile}},
		"reset":  {reset, nil},
		"status": {status, nil},
		"boss":   {boss, []string{"--layout", layoutFile, "--mob", "warden"}},
	}
	for name, c := range cmds {
		for _, lab := range []string{"", "fwb", "minecraft-fwb", "k8s:jdwillmsen-prd/minecraft-fwb-prd-0", "k8s:jdwillmsen-prd/other-0", "Lab Name"} {
			args := append([]string{"--lab", lab}, c.extra...)
			if code := runCommand(t, func() { c.fn(args) }); code != 1 {
				t.Errorf("%s --lab %q: exit %d, want 1", name, lab, code)
			}
		}
		args := append([]string{"--lab", "crew-lab", "--compose", "/srv/fwb/compose.yaml"}, c.extra...)
		if code := runCommand(t, func() { c.fn(args) }); code != 1 {
			t.Errorf("%s with an FWB compose file: exit %d, want 1", name, code)
		}
	}
	if len(console.Cmds) != 0 {
		t.Errorf("the server was touched: %v", console.Cmds)
	}
}

func TestRunAndResetRefuseWhileAStrangerIsOnline(t *testing.T) {
	console := fakeLab(t, stranger)
	for name, cmd := range map[string]func(){
		"run":   func() { run([]string{"--lab", "crew-lab", "--layout", layoutFile}) },
		"reset": func() { reset([]string{"--lab", "crew-lab"}) },
	} {
		if code := runCommand(t, cmd); code != 2 {
			t.Errorf("%s: exit %d, want the refusal code 2", name, code)
		}
	}
	for _, c := range console.Cmds {
		if c != "list" {
			t.Errorf("a command ran past the refusal: %q", c)
		}
	}
}

func TestResetRunsWhenOnlyCrewIsOnline(t *testing.T) {
	console := fakeLab(t, crewOnly)
	if code := runCommand(t, func() { reset([]string{"--lab", "crew-lab"}) }); code != -1 {
		t.Fatalf("reset exited %d", code)
	}
	if len(console.Cmds) < 2 {
		t.Errorf("reset ran nothing after the check: %v", console.Cmds)
	}
}

func TestRequireAloneHonoursCrewNames(t *testing.T) {
	console := fakeLab(t, stranger)
	if code := runCommand(t, func() { requireAlone(console, "Rusty,Steve99") }); code != -1 {
		t.Errorf("a listed player refused: exit %d", code)
	}
	if code := runCommand(t, func() { requireAlone(console, "Rusty") }); code != 2 {
		t.Errorf("an unlisted player allowed: exit %d", code)
	}
}

func TestDuelistsAreNeverDerivedFromCrewNames(t *testing.T) {
	args := []string{"--lab", "crew-lab", "--layout", "l.json", "--crew-names", "Rusty,Slate,Tinder,Agate,Basalt,Steve99"}
	cfg, err := parseDuel(args, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := arena.DefaultDuelists; !reflect.DeepEqual(cfg.duel.Duelists, want) {
		t.Errorf("duelists = %v, want the default %v however long --crew-names is", cfg.duel.Duelists, want)
	}
	if !strings.Contains(strings.Join(cfg.duel.CrewNames, ","), "Steve99") {
		t.Error("--crew-names no longer says who may be online")
	}
}

func TestDuelistsAllowAndRoster(t *testing.T) {
	roster := filepath.Join(t.TempDir(), "roster.txt")
	os.WriteFile(roster, []byte("# lab units\ncrew-lab-7\n Agate \ncrew-lab-8\ncrew-lab-7\ncrew-lab bad;\n"), 0o644)
	got, err := duelistList("Rusty,Slate", roster)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Rusty", "Slate", "crew-lab-7", "crew-lab-8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("duelists = %v, want %v", got, want)
	}
	if got, err := duelistList("Rusty", filepath.Join(t.TempDir(), "absent")); err != nil || !reflect.DeepEqual(got, []string{"Rusty"}) {
		t.Errorf("an absent roster: %v, %v", got, err)
	}
	if _, err := duelistList("", ""); err == nil {
		t.Error("an empty allow list accepted")
	}
}

func TestParseDuelRefusesAnFWBComposeFile(t *testing.T) {
	if _, err := parseDuel([]string{"--lab", "crew-lab", "--compose", "fwb/compose.yaml", "--layout", "l.json"}, io.Discard); err == nil {
		t.Error("an FWB compose file accepted")
	}
}
