package mirror

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type entry struct {
	name string
	body string
}

// fakeBridge serves one scripted snapshot and records the inventory the
// mirror sent.
type fakeBridge struct {
	status  int
	entries []entry
	have    map[string]int64
	auth    string
	calls   int
}

func (b *fakeBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.calls++
	b.auth = r.Header.Get("Authorization")
	var req struct {
		Have map[string]int64 `json:"have"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	b.have = req.Have
	if b.status != 0 {
		http.Error(w, "refused", b.status)
		return
	}
	tw := tar.NewWriter(w)
	for _, e := range b.entries {
		_ = tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))})
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
}

func manifest(files ...entry) string {
	type f struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var m struct {
		Files []f `json:"files"`
	}
	for _, e := range files {
		m.Files = append(m.Files, f{e.name, int64(len(e.body))})
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func newMirror(t *testing.T, b *fakeBridge) *Mirror {
	t.Helper()
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	return &Mirror{
		Root:   t.TempDir(),
		URL:    srv.URL,
		Token:  "tok",
		HTTP:   srv.Client(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

var (
	table1  = entry{"FWB/db/000001.ldb", "table-one"}
	table2  = entry{"FWB/db/000002.ldb", "table-two"}
	logFile = entry{"FWB/db/000003.log", "log"}
	level   = entry{"FWB/level.dat", "level"}
	current = entry{"FWB/db/CURRENT", "MANIFEST-000002\n"}
)

func TestSync_FirstRunWritesTheWholeWorld(t *testing.T) {
	b := &fakeBridge{entries: []entry{
		{"snapshot.json", manifest(table1, logFile, level, current)}, table1, logFile, level, current, {"snapshot.ok", ""},
	}}
	m := newMirror(t, b)

	stats, err := m.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{table1.name: table1.body, logFile.name: logFile.body, level.name: level.body, current.name: current.body}
	if got := tree(t, m.Root); !reflect.DeepEqual(got, want) {
		t.Errorf("mirror = %v, want %v", got, want)
	}
	if stats.Files != 4 || stats.Fetched != 4 || stats.Bytes != int64(len(table1.body)+len(logFile.body)+len(level.body)+len(current.body)) {
		t.Errorf("stats = %+v", stats)
	}
	if b.auth != "Bearer tok" {
		t.Errorf("Authorization = %q", b.auth)
	}
	if len(b.have) != 0 {
		t.Errorf("an empty mirror claimed to have %v", b.have)
	}
}

// The second run is the point of the design: tables already held are named
// in the inventory, are not re-sent, and stay; a table the server compacted
// away is removed; rewritten files are replaced.
func TestSync_SendsItsTablesKeepsSkippedOnesAndDropsVanishedOnes(t *testing.T) {
	newLog := entry{"FWB/db/000003.log", "log-grown"}
	b := &fakeBridge{entries: []entry{
		{"snapshot.json", manifest(table2, newLog, level, current)}, newLog, level, current, {"snapshot.ok", ""},
	}}
	m := newMirror(t, b)
	write(t, m.Root, table1.name, table1.body)
	write(t, m.Root, table2.name, table2.body)
	write(t, m.Root, logFile.name, logFile.body)

	stats, err := m.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantHave := map[string]int64{table1.name: int64(len(table1.body)), table2.name: int64(len(table2.body))}
	if !reflect.DeepEqual(b.have, wantHave) {
		t.Errorf("inventory = %v, want only the table files %v", b.have, wantHave)
	}
	want := map[string]string{table2.name: table2.body, newLog.name: newLog.body, level.name: level.body, current.name: current.body}
	if got := tree(t, m.Root); !reflect.DeepEqual(got, want) {
		t.Errorf("mirror = %v, want %v", got, want)
	}
	if stats.Fetched != 3 || stats.Removed != 1 {
		t.Errorf("stats = %+v, want 3 fetched and 1 removed", stats)
	}
}

// A stream that ends without snapshot.ok is a failed copy on the bridge's
// side. Applying any of it would mix two points in time.
func TestSync_StreamWithoutTheCompletionMarkerChangesNothing(t *testing.T) {
	newLog := entry{"FWB/db/000003.log", "log-grown"}
	b := &fakeBridge{entries: []entry{{"snapshot.json", manifest(table1, newLog, current)}, newLog, current}}
	m := newMirror(t, b)
	write(t, m.Root, table1.name, table1.body)
	write(t, m.Root, logFile.name, logFile.body)
	before := tree(t, m.Root)

	if _, err := m.Sync(context.Background()); err == nil {
		t.Fatal("Sync accepted a stream with no completion marker")
	}
	if got := tree(t, m.Root); !reflect.DeepEqual(got, before) {
		t.Errorf("mirror changed to %v after a failed sync", got)
	}
}

func TestSync_RefusalIsErrBusyAndChangesNothing(t *testing.T) {
	b := &fakeBridge{status: http.StatusConflict}
	m := newMirror(t, b)
	write(t, m.Root, table1.name, table1.body)

	if _, err := m.Sync(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if got := tree(t, m.Root); len(got) != 1 {
		t.Errorf("mirror = %v", got)
	}
}

func TestSync_RejectsStreamsItCannotTrust(t *testing.T) {
	cases := map[string][]entry{
		"no manifest first":          {table1, {"snapshot.ok", ""}},
		"file not in the manifest":   {{"snapshot.json", manifest(level)}, level, table1, {"snapshot.ok", ""}},
		"size differs from manifest": {{"snapshot.json", manifest(level)}, {level.name, "longer-than-listed"}, {"snapshot.ok", ""}},
		"path escapes the mirror":    {{"snapshot.json", `{"files":[{"name":"FWB/../../evil","size":1}]}`}, {"FWB/../../evil", "x"}, {"snapshot.ok", ""}},
		"absolute path":              {{"snapshot.json", `{"files":[{"name":"/etc/evil","size":1}]}`}, {"/etc/evil", "x"}, {"snapshot.ok", ""}},
		"skipped file it never had":  {{"snapshot.json", manifest(table1, level)}, level, {"snapshot.ok", ""}},
		"server error":               nil,
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			b := &fakeBridge{entries: entries}
			if entries == nil {
				b.status = http.StatusBadGateway
			}
			m := newMirror(t, b)
			_, err := m.Sync(context.Background())
			if err == nil || errors.Is(err, ErrBusy) {
				t.Fatalf("err = %v, want a hard failure", err)
			}
			got := tree(t, m.Root)
			var names []string
			for n := range got {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) != 0 {
				t.Errorf("mirror holds %v after a rejected stream", names)
			}
			parent := filepath.Dir(m.Root)
			if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
				t.Error("a file was written outside the mirror")
			}
		})
	}
}

func TestSync_LeftoverStagingFromACrashIsIgnored(t *testing.T) {
	b := &fakeBridge{entries: []entry{{"snapshot.json", manifest(level, current)}, level, current, {"snapshot.ok", ""}}}
	m := newMirror(t, b)
	write(t, m.Root, stagingDir+"/FWB/db/999999.ldb", "half-written")

	if _, err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name := range b.have {
		if strings.Contains(name, "999999") {
			t.Errorf("inventory offered a staged file: %v", b.have)
		}
	}
	if got := tree(t, m.Root); !reflect.DeepEqual(got, map[string]string{level.name: level.body, current.name: current.body}) {
		t.Errorf("mirror = %v", got)
	}
}

// A manifest with no database in it is not a world. Applying it would prune
// the entire mirror, and the next snapshot would have to copy everything
// while the server's saving is paused.
func TestSync_ManifestWithoutAWorldDatabaseChangesNothing(t *testing.T) {
	for name, entries := range map[string][]entry{
		"empty":            {{"snapshot.json", `{"files":[]}`}, {"snapshot.ok", ""}},
		"no CURRENT entry": {{"snapshot.json", manifest(level)}, level, {"snapshot.ok", ""}},
	} {
		t.Run(name, func(t *testing.T) {
			m := newMirror(t, &fakeBridge{entries: entries})
			write(t, m.Root, table1.name, table1.body)
			if _, err := m.Sync(context.Background()); err == nil {
				t.Fatal("Sync applied a snapshot with no world database in it")
			}
			if got := tree(t, m.Root); len(got) != 1 {
				t.Errorf("mirror = %v", got)
			}
		})
	}
}

// The retained generations are hard links to the files here, so a file the
// server has changed must arrive as a new one that replaces the old name,
// never as a rewrite of the file a generation is still holding.
func TestSync_ReplacesAChangedFileRatherThanRewritingIt(t *testing.T) {
	changed := entry{level.name, "level-after"}
	b := &fakeBridge{entries: []entry{
		{"snapshot.json", manifest(table1, level, current)}, table1, level, current, {"snapshot.ok", ""},
	}}
	m := newMirror(t, b)
	if _, err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(m.local(level.name))
	if err != nil {
		t.Fatal(err)
	}
	// A second copy of the world, as a generation holds it.
	kept := filepath.Join(t.TempDir(), "kept")
	if err := os.Link(m.local(level.name), kept); err != nil {
		t.Fatal(err)
	}

	b.entries = []entry{
		{"snapshot.json", manifest(table1, changed, current)}, changed, current, {"snapshot.ok", ""},
	}
	if _, err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if tree(t, m.Root)[level.name] != changed.body {
		t.Fatalf("the mirror did not take the new file: %v", tree(t, m.Root))
	}
	after, err := os.Stat(m.local(level.name))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("the mirror rewrote the file in place, which changes every generation holding it")
	}
	if body, err := os.ReadFile(kept); err != nil || string(body) != level.body {
		t.Errorf("the copy taken before the change now reads %q (%v)", body, err)
	}
}
