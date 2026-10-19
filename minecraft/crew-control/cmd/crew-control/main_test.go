package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

func TestServerRefusesUncleanUnitPathsWithoutRedirect(t *testing.T) {
	var upstream atomic.Int32
	unit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstream.Add(1)
		fmt.Fprint(w, "unit body")
	}))
	defer unit.Close()
	units, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Agate","url":%q}]`, unit.URL))
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), logging.New("error"))

	for _, p := range []string{
		"/units/crew-01/./camera",
		"/units//camera",
		"/units/crew-01//camera",
		"/units/crew-01/v1/camera/../../v1/tasks",
		"/units/crew-01/v1/camera/../../camera",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d (location %q), want 404", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	if n := upstream.Load(); n != 0 {
		t.Fatalf("upstream saw %d requests, want 0", n)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/units/crew-01/camera", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "unit body" || upstream.Load() != 1 {
		t.Fatalf("clean path: status %d body %q upstream %d", rec.Code, rec.Body, upstream.Load())
	}
}

func TestServerRoutesConsoleAndRemovedList(t *testing.T) {
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), logging.New("error"))
	for path, want := range map[string]int{
		"/":         http.StatusOK,
		"/healthz":  http.StatusOK,
		"/camera":   http.StatusFound,
		"/v1/units": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
	}
}

// The expected value is assembled rather than written as one literal: a
// complete postgres:// URI with an embedded password is what secret scanners
// are built to find, and a fixture that trips them on every push trains people
// to wave the scanner through.
func TestPostgresDSNFromEnv(t *testing.T) {
	const (
		user = "crew"
		pass = "p/ss@w:rd"
		host = "db.internal"
	)
	t.Setenv("PG_HOST", "")
	if dsn, err := postgresDSN(); dsn != "" || err != nil {
		t.Fatalf("unset host: %q %v", dsn, err)
	}
	t.Setenv("PG_HOST", host)
	t.Setenv("PG_PORT", "6432")
	t.Setenv("PG_DATABASE", "gameops")
	t.Setenv("PG_USERNAME", user)
	t.Setenv("PG_PASSWORD", pass)
	dsn, err := postgresDSN()
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://" + user + ":" + url.QueryEscape(pass) + "@" + host + ":6432/gameops?sslmode=require"; dsn != want {
		t.Fatalf("dsn %q, want %q", dsn, want)
	}
	// Raw @ / : in the password would re-parse as URL structure.
	if userinfo := strings.TrimPrefix(dsn[:strings.LastIndex(dsn, "@")], "postgres://"+user+":"); strings.ContainsAny(userinfo, "@/:") {
		t.Fatalf("raw password characters survived into the DSN: %q", userinfo)
	}
	t.Setenv("PG_PORT", "nope")
	if _, err := postgresDSN(); err == nil {
		t.Fatal("bad port accepted")
	}
}

func TestUnitWorkRoutesAreMounted(t *testing.T) {
	units, _ := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"}]`)
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), logging.New("error"),
		func(mux *http.ServeMux) { work.Routes(mux, &work.Lazy{}, units, "tok", nil) })
	req := httptest.NewRequest(http.MethodGet, "/v1/units/crew-01/work?wait=1s", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 while the store is not connected", rec.Code)
	}
}

func TestUnsetPostgresLeavesWorkRoutesDownUnlessMemoryIsChosen(t *testing.T) {
	log := logging.New("error")
	enqueue := func(s *work.Lazy) error {
		_, err := s.Enqueue(context.Background(), "crew-01", work.KindCommand, []byte(`{"name":"stop"}`), "op")
		return err
	}

	unconfigured := &work.Lazy{}
	openStore(context.Background(), unconfigured, "", false, log) // returns at once
	if err := enqueue(unconfigured); !errors.Is(err, work.ErrUnavailable) {
		t.Fatalf("unconfigured store: %v, want ErrUnavailable", err)
	}

	mem := &work.Lazy{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { openStore(ctx, mem, "", true, log); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for enqueue(mem) != nil {
		if time.Now().After(deadline) {
			t.Fatal("memory store never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
