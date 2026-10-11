package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The debug panel and the report are run and not read: the panel holds no
// timer or listener while it is off and leaves none when turned off, a
// report names no one and says where no one is unless asked, and scripts
// of two releases are told apart. Skipped where there is no node to run it
// with.
func TestTroubleshootingToolsCostNothingOffAndRevealNothingUnasked(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the troubleshooting script with")
	}
	out, err := exec.Command(node, "testdata/debug.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the troubleshooting script did not do what is asked of it: %v", err)
	}
}
