package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The history's script is run, as the panel's and the structures' are: in
// a page made of plain objects (testdata/dom.cjs), against a map that
// remembers which day it was told to show and a server that answers as the
// tests say. Which day each control leads to, where a play stops, and that
// putting the control away puts the map back on now are checked by doing
// them. Skipped where there is no node to run it with.
func TestHistoryScriptDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the history's script with")
	}
	out, err := exec.Command(node, "testdata/history.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the history's script did not do what is asked of it: %v", err)
	}
}

// The map's own script is run on a map that keeps Leaflet's rules for a
// view and for a tile layer's loading (testdata/past.test.cjs), and told a
// day: whose tiles and extent the terrain is then drawn from, that the
// view does not move for it, and when the tiles it replaces go are checked
// by doing them. Skipped where there is no node to run it with.
func TestMapShowsADayWithoutMovingWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the map's script with")
	}
	out, err := exec.Command(node, "testdata/past.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the map's script did not do what is asked of it: %v", err)
	}
}
