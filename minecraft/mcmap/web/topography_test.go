package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The script puts nothing on the page as markup, and is loaded once there
// is a panel to put its rows in.
func TestTopographyIsLoadedAfterThePanelAndWritesNoMarkup(t *testing.T) {
	usesNoMarkupSink(t, "topography.js")
	loadedAfterThePanel(t, "topography.js")
}

// The group is checked by doing it: its rows, its one layer for however
// many drawings are on, its legend, the dimensions that have it, the
// footer's line, and how a saved view, a link and the browser keep it. Skipped where there is no node to run it with.
func TestTopographyDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the topography's script with")
	}
	out, err := exec.Command(node, "testdata/topography.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the topography's script did not do what is asked of it: %v", err)
	}
}
