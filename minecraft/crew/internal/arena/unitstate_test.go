package arena

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPTacticsReadsTheUnitsProfile(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"tactics":{"profile":"cand-1","hash":"abc","applied_at":"2026-10-30T01:02:03Z","training_enabled":true}}`))
	}))
	defer srv.Close()
	src, err := NewHTTPTactics(srv.URL + "/units/{name}/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Tactics(context.Background(), "Rusty Q")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/units/Rusty%20Q/v1/state" {
		t.Errorf("path = %q", path)
	}
	if got.Profile != "cand-1" || got.Hash != "abc" || got.AppliedAt == nil || got.TrainingEnabled == nil || !*got.TrainingEnabled {
		t.Errorf("tactics = %+v", got)
	}
}

func TestHTTPTacticsFailsForAnOldBuildAnOfflineUnitAndAnUnknownName(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"old build": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"tasks":{}}`)) },
		"not found": http.NotFound,
		"bad json":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) },
	} {
		srv := httptest.NewServer(handler)
		src, _ := NewHTTPTactics(srv.URL + "/units/{name}/v1/state")
		_, err := src.Tactics(context.Background(), "Rusty")
		srv.Close()
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if name == "old build" && !errors.Is(err, ErrNoTactics) {
			t.Errorf("old build: err = %v, want ErrNoTactics", err)
		}
	}
	src, _ := NewHTTPTactics("http://127.0.0.1:1/units/{name}/v1/state")
	if _, err := src.Tactics(context.Background(), "Rusty"); err == nil {
		t.Error("an unreachable unit read without error")
	}
}

func TestNewHTTPTacticsRejectsBadTemplates(t *testing.T) {
	for _, bad := range []string{"", "http://x/state", "ftp://x/{name}", "/units/{name}"} {
		if _, err := NewHTTPTactics(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
