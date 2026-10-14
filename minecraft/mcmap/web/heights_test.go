package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The height bands' script is run, with the panel's and the live layer's
// beside it (testdata/heights.test.cjs): which band a block is in over a
// given surface, which markers are on the map when a band is off, what the
// panel counts in each, and what is kept, are checked by doing them, against
// worked examples. Skipped where there is no node to run it with.
func TestHeightBandsDoWhatTheySayWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the height bands' script with")
	}
	out, err := exec.Command(node, "testdata/heights.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the height bands did not do what is asked of them: %v", err)
	}
}
