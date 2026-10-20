package camera

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The mesher is a browser worker with no Go counterpart, so its face rules
// are checked by running the shipped file under node.
func TestMesherFaces(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	mesher, _ := filepath.Abs("web/mesher.js")
	cmd := exec.Command(node, "--test", "testdata/mesher.test.mjs")
	cmd.Env = append(os.Environ(), "MESHER_JS="+mesher)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mesher tests failed: %v\n%s", err, out)
	}
}
