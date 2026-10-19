package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The list of players is run and not read: what it asks for, what it
// draws, and that a gamertag stays text. Skipped where there is no node to
// run it with.
func TestRosterDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the roster's script with")
	}
	out, err := exec.Command(node, "testdata/roster.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the roster's script did not do what is asked of it: %v", err)
	}
}
