package main

import (
	"io"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

func squadArgs(extra ...string) []string {
	return append([]string{"--lab", "lab", "--layout", "l.json", "--red", "Rusty", "--blue", "Tinder"}, extra...)
}

func TestParseDuelWithSquadsBuildsTheSquadPolicy(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", "tok-1")
	cfg, err := parseDuel(squadArgs("--squad-url", "http://control.lab:8080", "--red-squad", "lab-red", "--blue-squad", "lab-blue"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := cfg.policy(&arena.FakeConsole{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pol.(*arena.SquadPolicy); !ok {
		t.Errorf("policy = %T, want the squad policy", pol)
	}
}

func TestParseDuelWithoutSquadsKeepsTheTagsOnly(t *testing.T) {
	cfg, err := parseDuel(squadArgs(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := cfg.policy(&arena.FakeConsole{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pol.(*arena.ConsolePolicy); !ok {
		t.Errorf("policy = %T, want the console tags alone", pol)
	}
}

func TestParseDuelRefusesASquadItMayNotSignal(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", "tok-1")
	good := []string{"--squad-url", "http://control.lab:8080", "--red-squad", "lab-red", "--blue-squad", "lab-blue"}
	with := func(flag, val string) []string {
		out := append([]string{}, good...)
		for i := 0; i < len(out); i += 2 {
			if out[i] == flag {
				out[i+1] = val
			}
		}
		return out
	}
	for name, args := range map[string][]string{
		"a production red squad": with("--red-squad", "alpha"),
		"a production blue":      with("--blue-squad", "fwb"),
		"a path-shaped id":       with("--red-squad", "lab-/../fwb"),
		"one squad twice":        with("--blue-squad", "lab-red"),
		"a file URL":             with("--squad-url", "file:///etc"),
		"no host":                with("--squad-url", "http://"),
		"only the URL":           {"--squad-url", "http://control.lab:8080"},
		"only a squad":           {"--red-squad", "lab-red"},
	} {
		if _, err := parseDuel(squadArgs(args...), io.Discard); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseDuelNeedsTheTokenFromTheEnvironment(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", "")
	_, err := parseDuel(squadArgs("--squad-url", "http://control.lab:8080", "--red-squad", "lab-red", "--blue-squad", "lab-blue"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "CONTROL_TOKEN") {
		t.Errorf("err = %v, want a missing-token refusal", err)
	}
}
