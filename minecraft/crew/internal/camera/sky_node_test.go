package camera

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The sky curves are browser code with no Go counterpart, so they are checked
// by running the shipped module under node.
func TestSkyCurves(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	sky, _ := filepath.Abs("web/sky.js")
	cmd := exec.Command(node, "--test", "testdata/sky.test.mjs")
	cmd.Env = append(os.Environ(), "SKY_JS="+sky)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sky tests failed: %v\n%s", err, out)
	}
}
