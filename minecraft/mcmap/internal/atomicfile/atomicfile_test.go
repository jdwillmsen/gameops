package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite_LeavesTheWholeFileAndNoLitter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "made")
	path := filepath.Join(dir, "f.json")
	for _, body := range []string{"first", "second, longer than the first"} {
		if err := Write(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Fatalf("read back %q, want %q", got, body)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files left in the directory, want only the one written", len(entries))
	}
}

func TestWrite_IntoExistingDirectoryReturnsNoError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := Write(path, []byte("body")); err != nil {
		t.Fatalf("Write returned %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "body" {
		t.Fatalf("read back %q, %v", got, err)
	}
}
