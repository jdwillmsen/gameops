package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	outSaving   = "[2026-10-01 05:48:29:789 INFO] Saving...\n"
	outNotReady = "[2026-10-01 05:48:25:714 ERROR] A previous save has not been completed.\n"
	outBusy     = "[2026-10-01 05:48:31:684 ERROR] The command is already running\n"
	outResumed  = "[2026-10-01 05:48:39:699 INFO] Changes to the world are resumed.\n"
	readyLine   = "[2026-10-01 05:48:30:684 INFO] Data saved. Files are now ready to be copied.\n"
)

func TestParseSaveQuery(t *testing.T) {
	list := "FWB/db/000003.log:156, FWB/db/CURRENT:16, FWB/level.dat:3007"
	want := []snapshotFile{{"FWB/db/000003.log", 156}, {"FWB/db/CURRENT", 16}, {"FWB/level.dat", 3007}}

	cases := []struct {
		name      string
		out       string
		wantFiles []snapshotFile
		wantReady bool
		wantErr   bool
	}{
		{"ready", readyLine + list + "\n", want, true, false},
		{"ready with carriage returns", strings.ReplaceAll(readyLine+list+"\n", "\n", "\r\n"), want, true, false},
		{"other console lines around it", "[x INFO] Gametime is 5\n" + readyLine + list + "\n[x INFO] Gametime is 6\n", want, true, false},
		{"not ready", outNotReady, nil, false, false},
		{"empty", "", nil, false, false},
		// The list arrives in the same burst as the marker but can straddle
		// the collect window; a line with no newline yet is not the list.
		{"list cut off mid-line", readyLine + "FWB/db/000003.log:156, FWB/db/CUR", nil, false, false},
		{"marker with no list yet", readyLine, nil, false, false},
		{"entry without a size", readyLine + "FWB/db/CURRENT\n", nil, false, true},
		{"non-numeric size", readyLine + "FWB/db/CURRENT:abc\n", nil, false, true},
		{"negative size", readyLine + "FWB/db/CURRENT:-1\n", nil, false, true},
		{"parent traversal", readyLine + "FWB/../../etc/passwd:5\n", nil, false, true},
		{"absolute path", readyLine + "/etc/passwd:5\n", nil, false, true},
		{"unclean path", readyLine + "FWB//db/CURRENT:16\n", nil, false, true},
		{"reserved name", readyLine + "snapshot.json:16\n", nil, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, ready, err := parseSaveQuery(c.out)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if ready != c.wantReady || !reflect.DeepEqual(files, c.wantFiles) {
				t.Fatalf("got %v ready=%v, want %v ready=%v", files, ready, c.wantFiles, c.wantReady)
			}
		})
	}
}

// fakeSaveConsole answers the three save commands from scripted outputs and
// records what it was sent, in order.
type fakeSaveConsole struct {
	mu      sync.Mutex
	sent    []string
	hold    string
	queries []string // consumed in order; the last one repeats
	resume  string
	errOn   map[string]error
	onQuery func()
	// down makes every command fail as it does while the websocket is being
	// redialled.
	down bool
	// cancelled records commands that arrived on an already-cancelled
	// context, which a real console write would turn into a closed
	// connection.
	cancelled []string
}

func (f *fakeSaveConsole) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeSaveConsole) SendCommand(ctx context.Context, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, cmd)
	if ctx.Err() != nil {
		f.cancelled = append(f.cancelled, cmd)
	}
	if f.down {
		return "", ErrNotConnected
	}
	if err := f.errOn[cmd]; err != nil {
		// A failed command can still have collected output before it failed.
		if cmd == "save hold" {
			return f.hold, err
		}
		return "", err
	}
	switch cmd {
	case "save hold":
		return f.hold, nil
	case "save query":
		if f.onQuery != nil {
			f.onQuery()
		}
		out := f.queries[0]
		if len(f.queries) > 1 {
			f.queries = f.queries[1:]
		}
		return out, nil
	case "save resume":
		return f.resume, nil
	}
	return "", errors.New("unexpected command " + cmd)
}

func (f *fakeSaveConsole) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *fakeSaveConsole) count(cmd string) int {
	n := 0
	for _, c := range f.commands() {
		if c == cmd {
			n++
		}
	}
	return n
}

// snapshotFixture is a worlds directory with one world whose files are longer
// on disk than the lengths the console reports, the way a live world's are
// while the server keeps appending.
func snapshotFixture(t *testing.T) (worlds string, queryOut string) {
	t.Helper()
	worlds = t.TempDir()
	files := map[string]string{
		"FWB/db/000001.ldb":      "old-table",
		"FWB/db/000002.ldb":      "new-table",
		"FWB/db/000003.log":      "log-bytes-AND-UNSAVED-TAIL",
		"FWB/db/CURRENT":         "MANIFEST-000002\n",
		"FWB/db/MANIFEST-000002": "manifest",
		"FWB/level.dat":          "leveldat",
	}
	for name, body := range files {
		p := filepath.Join(worlds, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	queryOut = readyLine + "FWB/db/000001.ldb:9, FWB/db/000002.ldb:9, FWB/db/000003.log:9, FWB/db/CURRENT:16, FWB/db/MANIFEST-000002:8, FWB/level.dat:8\n"
	return worlds, queryOut
}

func testSnapshotter(console commandSender, worlds string) *snapshotter {
	s := newSnapshotter(console, worlds, 5*time.Second, testLogger())
	s.readyTimeout = 500 * time.Millisecond
	s.queryEvery = time.Millisecond
	s.resumeEvery = time.Millisecond
	return s
}

type tarEntry struct {
	name string
	body string
}

func readTar(t *testing.T, r io.Reader) []tarEntry {
	t.Helper()
	var entries []tarEntry
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			// A truncated stream is a legitimate outcome some tests assert on.
			return append(entries, tarEntry{name: "<error>", body: err.Error()})
		}
		body, _ := io.ReadAll(tr)
		entries = append(entries, tarEntry{hdr.Name, string(body)})
	}
}

func entryNames(entries []tarEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}

func snapshotMux(s *snapshotter) http.Handler {
	return newMux(&server{cfg: Config{BridgeToken: "tok"}, console: testConsole(), snapshots: s, logger: testLogger()})
}

func postSnapshot(t *testing.T, mux http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/snapshot", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The whole contract in one pass: saving is paused, the manifest comes first,
// immutable tables the caller already holds are skipped, every file is cut to
// the length the console reported, the completion marker comes last, and
// saving is resumed.
func TestSnapshot_StreamsOnlyWhatTheCallerLacksAndResumes(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{outNotReady, queryOut}, resume: outResumed}
	mux := snapshotMux(testSnapshotter(console, worlds))

	rec := postSnapshot(t, mux, `{"have":{"FWB/db/000001.ldb":9,"FWB/db/000002.ldb":4,"FWB/db/000003.log":9}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-tar" {
		t.Errorf("Content-Type = %q", ct)
	}
	entries := readTar(t, rec.Body)
	wantNames := []string{"snapshot.json", "FWB/db/000002.ldb", "FWB/db/000003.log", "FWB/db/CURRENT", "FWB/db/MANIFEST-000002", "FWB/level.dat", "snapshot.ok"}
	if got := entryNames(entries); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("entries = %v\nwant      %v", got, wantNames)
	}

	var manifest snapshotManifest
	if err := json.Unmarshal([]byte(entries[0].body), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Files) != 6 || manifest.Files[0] != (snapshotFile{"FWB/db/000001.ldb", 9}) {
		t.Errorf("manifest lists %+v, want all six files including the skipped one", manifest.Files)
	}
	for _, e := range entries {
		if e.name == "FWB/db/000003.log" && e.body != "log-bytes" {
			t.Errorf("log body = %q, want it cut to the 9 bytes the console reported", e.body)
		}
	}

	want := []string{"save hold", "save query", "save query", "save resume"}
	if got := console.commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("console saw %v, want %v", got, want)
	}
}

// Something else (the nightly backup, the census) already paused saving.
// Resuming here would unfreeze the world under their copy.
func TestSnapshot_HoldAlreadyActiveIsRefusedWithoutResuming(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outBusy, queries: []string{queryOut}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if got := console.commands(); !reflect.DeepEqual(got, []string{"save hold"}) {
		t.Errorf("console saw %v, want only the refused hold", got)
	}
}

func TestSnapshot_NeverReadyTimesOutAndResumes(t *testing.T) {
	worlds, _ := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{outNotReady}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
	if console.count("save resume") != 1 {
		t.Errorf("console saw %v, want exactly one resume", console.commands())
	}
}

// A file shorter than the console said it would be means the copy is not the
// consistent point-in-time set, so the stream must end without the marker.
func TestSnapshot_ShortFileEndsWithoutTheCompletionMarkerAndResumes(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	if err := os.WriteFile(filepath.Join(worlds, "FWB", "level.dat"), []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	console := &fakeSaveConsole{hold: outSaving, queries: []string{queryOut}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	for _, name := range entryNames(readTar(t, rec.Body)) {
		if name == "snapshot.ok" {
			t.Fatal("stream carried snapshot.ok although level.dat was short")
		}
	}
	if console.count("save resume") != 1 {
		t.Errorf("console saw %v, want exactly one resume", console.commands())
	}
}

func TestSnapshot_MalformedFileListFailsAndResumes(t *testing.T) {
	worlds, _ := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{readyLine + "FWB/../secret:4\n"}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if console.count("save resume") != 1 {
		t.Errorf("console saw %v, want exactly one resume", console.commands())
	}
}

func TestSnapshot_SecondCallerIsRefusedWhileOneIsRunning(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	inQuery := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	console := &fakeSaveConsole{hold: outSaving, queries: []string{queryOut}, resume: outResumed}
	console.onQuery = func() {
		once.Do(func() {
			close(inQuery)
			<-release
		})
	}
	mux := snapshotMux(testSnapshotter(console, worlds))

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- postSnapshot(t, mux, `{}`) }()
	<-inQuery

	if rec := postSnapshot(t, mux, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("concurrent snapshot = %d, want 409", rec.Code)
	}
	close(release)
	if rec := <-done; rec.Code != http.StatusOK {
		t.Errorf("first snapshot = %d, want 200", rec.Code)
	}
	if console.count("save hold") != 1 || console.count("save resume") != 1 {
		t.Errorf("console saw %v, want one hold and one resume", console.commands())
	}
}

// The first resume can be lost to a console hiccup; a world left paused keeps
// every change in memory until the next restart.
func TestSnapshot_ResumeIsRetriedUntilTheServerConfirms(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &resumeFlakyConsole{fakeSaveConsole: fakeSaveConsole{hold: outSaving, queries: []string{queryOut}}, failFirst: 2}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := console.count("save resume"); got != 3 {
		t.Errorf("resume was sent %d times, want 3 (two lost, one confirmed)", got)
	}
}

type resumeFlakyConsole struct {
	fakeSaveConsole
	failFirst int
}

func (f *resumeFlakyConsole) SendCommand(ctx context.Context, cmd string) (string, error) {
	out, err := f.fakeSaveConsole.SendCommand(ctx, cmd)
	if cmd != "save resume" {
		return out, err
	}
	if f.count("save resume") <= f.failFirst {
		return "", nil
	}
	return outResumed, nil
}

func TestSnapshot_ConsoleDownIs502AndSendsNothingElse(t *testing.T) {
	worlds, _ := snapshotFixture(t)
	console := &fakeSaveConsole{errOn: map[string]error{"save hold": ErrNotConnected}}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if got := console.commands(); !reflect.DeepEqual(got, []string{"save hold"}) {
		t.Errorf("console saw %v, want only the failed hold", got)
	}
}

func TestSnapshot_RequiresTheBearerToken(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{queryOut}, resume: outResumed}
	mux := snapshotMux(testSnapshotter(console, worlds))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/snapshot", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(console.commands()) != 0 {
		t.Errorf("console saw %v before authentication", console.commands())
	}
}

func TestSnapshot_BadBodyIs400BeforeAnyHold(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{queryOut}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{"have":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(console.commands()) != 0 {
		t.Errorf("console saw %v for a request it could not parse", console.commands())
	}
}

// The reply to `save hold` has no correlation id and can land after the
// collect window. An empty reply is therefore not an acknowledgement: the
// refusal can still arrive, and when it does the pause is someone else's.
func TestSnapshot_LateRefusalOfTheHoldIsStillSomeoneElsesPause(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: "", queries: []string{outBusy + queryOut}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if console.count("save resume") != 0 {
		t.Errorf("console saw %v: resumed a pause that belongs to another job", console.commands())
	}
}

// Another job's `save hold` landing inside this snapshot is refused by the
// server, but that job does not check: it goes on to copy under this pause
// and resumes when it is done. Resuming here would unfreeze the world under
// its copy, so the snapshot stops and leaves the resume to that job.
func TestSnapshot_ContestedHoldStopsWithoutResuming(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	for name, console := range map[string]*fakeSaveConsole{
		"refusal beside the acknowledgement": {hold: outSaving + outBusy, queries: []string{queryOut}, resume: outResumed},
		"refusal while waiting for the save": {hold: outSaving, queries: []string{outNotReady + outBusy, queryOut}, resume: outResumed},
	} {
		t.Run(name, func(t *testing.T) {
			rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", rec.Code)
			}
			if console.count("save resume") != 0 {
				t.Errorf("console saw %v, want no resume", console.commands())
			}
		})
	}
}

// Only a line the server itself printed counts. A line that merely contains
// the words (a player name, a script's output) is not the server speaking.
func TestSnapshot_OnlyWholeServerLinesAreTreatedAsItsAnswers(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	noise := "[2026-10-01 05:48:31:684 INFO] [Scripting] The command is already running\n" +
		"[2026-10-01 05:48:31:684 INFO] Player connected: The command is already running, xuid: 1\n"
	console := &fakeSaveConsole{hold: outSaving + noise, queries: []string{noise + queryOut}, resume: outResumed}

	rec := postSnapshot(t, snapshotMux(testSnapshotter(console, worlds)), `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: lookalike lines were taken for the server refusing the hold", rec.Code)
	}
	if console.count("save resume") != 1 {
		t.Errorf("console saw %v, want one resume", console.commands())
	}
}

func TestParseSaveQuery_ReadyMarkerMustBeAServerLine(t *testing.T) {
	out := "[2026-10-01 05:48:30:684 INFO] [Scripting] Data saved. Files are now ready to be copied.\nFWB/level.dat:1\n"
	if files, ready, err := parseSaveQuery(out); ready || err != nil || files != nil {
		t.Errorf("a script line was taken for the server's: %v %v %v", files, ready, err)
	}
}

// A failed `save hold` may still have reached the server, so it is resumed,
// unless what did come back shows the pause is someone else's.
func TestSnapshot_FailedHoldIsResumedUnlessItWasRefused(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	boom := errors.New("write timed out")

	ours := &fakeSaveConsole{hold: "", queries: []string{queryOut}, resume: outResumed, errOn: map[string]error{"save hold": boom}}
	if rec := postSnapshot(t, snapshotMux(testSnapshotter(ours, worlds)), `{}`); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if ours.count("save resume") != 1 {
		t.Errorf("console saw %v, want the possibly-applied hold resumed", ours.commands())
	}

	theirs := &fakeSaveConsole{hold: outBusy, queries: []string{queryOut}, resume: outResumed, errOn: map[string]error{"save hold": boom}}
	if rec := postSnapshot(t, snapshotMux(testSnapshotter(theirs, worlds)), `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if theirs.count("save resume") != 0 {
		t.Errorf("console saw %v, want no resume of a refused hold", theirs.commands())
	}
}

// The console connection can drop for a second while the server keeps
// running and keeps the pause. Giving up there would leave the world unsaved
// and make every later snapshot see a pause it believes is someone else's.
func TestSnapshot_ResumeLostToADroppedConsoleIsOwedAndPaidOnReconnect(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{queryOut}, resume: outResumed}
	console.onQuery = func() { console.down = true } // already under the fake's lock
	s := testSnapshotter(console, worlds)
	mux := snapshotMux(s)

	postSnapshot(t, mux, `{}`)
	if !s.resumeOwed() {
		t.Fatal("the resume could not be sent, yet nothing records that it is still owed")
	}

	// Still down: a new snapshot must not pause again on top of the old pause.
	if rec := postSnapshot(t, mux, `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("snapshot while a resume is owed = %d, want 503", rec.Code)
	}
	if console.count("save hold") != 1 {
		t.Errorf("console saw %v: a second hold was sent while a resume was owed", console.commands())
	}

	console.setDown(false)
	console.onQuery = nil
	s.settle()
	if s.resumeOwed() {
		t.Fatal("the console is back and the resume is still owed")
	}
	if rec := postSnapshot(t, mux, `{}`); rec.Code != http.StatusOK {
		t.Errorf("snapshot after the debt was paid = %d, want 200", rec.Code)
	}
}

// A resume the server answers with "nothing is held" settles the debt too:
// that is what a server that restarted in the meantime says.
func TestSnapshot_NothingHeldSettlesAnOwedResume(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{queryOut}, resume: outNotReady}
	s := testSnapshotter(console, worlds)

	if rec := postSnapshot(t, snapshotMux(s), `{}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if s.resumeOwed() || console.count("save resume") != 1 {
		t.Errorf("owed=%v after %v", s.resumeOwed(), console.commands())
	}
}

// A caller that goes away must not take the console down with it: a console
// write on a cancelled context closes the whole websocket, at the moment the
// resume most needs it.
func TestSnapshot_CallerGoingAwayStillResumesOnALiveContext(t *testing.T) {
	worlds, _ := snapshotFixture(t)
	console := &fakeSaveConsole{hold: outSaving, queries: []string{outNotReady}, resume: outResumed}
	ctx, cancel := context.WithCancel(context.Background())
	console.onQuery = cancel
	s := testSnapshotter(console, worlds)
	s.readyTimeout = 5 * time.Second

	req := httptest.NewRequest("POST", "/snapshot", strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	snapshotMux(s).ServeHTTP(httptest.NewRecorder(), req)

	if console.count("save resume") != 1 || s.resumeOwed() {
		t.Fatalf("console saw %v, owed=%v: want one confirmed resume", console.commands(), s.resumeOwed())
	}
	if len(console.cancelled) != 0 {
		t.Errorf("%v were sent on a cancelled context", console.cancelled)
	}
}

// Shutting down mid-snapshot must end with saving resumed, and must not
// start another.
func TestSnapshot_CloseResumesAndRefusesNewWork(t *testing.T) {
	worlds, queryOut := snapshotFixture(t)
	inQuery := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	console := &fakeSaveConsole{hold: outSaving, queries: []string{outNotReady}, resume: outResumed}
	s := testSnapshotter(console, worlds)
	s.readyTimeout = 30 * time.Second
	console.onQuery = func() {
		once.Do(func() {
			close(inQuery)
			<-release
		})
	}
	mux := snapshotMux(s)

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- postSnapshot(t, mux, `{}`) }()
	<-inQuery

	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	close(release)
	<-done
	<-closed

	if console.count("save resume") != 1 || s.resumeOwed() {
		t.Fatalf("console saw %v, owed=%v", console.commands(), s.resumeOwed())
	}
	console.queries = []string{queryOut}
	if rec := postSnapshot(t, mux, `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("snapshot after Close = %d, want 503", rec.Code)
	}
	if console.count("save hold") != 1 {
		t.Errorf("console saw %v: a hold was sent after Close", console.commands())
	}
}
