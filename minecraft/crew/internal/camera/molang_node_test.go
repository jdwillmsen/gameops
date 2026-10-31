package camera

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
)

// The expression evaluator is browser code with no Go counterpart, so it is
// checked by running the shipped module under node.
func TestMolangModule(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	molang, _ := filepath.Abs("web/molang.js")
	cmd := exec.Command(node, "--test", "testdata/molang.test.mjs")
	cmd.Env = append(os.Environ(), "MOLANG_JS="+molang)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("molang tests failed: %v\n%s", err, out)
	}
}

// TestWildlifeAnimationsEvaluate runs the animations of the lab's real
// wildlife pack, as the atlas build compiles them, through the page's
// evaluator.
func TestWildlifeAnimationsEvaluate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if _, err := os.Stat(filepath.Join(wildlifePack, "manifest.json")); err != nil {
		t.Skip("wildlife pack is not in this checkout")
	}
	_, table, _, err := atlas.BuildPackEntities(wildlifePack)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	tablePath := filepath.Join(t.TempDir(), "pack.json")
	if err := os.WriteFile(tablePath, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	molang, _ := filepath.Abs("web/molang.js")
	cmd := exec.Command(node, "--test", "testdata/wildlife.test.mjs")
	cmd.Env = append(os.Environ(), "MOLANG_JS="+molang, "PACK_TABLE="+tablePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wildlife animation tests failed: %v\n%s", err, out)
	}
}
