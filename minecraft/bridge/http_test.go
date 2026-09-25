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
