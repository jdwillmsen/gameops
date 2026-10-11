package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The zoom's words and the place a view keeps are run and not read: the
// scale at each zoom from the farthest out to the deepest, what a viewer
// may type, and a place saved with a fractional zoom or an old whole one.
// Skipped where there is no node to run it with.
func TestZoomIsToldInPlainTermsAndKeptWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the zoom's script with")
	}
	out, err := exec.Command(node, "testdata/zoom.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the zoom's script did not do what is asked of it: %v", err)
	}
}
