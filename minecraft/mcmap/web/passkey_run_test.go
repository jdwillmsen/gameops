package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The passkey login and the account and Admin panels are run, not read:
// passkey.js in a page of plain objects against a stub server and a stub
// authenticator. Skipped where there is no node to run it with.
func TestPasskeyPageDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the passkey script with")
	}
	out, err := exec.Command(node, "testdata/passkey.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the passkey script did not do what is asked of it: %v", err)
	}
}
