package web

import (
	"os/exec"
	"strings"
	"testing"
)

// The footer's readout is run and not read: the biome under the pointer
// and the height of its surface, in each dimension, and nothing of either
// while the overlay is off. Skipped where there is no node to run it with.
func TestBiomeReadoutDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the biomes' script with")
	}
	out, err := exec.Command(node, "testdata/biomes.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the biomes' script did not do what is asked of it: %v", err)
	}
}
