package main

import (
	"bufio"
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
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
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
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), auth.Disabled().Operator, logging.New("error"))

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
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), auth.Disabled().Operator, logging.New("error"))
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
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), auth.Disabled().Operator, logging.New("error"),
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

func TestAuthRoutesOnlyWhenConfigured(t *testing.T) {
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	poller := telemetry.NewPoller(units, &http.Client{}, time.Second)
	status := func(a *auth.Auth) int {
		h := newHandler(units, poller, a.Operator, logging.New("error"), a.Mount)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/me", nil))
		return rec.Code
	}
	if got := status(auth.Disabled()); got != http.StatusNotFound {
		t.Fatalf("disabled: /auth/me %d, want 404", got)
	}
	on, err := auth.New(auth.Config{Origin: "https://crew.example", DataDir: t.TempDir(), Operators: `[{"id":"owner","name":"Owner"}]`})
	if err != nil {
		t.Fatal(err)
	}
	if got := status(on); got != http.StatusUnauthorized {
		t.Fatalf("enabled: /auth/me %d, want 401", got)
	}
}

func TestStoreDownKeepsConsoleAndStreamUp(t *testing.T) {
	units, _ := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	poller := telemetry.NewPoller(units, &http.Client{}, time.Second)
	a, err := auth.New(auth.Config{Origin: "https://crew.example", DataDir: t.TempDir(), Operators: `[{"id":"owner","name":"Owner"}]`})
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, poller, a.Operator, logging.New("error"), func(mux *http.ServeMux) {
		api := work.Routes(mux, &work.Lazy{}, units, "tok", poller.PublishWork)
		api.MountOperator(mux, a)
		poller.SetWorkSource(api.Feed)
	})
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/", 200},
		{"POST", "/v1/work/crew-01/stop", 403},
		{"GET", "/v1/work/crew-01", 401},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/units/stream")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stream: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestWorkStreamNeedsARealSession(t *testing.T) {
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{
		Origin: "https://crew.example.test", DataDir: t.TempDir(), InvitesDir: t.TempDir(),
		Operators: `[{"id":"owner","name":"Owner"}]`, Log: logging.New("error"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := a.SessionForTest("owner")
	if err != nil {
		t.Fatal(err)
	}
	poller := telemetry.NewPoller(units, &http.Client{}, time.Second)
	h := newHandler(units, poller, a.Operator, logging.New("error"), func(mux *http.ServeMux) {
		api := work.Routes(mux, work.NewMemory(), units, "unit-token", poller.PublishWork)
		api.MountOperator(mux, a)
		poller.SetWorkSource(api.Feed)
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	open := func(c *http.Cookie) <-chan string {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/units/stream", nil)
		if c != nil {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		out := make(chan string, 16)
		go func() {
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				if n, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
					out <- n
				}
			}
			close(out)
		}()
		return out
	}
	anon, signed := open(nil), open(cookie)
	expect := func(ch <-chan string, want string) bool {
		deadline := time.After(2 * time.Second)
		for {
			select {
			case n := <-ch:
				if n == want {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	if !expect(anon, "units") || !expect(signed, "units") {
		t.Fatal("streams did not open")
	}
	if !expect(signed, "work") {
		t.Fatal("signed-in stream missed its history")
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/work/crew-01/stop", nil)
	req.Header.Set("X-Crew-Request", "1")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("enqueue: %d", resp.StatusCode)
	}

	if !expect(signed, "work") {
		t.Fatal("signed-in stream missed the enqueue")
	}
	select {
	case n := <-anon:
		t.Fatalf("anonymous stream got %q", n)
	case <-time.After(300 * time.Millisecond):
	}
}
