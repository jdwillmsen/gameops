package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDescribeSortsStatesAndNamesAir(t *testing.T) {
	if got := describe(outcome{}); got != "air" {
		t.Errorf("air = %q", got)
	}
	o := outcome{Placed: true, Name: "oak_stairs", Props: map[string]any{"weirdo_direction": int32(2), "upside_down_bit": false, "minecraft:corner": "none"}}
	if got, want := describe(o), "oak_stairs[corner=none,upside_down_bit=false,weirdo_direction=2]"; got != want {
		t.Errorf("describe = %q, want %q", got, want)
	}
}

func TestProbeNamesResolvesListsAndRejectsUnknown(t *testing.T) {
	all, err := probeNames("all")
	if err != nil || len(all) != len(probeList()) {
		t.Fatalf("all = %d, %v", len(all), err)
	}
	two, err := probeNames("hinge, gravity")
	if err != nil || len(two) != 2 || two[0].name != "hinge" {
		t.Errorf("two = %v, %v", two, err)
	}
	if _, err := probeNames("hinge,nope"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown probe accepted: %v", err)
	}
}

func TestYawStepsCoverTheRotationStates(t *testing.T) {
	y := sixteenYaws()
	if len(y) != 16 || y[0] != 0 || y[1] != 23 || y[4] != 90 || y[15] != 338 {
		t.Errorf("sixteen = %v", y)
	}
	if r := roundYaws(); len(r) != 32 || r[2] != 23 || r[8] != 90 {
		t.Errorf("round = %v", r)
	}
}

func TestReportPrintsAsItGoes(t *testing.T) {
	var buf bytes.Buffer
	r := &report{out: &buf}
	r.head("Gravity")
	r.say("- sand fell in %dms", 450)
	if !strings.Contains(buf.String(), "## Gravity") || !strings.Contains(buf.String(), "sand fell in 450ms") || len(r.lines) != 2 {
		t.Errorf("report = %q", buf.String())
	}
}

func TestJoinLinesNamesTheEmptyCase(t *testing.T) {
	if joinLines(nil) != "nothing changed" || joinLines([]string{"a", "b"}) != "a; b" {
		t.Error("joinLines")
	}
	if got := dedupe([]string{"a", "a", "b", "a"}); strings.Join(got, "") != "aba" {
		t.Errorf("dedupe = %v", got)
	}
}
