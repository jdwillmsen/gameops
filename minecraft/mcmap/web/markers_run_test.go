package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The markers' scripts are run, as the panel's are: the script that keeps
// the pictures, the one that finds the middle of the map that can be seen
// and the live layer are loaded onto a map made of plain objects
// (testdata/stage.cjs), frames are sent as the server sends them, and what
// was drawn, at what size, and where the map was taken are read back. An
// undeclared name in a path only a real frame takes, a marker that swells
// with the canvas through a zoom, and a followed player left under the
// panel all read fine and only show when run. Skipped where there is no
// node to run it with.
func TestMarkersAreTheirSizeAndFollowedOnesStayInTheMiddleWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the markers' scripts with")
	}
	out, err := exec.Command(node, "testdata/markers.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the markers' scripts did not do what is asked of them: %v", err)
	}
}
