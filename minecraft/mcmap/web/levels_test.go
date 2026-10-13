package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The nether's levels are run, not read: the map's script and the picker's
// are put in a page of plain objects (testdata/dom.cjs) against a server
// that lists the levels it has drawn, and what the picker shows where, which
// addresses each level is drawn from, what is kept, and how a fast run of
// choices leaves the layers of the map are checked by doing them. Skipped
// where there is no node to run it with.
func TestNetherLevelsDoWhatTheySayWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the levels' script with")
	}
	out, err := exec.Command(node, "testdata/levels.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the levels' script did not do what is asked of it: %v", err)
	}
}
