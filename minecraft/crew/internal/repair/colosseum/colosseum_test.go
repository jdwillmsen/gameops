package colosseum

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPlanAndLayoutLoad(t *testing.T) {
	ops, err := Plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) < 1000 {
		t.Fatalf("plan has %d ops", len(ops))
	}
	l, err := Layout()
	if err != nil {
		t.Fatal(err)
	}
	if l.Center.X != -300 || l.Center.Z != -758 {
		t.Errorf("layout centred %d,%d", l.Center.X, l.Center.Z)
	}
}

// The committed plan must be what the generator writes today. Regenerate with
// `go generate ./internal/repair/colosseum`.
func TestCommittedPlanMatchesTheGenerator(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	if err := exec.Command(py, "-c", "import numpy").Run(); err != nil {
		t.Skip("numpy is not installed")
	}
	dir := t.TempDir()
	out, err := exec.Command(py, "-I", filepath.Join("..", "..", "..", "lab", "colosseum", "colosseum.py"), dir).CombinedOutput()
	if err != nil {
		t.Fatalf("generator: %v\n%s", err, out)
	}
	var want []byte
	for _, name := range []string{"clear.cmds", "build.cmds"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, b...)
	}
	zr, err := gzip.NewReader(bytes.NewReader(planGz))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("colosseum.cmds.gz differs from the generator's output; run go generate ./internal/repair/colosseum")
	}
	layout, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(layout, layoutJSON) {
		t.Error("layout.json differs from the generator's output; run go generate ./internal/repair/colosseum")
	}
}
