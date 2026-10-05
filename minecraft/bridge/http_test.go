package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// testDataServer builds a mux whose DataDir is an empty temp directory, so
// each test controls exactly which data files exist.
func testDataServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	return newMux(&server{
		cfg:     Config{BridgeToken: "tok", DataDir: dir},
		console: testConsole(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}), dir
}

func getAuthed(t *testing.T, mux http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// A data file the server has not written yet is a normal state on a fresh
// volume, not a bridge fault, so it must not present as a 500 that pages
// someone.
func TestDataEndpoints_MissingFileIs404(t *testing.T) {
	mux, _ := testDataServer(t)

	for _, path := range []string{"/permissions", "/allowlist"} {
		if rec := getAuthed(t, mux, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with the file absent = %d, want 404", path, rec.Code)
		}
	}
}

// An open failure that is not "absent" (a bad mount, wrong ownership) is a
// real server-side fault and must stay a 500.
func TestDataEndpoints_UnreadableFileIs500(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 is still readable")
	}
	mux, dir := testDataServer(t)

	for _, tc := range []struct{ path, file string }{
		{"/permissions", "permissions.json"},
		{"/allowlist", "allowlist.json"},
	} {
		if err := os.WriteFile(filepath.Join(dir, tc.file), []byte("[]"), 0o000); err != nil {
			t.Fatalf("write %s: %v", tc.file, err)
		}
		if rec := getAuthed(t, mux, tc.path); rec.Code != http.StatusInternalServerError {
			t.Errorf("GET %s with an unreadable file = %d, want 500", tc.path, rec.Code)
		}
	}
}

func TestDataEndpoints_MalformedFileIs500(t *testing.T) {
	mux, dir := testDataServer(t)

	for _, tc := range []struct{ path, file string }{
		{"/permissions", "permissions.json"},
		{"/allowlist", "allowlist.json"},
	} {
		if err := os.WriteFile(filepath.Join(dir, tc.file), []byte("{not json"), 0o644); err != nil {
			t.Fatalf("write %s: %v", tc.file, err)
		}
		if rec := getAuthed(t, mux, tc.path); rec.Code != http.StatusInternalServerError {
			t.Errorf("GET %s with unparseable contents = %d, want 500", tc.path, rec.Code)
		}
	}
}

func TestDataEndpoints_ValidFileIsServed(t *testing.T) {
	mux, dir := testDataServer(t)

	perms := `[{"permission":"operator","xuid":"2535000000000001"}]`
	if err := os.WriteFile(filepath.Join(dir, "permissions.json"), []byte(perms), 0o644); err != nil {
		t.Fatalf("write permissions.json: %v", err)
	}
	rec := getAuthed(t, mux, "/permissions")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /permissions = %d, want 200", rec.Code)
	}
	if got, want := rec.Body.String(), `{"2535000000000001":"operator"}`; got != want+"\n" {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestHandleEvents_IncludesBackfillFlag(t *testing.T) {
	c := testConsole()
	c.Events.IngestBackfill("Player connected: Steve, xuid: 111", time.Now())
	mux := testServer(t, c)

	rec := getAuthed(t, mux, "/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /events = %d, want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"backfill":true`)) {
		t.Errorf("body = %s, want a backfilled event with \"backfill\":true", rec.Body.String())
	}
}

func postCommand(t *testing.T, mux http.Handler, cmd string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(commandRequest{Command: cmd})
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	req := httptest.NewRequest("POST", "/command", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// kickServer wires the HTTP API to a fake console that records every stdin
// line it receives, so a test can assert what did and did not reach the
// server.
func kickServer(t *testing.T, kickable string) (http.Handler, <-chan string) {
	t.Helper()
	received := make(chan string, 16)
	addr, _ := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, _ *http.Request) {
		writeTestMsg(t, ctx, conn, wsMessage{Type: "logHistory"})
		for {
			msg, err := readTestMsg(ctx, conn)
			if err != nil {
				return
			}
			if msg.Type == "stdin" {
				received <- msg.Data
			}
		}
	})

	k, err := ParseKickable(kickable)
	if err != nil {
		t.Fatalf("ParseKickable: %v", err)
	}
	c := NewConsole(addr, "pw", testOrigin, 2*time.Second, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitConnected(t, c)

	return newMux(&server{
		cfg:     Config{BridgeToken: "tok", Kickable: k},
		console: c,
		logger:  testLogger(),
	}), received
}

func TestCommand_KickReachesConsoleOnlyForActors(t *testing.T) {
	mux, received := kickServer(t, "AfkBotOne,Afk Bot Two")

	rec := postCommand(t, mux, `kick "Afk Bot Two"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("kick of an actor = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var resp commandResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Rule != "kick" {
		t.Errorf("rule = %q, want kick", resp.Rule)
	}
	select {
	case got := <-received:
		if want := "kick \"Afk Bot Two\"\n"; got != want {
			t.Errorf("console received %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an accepted kick never reached the console")
	}

	for _, cmd := range []string{"kick Steve", "kick @a", "kick AfkBotOne reason", "kick AfkBotOne\nstop"} {
		if rec := postCommand(t, mux, cmd); rec.Code != http.StatusForbidden {
			t.Errorf("POST /command %q = %d, want 403", cmd, rec.Code)
		}
	}
	// Refusal happens before the console is touched, so anything received
	// now came from a refused command.
	select {
	case got := <-received:
		t.Errorf("a refused kick reached the console as %q", got)
	default:
	}
}

// A bridge deployed without BRIDGE_KICKABLE must refuse every kick, including
// one naming a real actor's gamertag.
func TestCommand_KickRefusedWithoutKickableList(t *testing.T) {
	mux := testServer(t, testConsole())

	if rec := postCommand(t, mux, "kick AfkBotOne"); rec.Code != http.StatusForbidden {
		t.Errorf("kick with no kickable list = %d, want 403", rec.Code)
	}
}

func getScript(ctx context.Context, mux http.Handler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/script"+query, nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func getScriptInBackground(ctx context.Context, mux http.Handler, query string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- getScript(ctx, mux, query) }()
	return done
}

func decodeScript(t *testing.T, rec *httptest.ResponseRecorder) scriptResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /script = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var resp scriptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return resp
}

func TestScriptEndpointRequiresBearer(t *testing.T) {
	c := testConsole()
	c.Script.Ingest(scriptLine(testScriptPayload), time.Now())
	mux := testServer(t, c)

	for name, header := range map[string]string{"no token": "", "wrong token": "Bearer nope"} {
		req := httptest.NewRequest("GET", "/script", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: GET /script = %d, want 401", name, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("gen")) {
			t.Errorf("%s: an unauthenticated response carried a record: %s", name, rec.Body.String())
		}
	}
}

func TestScriptEndpointReturnsImmediatelyWhenNewRecordsExist(t *testing.T) {
	c := testConsole()
	at := time.Date(2026, 10, 5, 12, 0, 0, 250_000_000, time.UTC)
	c.Script.Ingest(numberedScriptLine(1), at)
	c.Script.Ingest(numberedScriptLine(2), at)
	mux := testServer(t, c)

	start := time.Now()
	rec := getScript(context.Background(), mux, "?since=1&wait=25000")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v with a record already waiting", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /script = %d, want 200", rec.Code)
	}
	if got, want := rec.Body.String(), `{"records":[{"id":2,"at":"2026-10-05T12:00:00.25Z","data":{"gen":2}}]}`+"\n"; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestScriptEndpointBlocksThenReturnsOnArrival(t *testing.T) {
	c := testConsole()
	c.Script.Ingest(numberedScriptLine(1), time.Now())
	mux := testServer(t, c)

	done := getScriptInBackground(context.Background(), mux, "?since=1&wait=25000")
	waitForScriptWaiters(t, c.Script, 1)
	select {
	case rec := <-done:
		t.Fatalf("answered with nothing new: %d %s", rec.Code, rec.Body.String())
	default:
	}

	c.Script.Ingest(numberedScriptLine(2), time.Now())

	select {
	case rec := <-done:
		resp := decodeScript(t, rec)
		if len(resp.Records) != 1 || resp.Records[0].ID != 2 {
			t.Errorf("records = %+v, want only id 2", resp.Records)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a new record did not release the request")
	}
}

func TestScriptEndpointReturnsEmptyWhenTheWaitElapses(t *testing.T) {
	mux := testServer(t, testConsole())

	start := time.Now()
	rec := getScript(context.Background(), mux, "?wait=40")
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("answered after %v, want the full wait", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /script = %d, want 200", rec.Code)
	}
	// An empty list, not null: the reader ranges over it either way, but
	// only one of them is what the contract says.
	if got, want := rec.Body.String(), `{"records":[]}`+"\n"; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestScriptEndpointWithoutAWaitDoesNotBlock(t *testing.T) {
	mux := testServer(t, testConsole())

	start := time.Now()
	rec := getScript(context.Background(), mux, "")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v with no wait asked for", elapsed)
	}
	if got, want := rec.Body.String(), `{"records":[]}`+"\n"; rec.Code != http.StatusOK || got != want {
		t.Errorf("GET /script = %d %s, want 200 %s", rec.Code, got, want)
	}
}

func TestScriptEndpointCapsTheWait(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"":         0,
		"0":        0,
		"2000":     2 * time.Second,
		"25000":    scriptMaxWait,
		"25001":    scriptMaxWait,
		"86400000": scriptMaxWait,
		// Larger than a Duration can hold in nanoseconds: clamped, never wrapped
		// into a negative or a short wait.
		"9223372036854775807": scriptMaxWait,
	} {
		got, err := parseScriptWait(raw)
		if err != nil {
			t.Errorf("parseScriptWait(%q): %v", raw, err)
		}
		if got != want {
			t.Errorf("parseScriptWait(%q) = %v, want %v", raw, got, want)
		}
	}
	if scriptMaxWait != 25*time.Second {
		t.Errorf("scriptMaxWait = %v, want 25s", scriptMaxWait)
	}
}

func TestScriptEndpointRejectsMalformedParameters(t *testing.T) {
	c := testConsole()
	c.Script.Ingest(scriptLine(testScriptPayload), time.Now())
	mux := testServer(t, c)

	for _, query := range []string{"?since=abc", "?since=-1", "?since=1.5", "?wait=soon", "?wait=-1", "?wait=2s", "?wait=99999999999999999999"} {
		if rec := getScript(context.Background(), mux, query); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /script%s = %d, want 400", query, rec.Code)
		}
	}
}

func TestScriptEndpointFlagsAGap(t *testing.T) {
	c := testConsole()
	for i := 1; i <= scriptLogCapacity+50; i++ {
		c.Script.Ingest(numberedScriptLine(i), time.Now())
	}
	mux := testServer(t, c)

	behind := decodeScript(t, getScript(context.Background(), mux, "?since=3"))
	if !behind.Gap {
		t.Error("gap = false for a cursor the ring has moved past")
	}
	if len(behind.Records) != scriptLogCapacity || behind.Records[0].ID != 51 {
		t.Errorf("got %d records from id %d, want everything retained from id 51", len(behind.Records), behind.Records[0].ID)
	}

	current := getScript(context.Background(), mux, "?since=300")
	if bytes.Contains(current.Body.Bytes(), []byte(`"gap"`)) {
		t.Errorf("body mentions a gap for a cursor inside the ring: %.80s", current.Body.String())
	}
	if resp := decodeScript(t, current); len(resp.Records) != 6 {
		t.Errorf("got %d records after id 300, want 6", len(resp.Records))
	}
}

func TestScriptEndpointUnblocksOnClientDisconnect(t *testing.T) {
	c := testConsole()
	mux := testServer(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	done := getScriptInBackground(ctx, mux, "?wait=25000")
	waitForScriptWaiters(t, c.Script, 1)

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept waiting after its client went away")
	}
	waitForScriptWaiters(t, c.Script, 0)
}

func TestScriptEndpointRefusesWaitersPastTheCap(t *testing.T) {
	c := testConsole()
	mux := testServer(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var parked []<-chan *httptest.ResponseRecorder
	for range scriptMaxWaiters {
		parked = append(parked, getScriptInBackground(ctx, mux, "?wait=25000"))
	}
	waitForScriptWaiters(t, c.Script, scriptMaxWaiters)

	rec := getScript(ctx, mux, "?wait=25000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("GET /script past the cap = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 carried no Retry-After")
	}
	if rec := getScript(ctx, mux, ""); rec.Code != http.StatusOK {
		t.Errorf("a non-blocking read past the cap = %d, want 200", rec.Code)
	}

	c.Script.Ingest(scriptLine(testScriptPayload), time.Now())
	for i, done := range parked {
		select {
		case rec := <-done:
			if resp := decodeScript(t, rec); len(resp.Records) != 1 {
				t.Errorf("waiter %d got %d records, want 1", i, len(resp.Records))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d was not released by the record", i)
		}
	}
}

// The server-wide write deadline is sized for a console command and starts
// when the request is read, so a wait longer than it would be answered on a
// connection the server has already given up on.
func TestScriptEndpointOutlivesTheServerWriteTimeout(t *testing.T) {
	srv := httptest.NewUnstartedServer(testServer(t, testConsole()))
	srv.Config.ReadTimeout = 50 * time.Millisecond
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/script?wait=400", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	start := time.Now()
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /script: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Errorf("answered after %v, want the full 400ms wait", elapsed)
	}
	if got, want := string(body), `{"records":[]}`+"\n"; resp.StatusCode != http.StatusOK || got != want {
		t.Errorf("GET /script = %d %q, want 200 %q", resp.StatusCode, got, want)
	}
}

// Shutdown waits for handlers without cancelling them. A parked request must
// be let go first, or every restart of the bridge costs the full grace
// period and ends with the reader's connection cut.
func TestScriptEndpointDoesNotHoldAShutdown(t *testing.T) {
	c := testConsole()
	srv := httptest.NewServer(testServer(t, c))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/script?wait=25000", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	status := make(chan int, 1)
	go func() {
		resp, err := srv.Client().Do(req)
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	waitForScriptWaiters(t, c.Script, 1)

	c.Script.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with a parked request: %v", err)
	}
	if got := <-status; got != http.StatusOK {
		t.Errorf("the parked request ended with %d, want a clean 200", got)
	}
}

// The whole path: a line the server prints on its console, split across two
// websocket frames the way a pipe read splits it, comes out of GET /script.
func TestScriptEndpointServesWhatTheConsolePrints(t *testing.T) {
	line := scriptLine(testScriptPayload) + "\n"
	printNow := make(chan struct{})
	addr, _ := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, _ *http.Request) {
		writeTestMsg(t, ctx, conn, wsMessage{Type: "logHistory", Lines: []string{scriptLine(`{"gen":"replayed"}`)}})
		select {
		case <-printNow:
		case <-ctx.Done():
			return
		}
		writeTestMsg(t, ctx, conn, wsMessage{Type: "stdout", Data: line[:40]})
		writeTestMsg(t, ctx, conn, wsMessage{Type: "stdout", Data: line[40:] + "\n"})
		<-ctx.Done()
	})

	c := NewConsole(addr, "pw", testOrigin, 2*time.Second, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitConnected(t, c)
	mux := testServer(t, c)

	answer := getScriptInBackground(context.Background(), mux, "?since=0&wait=5000")
	waitForScriptWaiters(t, c.Script, 1)
	close(printNow)

	select {
	case rec := <-answer:
		resp := decodeScript(t, rec)
		if len(resp.Records) != 1 || string(resp.Records[0].Data) != testScriptPayload {
			t.Errorf("records = %+v, want only the live record", resp.Records)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a record printed on the console never reached GET /script")
	}
}
