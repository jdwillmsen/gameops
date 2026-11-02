package camera

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The quality table is a browser module with no Go counterpart, so its values
// are checked by running the shipped file under node. The console ships its
// own copy of the level names, and the same test compares them.
func TestQualityTable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	quality, _ := filepath.Abs("web/quality.js")
	consoleQuality, _ := filepath.Abs("../../../crew-control/internal/console/web/quality.js")
	cmd := exec.Command(node, "--test", "testdata/quality.test.mjs")
	cmd.Env = append(os.Environ(), "QUALITY_JS="+quality, "CONSOLE_QUALITY_JS="+consoleQuality)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("quality tests failed: %v\n%s", err, out)
	}
}
