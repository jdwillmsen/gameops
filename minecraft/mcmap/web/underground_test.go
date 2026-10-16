package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The scripts put nothing on the page as markup, and are loaded once there
// is a panel to put their rows in.
func TestUndergroundAndLostAreLoadedAfterThePanelAndWriteNoMarkup(t *testing.T) {
	for _, script := range []string{"underground.js", "lost.js"} {
		usesNoMarkupSink(t, script)
		loadedAfterThePanel(t, script)
	}
}

// Both are checked by doing them: their rows, the one layer and its
// address, the heights and how they are kept, the nether's own levels, a
// saved view's place, the days and the login, and a server that has none
// of it. Skipped where there is no node to run them with.
func TestUndergroundAndLostDoWhatTheySayWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the scripts with")
	}
	for _, script := range []string{"testdata/underground.test.cjs", "testdata/lost.test.cjs"} {
		out, err := exec.Command(node, script).CombinedOutput()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if !strings.HasPrefix(line, "ok ") {
				t.Log(line)
			}
		}
		if err != nil {
			t.Errorf("%s did not do what is asked of it: %v", script, err)
		}
	}
}
