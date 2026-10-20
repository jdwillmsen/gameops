package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePlansMatchesStdoutAndShipsScripts(t *testing.T) {
	dir := t.TempDir()
	if code := writePlans([]string{dir, "signs"}); code != 0 {
		t.Fatalf("writePlans exit %d", code)
	}
	var want bytes.Buffer
	if err := render(&want, courses["signs"]); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "signs.plan"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Error("signs.plan differs from the generator's output")
	}
	for _, name := range []string{"courses.sh", "nightly.sh"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable", name)
		}
	}
}

func TestWritePlansRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	if code := writePlans([]string{dir, "nope"}); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %d files for a rejected request", len(entries))
	}
}
