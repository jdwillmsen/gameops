package repair

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

func tarOf(t *testing.T, files map[string]string, w io.Writer) {
	t.Helper()
	tw := tar.NewWriter(w)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
}

func snapshotter(t *testing.T, c *arena.FakeConsole, files map[string]string) Snapshotter {
	return Snapshotter{
		Console: c, Tar: []string{"tar"}, Sleep: func(time.Duration) {},
		Start: func(_ context.Context, args []string, w io.Writer) error { tarOf(t, files, w); return nil },
	}
}

func readyConsole() *arena.FakeConsole {
	return &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "save query" {
			return "Data saved. Files are now ready to be copied.\nworlds/crew-lab/db/CURRENT:11, worlds/crew-lab/db/000001.ldb:1", nil
		}
		return "", nil
	}}
}

func TestFetchHoldsCopiesAndResumes(t *testing.T) {
	c := readyConsole()
	dest := t.TempDir()
	snap, err := snapshotter(t, c, map[string]string{"db/CURRENT": "MANIFEST-1\n", "db/000001.ldb": "x"}).Fetch(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(snap.Dir, "CURRENT")); string(got) != "MANIFEST-1\n" {
		t.Errorf("CURRENT = %q", got)
	}
	want := []string{"save hold", "save query", "save resume"}
	if strings.Join(c.Cmds, "|") != strings.Join(want, "|") {
		t.Errorf("console saw %v, want %v", c.Cmds, want)
	}
}

func TestFetchResumesWhenTheCopyFails(t *testing.T) {
	c := readyConsole()
	s := snapshotter(t, c, nil)
	s.Start = func(context.Context, []string, io.Writer) error { return errors.New("pod gone") }
	if _, err := s.Fetch(context.Background(), t.TempDir()); err == nil {
		t.Fatal("want an error")
	}
	if c.Count("save resume") != 1 {
		t.Errorf("saving must be resumed after a failed copy: %v", c.Cmds)
	}
}

func TestFetchGivesUpWhenFilesNeverSettle(t *testing.T) {
	c := &arena.FakeConsole{}
	_, err := snapshotter(t, c, nil).Fetch(context.Background(), t.TempDir())
	if !errors.Is(err, ErrSnapshotNotReady) {
		t.Fatalf("err = %v", err)
	}
	if c.Count("save query") != holdTries || c.Count("save resume") != 1 {
		t.Errorf("queries %d, resumes %d", c.Count("save query"), c.Count("save resume"))
	}
}

func TestFetchRefusesEntriesOutsideDB(t *testing.T) {
	for _, name := range []string{"../evil", "db/../../evil", "other/file", "db/sub/file"} {
		c := readyConsole()
		dest := t.TempDir()
		if _, err := snapshotter(t, c, map[string]string{name: "x"}).Fetch(context.Background(), dest); err == nil {
			t.Errorf("%q was accepted", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil")); err == nil {
			t.Errorf("%q escaped the destination", name)
		}
	}
}

func TestParseSaveQuery(t *testing.T) {
	reply := "[2026-10-27 12:00:00:000 INFO] Data saved. Files are now ready to be copied.\n" +
		"[2026-10-27 12:00:00:001 INFO] crew-lab/db/000005.ldb:1234, crew-lab/db/CURRENT:16, crew-lab/db/MANIFEST-000004:258, crew-lab/level.dat:2000"
	got := ParseSaveQuery(reply)
	want := map[string]int64{"000005.ldb": 1234, "CURRENT": 16, "MANIFEST-000004": 258, "level.dat": 2000}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if ParseSaveQuery("Data saved. Files are now ready to be copied.") != nil {
		t.Error("a reply with no list must parse to nothing")
	}
}

func TestFetchTruncatesToListedLengths(t *testing.T) {
	c := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "save query" {
			return "Data saved. Files are now ready to be copied.\ncrew-lab/db/000007.log:4, crew-lab/db/CURRENT:3", nil
		}
		return "", nil
	}}
	snap, err := snapshotter(t, c, map[string]string{"db/000007.log": "abcdefgh", "db/CURRENT": "abc", "db/000009.ldb": "unlisted"}).Fetch(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(snap.Dir, "000007.log")); string(got) != "abcd" {
		t.Errorf("log = %q, want it cut to 4 bytes", got)
	}
	if got, _ := os.ReadFile(filepath.Join(snap.Dir, "CURRENT")); string(got) != "abc" {
		t.Errorf("CURRENT = %q", got)
	}
}

func TestFetchRejectsACopyShorterThanListed(t *testing.T) {
	c := readyConsole() // lists CURRENT:11 and 000001.ldb:1
	if _, err := snapshotter(t, c, map[string]string{"db/CURRENT": "short", "db/000001.ldb": "x"}).Fetch(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a file shorter than listed must fail")
	}
	if _, err := snapshotter(t, readyConsole(), map[string]string{"db/CURRENT": "12345678901"}).Fetch(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a listed file missing from the copy must fail")
	}
	if c.Count("save resume") != 1 {
		t.Errorf("resume: %v", c.Cmds)
	}
}

func TestFetchWaitsForTheFileList(t *testing.T) {
	n := 0
	c := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd != "save query" {
			return "", nil
		}
		n++
		if n == 1 {
			return "Data saved. Files are now ready to be copied.", nil
		}
		return "Data saved. Files are now ready to be copied.\ncrew-lab/db/CURRENT:3", nil
	}}
	if _, err := snapshotter(t, c, map[string]string{"db/CURRENT": "abc"}).Fetch(context.Background(), t.TempDir()); err != nil || n != 2 {
		t.Fatalf("queries %d, err %v", n, err)
	}
}

func TestFetchCancelledStopsPollingAndResumes(t *testing.T) {
	c := readyConsole()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := snapshotter(t, c, nil).Fetch(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) || c.Count("save query") != 0 || c.Count("save resume") != 1 {
		t.Errorf("err %v, commands %v", err, c.Cmds)
	}
}

func TestFetchKeepsBothErrorsWhenResumeFails(t *testing.T) {
	c := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "save resume" {
			return "", errors.New("resume boom")
		}
		return "", nil
	}}
	s := snapshotter(t, c, nil)
	s.Sleep = func(time.Duration) {}
	_, err := s.Fetch(context.Background(), t.TempDir())
	if !errors.Is(err, ErrSnapshotNotReady) || !strings.Contains(err.Error(), "resume boom") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchClearsAnEarlierCopy(t *testing.T) {
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dest, "db", "000999.ldb")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotter(t, readyConsole(), map[string]string{"db/CURRENT": "12345678901", "db/000001.ldb": "x"}).Fetch(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a file of the earlier copy survived")
	}
}

func TestFetchDrainsTarPaddingAfterTheArchive(t *testing.T) {
	s := snapshotter(t, readyConsole(), map[string]string{"db/CURRENT": "MANIFEST-1\n", "db/000001.ldb": "x"})
	s.Start = func(_ context.Context, _ []string, w io.Writer) error {
		tarOf(t, map[string]string{"db/CURRENT": "MANIFEST-1\n", "db/000001.ldb": "x"}, w)
		// GNU tar pads to a 10 KiB record; the reader never asks for it.
		_, err := w.Write(make([]byte, 10240))
		return err
	}
	errc := make(chan error, 1)
	go func() { _, err := s.Fetch(context.Background(), t.TempDir()); errc <- err }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Fetch blocked on the unread tar padding")
	}
}

func TestExtractDBRefusesUnsafeEntries(t *testing.T) {
	cases := []tar.Header{
		{Name: "/etc/evil", Typeflag: tar.TypeReg},
		{Name: "/db/evil", Typeflag: tar.TypeReg},
		{Name: "db/../../evil", Typeflag: tar.TypeReg},
		{Name: `db\..\evil`, Typeflag: tar.TypeReg},
		{Name: "db/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "db/hard", Typeflag: tar.TypeLink, Linkname: "db/other"},
	}
	for _, h := range cases {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		parent := t.TempDir()
		dest := filepath.Join(parent, "dest")
		if err := os.Mkdir(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := extractDB(&buf, dest); err == nil {
			t.Errorf("%q (type %c) was accepted", h.Name, h.Typeflag)
		}
		if _, err := os.Lstat(filepath.Join(parent, "evil")); err == nil {
			t.Errorf("%q escaped the destination", h.Name)
		}
	}
}
