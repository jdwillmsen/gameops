package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The other tests of the page read its scripts; this one runs them. The
// panel's script and the script that keeps the record are loaded into a
// page made of plain objects (testdata/dom.cjs), rows are registered as the
// layers register them, and the lines are pressed: what is on, what is
// kept, and what a reload brings back are then checked as they are, which
// reading the source cannot do. It needs a node to run in and nothing
// else, and is skipped where there is none.
func TestLayerPanelDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the panel's scripts with")
	}
	out, err := exec.Command(node, "testdata/panel.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the panel's scripts did not do what is asked of them: %v", err)
	}
}
