package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unset GITHUB_OUTPUT is the normal case outside Actions, and must not be an
// error: the check is run by hand often enough that failing here would make
// local use impossible.
func TestEmitIsQuietOutsideActions(t *testing.T) {
	t.Setenv("GITHUB_OUTPUT", "")

	if err := emit(map[string]string{"status": "match"}); err != nil {
		t.Fatalf("emit with no GITHUB_OUTPUT: %v", err)
	}
}

func TestEmitAppendsRatherThanTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	// Actions hands the same file to every step, so a step that truncated it
	// would erase what earlier ones reported.
	if err := os.WriteFile(path, []byte("earlier=kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_OUTPUT", path)

	if err := emit(map[string]string{"status": "match", "changed": "false"}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"earlier=kept", "status=match", "changed=false"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("output file is missing %q; got:\n%s", want, got)
		}
	}
}

// The regression. emit used to open the file, ignore every write error and
// defer the close, so a step output that could not be written looked exactly
// like one that had been. The workflow then read an empty `status` and
// branched on it as though the check had answered.
func TestEmitReportsAnUnwritableOutputFile(t *testing.T) {
	// A directory, because opening one for writing fails on every platform
	// this runs on and needs no permission games that root would defeat.
	path := t.TempDir()
	t.Setenv("GITHUB_OUTPUT", path)

	err := emit(map[string]string{"status": "match"})
	if err == nil {
		t.Fatal("emit reported success for an output file it could not open")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error should name the path it failed on, got %q", err)
	}
}

// A path that does not exist is the shape a mis-set GITHUB_OUTPUT takes, and
// it must be as loud as an unwritable one: emit opens without O_CREATE
// because Actions creates the file, so a missing one means the variable is
// pointing somewhere wrong.
func TestEmitReportsAMissingOutputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does", "not", "exist")
	t.Setenv("GITHUB_OUTPUT", path)

	if err := emit(map[string]string{"status": "match"}); err == nil {
		t.Fatal("emit reported success for an output file that does not exist")
	}
}
