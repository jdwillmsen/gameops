package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

func TestDiagnostics_NeedASession(t *testing.T) {
	s := withLogin(t)
	if rec := do(s.Handler(), "GET", "/api/diagnostics", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/diagnostics without a session = %d, want 401", rec.Code)
	}
}

func TestDiagnostics_SayWhatTheServerIsAndNothingAboutAnyone(t *testing.T) {
	s := withLogin(t)
	s.Static = fstest.MapFS{
		"index.html": {Data: []byte("<html>map</html>")},
		"app.js":     {Data: []byte("console.log(1)")},
		"lib/x.js":   {Data: []byte("nested")},
		"notes.txt":  {Data: []byte("not a page file")},
	}
	rec := do(s.Handler(), "GET", "/api/diagnostics", "", []*http.Cookie{session(s, auth.Identity{Gamertag: "Secretname", XUID: "2535400000000000"})})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	body := rec.Body.String()
	for _, leak := range []string{"Secretname", "2535400000000000", "problem\":\""} {
		if strings.Contains(body, leak) {
			t.Errorf("the answer carries %q: %s", leak, body)
		}
	}
	var got diagnosticsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Role != "player" || !got.Features["login"] || got.Features["live"] || got.RefreshSeconds != 900 {
		t.Errorf("answer = %+v", got)
	}
	// Only the page's own files at the top, each by its fingerprint.
	if len(got.Assets) != 2 || got.Assets["app.js"] != fingerprint([]byte("console.log(1)")) || got.Assets["index.html"] == "" {
		t.Errorf("assets = %v", got.Assets)
	}
	if got.RenderedAt["overworld"].IsZero() {
		t.Errorf("renderedAt = %v, want the overworld's", got.RenderedAt)
	}
}

func TestDiagnostics_ListNoMoreFilesThanTheBound(t *testing.T) {
	s, _ := fixture(t)
	files := fstest.MapFS{}
	for i := range maxAssets + 20 {
		files[strings.Repeat("a", 1)+string(rune('a'+i%26))+string(rune('a'+i/26))+".js"] = &fstest.MapFile{Data: []byte("x")}
	}
	s.Static = files
	var got diagnosticsJSON
	if err := json.Unmarshal(get(t, s, "/api/diagnostics").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Assets) != maxAssets {
		t.Errorf("assets = %d, want %d", len(got.Assets), maxAssets)
	}
	if got.Role != "open" {
		t.Errorf("role = %q, want open with no login", got.Role)
	}
}

func TestFingerprintIsFNV1aAsTheScriptComputesIt(t *testing.T) {
	// The same figures the page's own test holds its script to.
	for text, want := range map[string]string{"": "811c9dc5", "a": "e40c292c", "foobar": "bf9cf968"} {
		if got := fingerprint([]byte(text)); got != want {
			t.Errorf("fingerprint(%q) = %s, want %s", text, got, want)
		}
	}
}
