package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The Xbox sign-in script is run, not read: xbox.js in a page of plain
// objects. Skipped where there is no node to run it with.
func TestXboxPageDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the Xbox script with")
	}
	out, err := exec.Command(node, "testdata/xbox.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the Xbox script did not do what is asked of it: %v", err)
	}
}
