package mapclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const worldBody = `{"checked":true,"checkedAt":"2026-10-03T04:05:06.123456789Z",
"chunks":{"overworld":100,"nether":10,"end":1},
"missing":{"overworld":0,"nether":0,"end":0},"missingTotal":0,
"lost":{"overworld":4753,"nether":1413,"end":294},"lostTotal":6460,
"lostSample":[{"dimension":"overworld","x":2560,"z":-16},{"dimension":"nether","x":2576,"z":32}]}`

func TestWorld_ReadsTheLossTheMapReports(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		io.WriteString(w, worldBody)
	}))
	t.Cleanup(srv.Close)

	w, err := New(srv.URL, "internal-token", time.Second).World(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodGet || seen.URL.Path != "/internal/v1/world" {
		t.Errorf("request = %s %s", seen.Method, seen.URL.Path)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer internal-token" {
		t.Errorf("Authorization = %q", got)
	}
	if !w.Checked || w.LostTotal != 6460 || w.Lost["overworld"] != 4753 || w.Lost["end"] != 294 {
		t.Errorf("world = %+v", w)
	}
	if want := time.Date(2026, 10, 3, 4, 5, 6, 123456789, time.UTC); !w.CheckedAt.Equal(want) {
		t.Errorf("CheckedAt = %v, want %v", w.CheckedAt, want)
	}
	if len(w.LostSample) != 2 || w.LostSample[0] != (Chunk{Dimension: "overworld", X: 2560, Z: -16}) {
		t.Errorf("LostSample = %+v", w.LostSample)
	}
}

func TestWorld_BeforeTheFirstCountIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"checked":false}`)
	}))
	t.Cleanup(srv.Close)

	w, err := New(srv.URL, "t", time.Second).World(context.Background())
	if err != nil || w.Checked || w.LostTotal != 0 {
		t.Fatalf("world = %+v, err = %v", w, err)
	}
}

func TestWorld_RefusalsAreErrorsNotEmptyWorlds(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "detail", status)
		}))
		_, err := New(srv.URL, "t", time.Second).World(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("status %d: err = nil, want an error: an unreachable count must never read as a clean world", status)
		}
	}
}

func TestWorld_MalformedBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `<html>not json</html>`)
	}))
	t.Cleanup(srv.Close)
	if _, err := New(srv.URL, "t", time.Second).World(context.Background()); err == nil {
		t.Fatal("err = nil, want a decode error")
	}
}

func TestAcknowledgeWorld_NamesTheCountItAccepts(t *testing.T) {
	var seen *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	at := time.Date(2026, 10, 3, 4, 5, 6, 123456789, time.UTC)
	if err := New(srv.URL, "internal-token", time.Second).AcknowledgeWorld(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodPost || seen.URL.Path != "/internal/v1/world/acknowledge" {
		t.Errorf("request = %s %s", seen.Method, seen.URL.Path)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer internal-token" {
		t.Errorf("Authorization = %q", got)
	}
	if strings.TrimSpace(body) != `{"checkedAt":"2026-10-03T04:05:06.123456789Z"}` {
		t.Errorf("body = %s, want only checkedAt at full precision (the map compares it exactly)", body)
	}
}

func TestAcknowledgeWorld_AConflictIsItsOwnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "a newer chunk count has replaced the one acknowledged", http.StatusConflict)
	}))
	t.Cleanup(srv.Close)
	err := New(srv.URL, "t", time.Second).AcknowledgeWorld(context.Background(), time.Now())
	if !errors.Is(err, ErrCountReplaced) {
		t.Fatalf("err = %v, want ErrCountReplaced", err)
	}
}

func TestAcknowledgeWorld_OtherRefusalsAreNotConflicts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "detail", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	err := New(srv.URL, "t", time.Second).AcknowledgeWorld(context.Background(), time.Now())
	if err == nil || errors.Is(err, ErrCountReplaced) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}
