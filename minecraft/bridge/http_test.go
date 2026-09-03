package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
