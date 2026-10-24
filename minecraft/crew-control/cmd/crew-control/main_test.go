package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/jobs"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/metrics"
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
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), nil, auth.Disabled().Operator, logging.New("error"))

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

func TestMetricsAnswerOnTheListenerAndNeverThroughTheProxy(t *testing.T) {
	var upstream atomic.Int32
	unit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		_, _ = fmt.Fprint(w, "unit body for "+r.URL.Path)
	}))
	defer unit.Close()
	units, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Agate","url":%q}]`, unit.URL))
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), nil, auth.Disabled().Operator, logging.New("error"),
		mountMetrics(metrics.New([]string{"crew-01"})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "crew_fleet_units") {
		t.Fatalf("/metrics: status %d body %.200q", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics: status %d, want 405", rec.Code)
	}

	// Under /units/ the proxy answers, and it forwards only its allow-list: a
	// metrics path must neither reach the unit nor return the fleet's series.
	for _, p := range []string{
		"/units/crew-01/metrics",
		"/units/crew-01/v1/metrics",
		"/units/metrics",
		"/units/crew-01/../metrics",
		"/units/crew-01/metrics/",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "crew_fleet") {
			t.Errorf("%s: status %d body %.100q, want a bare 404", p, rec.Code, rec.Body)
		}
	}
	if n := upstream.Load(); n != 0 {
		t.Fatalf("a metrics path reached the unit %d times", n)
	}
}

func TestServerRoutesConsoleAndRemovedList(t *testing.T) {
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), nil, auth.Disabled().Operator, logging.New("error"))
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
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), nil, auth.Disabled().Operator, logging.New("error"),
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
	openStore(context.Background(), unconfigured, &atomic.Pointer[ledger.Ledger]{}, &atomic.Pointer[jobs.Scheduler]{}, &events.Lazy{}, telemetry.NewPoller(roster.Roster{}, &http.Client{}, time.Second), nil, "", false, log) // returns at once
	if err := enqueue(unconfigured); !errors.Is(err, work.ErrUnavailable) {
		t.Fatalf("unconfigured store: %v, want ErrUnavailable", err)
	}

	mem := &work.Lazy{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var world atomic.Pointer[ledger.Ledger]
	seeds := []ledger.Place{{ID: "home-chest", Name: "Home", Kind: ledger.KindChest,
		Box: ledger.Box{Min: ledger.Pos{X: 1, Y: 64, Z: 2}, Max: ledger.Pos{X: 1, Y: 64, Z: 2}}}}
	go func() {
		openStore(ctx, mem, &world, &atomic.Pointer[jobs.Scheduler]{}, &events.Lazy{}, telemetry.NewPoller(roster.Roster{}, &http.Client{}, time.Second), seeds, "", true, log)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for enqueue(mem) != nil {
		if time.Now().After(deadline) {
			t.Fatal("memory store never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	l := world.Load()
	if l == nil {
		t.Fatal("ledger not published with the store")
	}
	if _, ok, err := (*l).Place(context.Background(), "home-chest"); err != nil || !ok {
		t.Fatalf("seed place missing: ok=%v err=%v", ok, err)
	}
	cancel()
	<-done
}

func TestSeenReachesTheLedgerAndTheLogHoldsNoCoordinates(t *testing.T) {
	// The logger binds os.Stdout when built, so swap it first.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	real := os.Stdout
	os.Stdout = w
	log := logging.New("info")
	os.Stdout = real
	var world atomic.Pointer[ledger.Ledger]
	hook := observeSeen(&world, log)
	var s work.Seen
	s.Chest.X, s.Chest.Y, s.Chest.Z = -12, 100, 5
	s.Items = map[string]int{"coal": 7}
	s.At = time.Now()

	hook("crew-01", s) // no ledger yet: dropped, not a panic
	var l ledger.Ledger = ledger.NewMemory()
	world.Store(&l)
	hook("crew-01", s)

	st, err := l.Stock(context.Background(), "chest-m12-100-5")
	if err != nil || len(st) != 1 || st[0].Count != 7 {
		t.Fatalf("stock %v err=%v", st, err)
	}
	_ = w.Close()
	raw, _ := io.ReadAll(r)
	out := string(raw)
	if !strings.Contains(out, "stock_observed") || !strings.Contains(out, `"place":"unnamed"`) {
		t.Errorf("log %q", out)
	}
	// Judge parsed fields, not substrings: a timestamp may legitimately hold "100".
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		lines++
		for k, v := range rec {
			switch v := v.(type) {
			case float64:
				if v == -12 || v == 100 || v == 5 {
					t.Errorf("field %q holds a coordinate: %v", k, v)
				}
			case string:
				for _, leak := range []string{"coal", "-12,100,5", "-12 100 5", "m12-100-5", "chest-m12"} {
					if strings.Contains(v, leak) {
						t.Errorf("field %q leaks %q: %q", k, leak, v)
					}
				}
			}
		}
	}
	if lines == 0 {
		t.Error("no log lines captured")
	}
}

func TestAuthRoutesOnlyWhenConfigured(t *testing.T) {
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	poller := telemetry.NewPoller(units, &http.Client{}, time.Second)
	status := func(a *auth.Auth) int {
		h := newHandler(units, poller, nil, a.Operator, logging.New("error"), a.Mount)
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
	h := newHandler(units, poller, nil, a.Operator, logging.New("error"), func(mux *http.ServeMux) {
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
	dataDir := t.TempDir()
	sessionKey := []byte("0123456789abcdef0123456789abcdef")
	credentialID := []byte("independent-test-credential")
	passkeys, err := json.Marshal([]auth.Passkey{{
		ID: credentialID, PublicKey: []byte("test-public-key"), UserHandle: []byte("test-user-handle"), Operator: "owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "session.key"), sessionKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "passkeys.json"), passkeys, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{
		Origin: "https://crew.example.test", DataDir: dataDir, InvitesDir: t.TempDir(),
		Operators: `[{"id":"owner","name":"Owner"}]`, Log: logging.New("error"),
	})
	if err != nil {
		t.Fatal(err)
	}
	keyHash := sha256.Sum256(credentialID)
	now := time.Now()
	payload, err := json.Marshal(struct {
		Op  string `json:"op"`
		Key string `json:"key"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}{"owner", base64.RawURLEncoding.EncodeToString(keyHash[:16]), now.Unix(), now.Add(12 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, sessionKey)
	_, _ = mac.Write([]byte(body))
	cookie := &http.Cookie{Name: auth.SessionCookie, Value: body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}
	poller := telemetry.NewPoller(units, &http.Client{}, time.Second)
	h := newHandler(units, poller, nil, a.Operator, logging.New("error"), func(mux *http.ServeMux) {
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

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/work/crew-01/stop", strings.NewReader("{}"))
	req.Header.Set("X-Crew-Request", "1")
	req.Header.Set("Content-Type", "application/json")
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
	quiet := time.After(300 * time.Millisecond)
	for {
		select {
		case n := <-anon:
			// The fleet log is public; task bodies and coordinates are not.
			if n == "events" || n == "log" {
				continue
			}
			t.Fatalf("anonymous stream got %q", n)
		case <-quiet:
			return
		}
	}
}

func TestBornEventMapsToHistoryRow(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	b := telemetry.Born{
		Event: telemetry.Event{Unit: "crew-01", At: at, Kind: "task_failed", Level: "crit",
			Text: "failed haul: no path", Task: "11111111-1111-1111-1111-111111111111",
			Template: "haul", Took: 1500 * time.Millisecond},
		Pos: telemetry.Position{X: 1.5, Y: 64, Z: -2, Known: true},
	}
	e, h := bornEvent(b)
	if e.Unit != "crew-01" || !e.At.Equal(at) || e.Kind != "task_failed" || e.Severity != "crit" {
		t.Fatalf("mapped wrong: %+v", e)
	}
	if e.TaskID == nil || e.TaskID.String() != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("task: %v", e.TaskID)
	}
	if e.Pos == nil || *e.Pos != (events.Pos{X: 1.5, Y: 64, Z: -2}) {
		t.Fatalf("pos: %+v", e.Pos)
	}
	if h.Template != "haul" || h.Took != 1500*time.Millisecond || h.Task != b.Task {
		t.Fatalf("hint: %+v", h)
	}
	// The payload holds the text the feed showed, so history reads like the
	// live log did, and the duration in a unit a query can compare.
	var p map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &p); err != nil || string(p["text"]) != `"failed haul: no path"` ||
		string(p["template"]) != `"haul"` || string(p["duration_ms"]) != `1500` {
		t.Fatalf("payload: %s (%v)", e.Payload, err)
	}

	// An id the queue would not recognise stays in the payload rather than
	// being lost, and an unknown position stores as none.
	b.Task, b.Pos.Known = "run-local-7", false
	e, h = bornEvent(b)
	if e.TaskID != nil || e.Pos != nil {
		t.Fatalf("unrecognised id or pos stored: %+v", e)
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil || string(p["task"]) != `"run-local-7"` {
		t.Fatalf("payload: %s (%v)", e.Payload, err)
	}
	if h.Task != "run-local-7" {
		t.Fatalf("hint keeps the raw id so a start pairs with its finish: %+v", h)
	}

	// A text with quotes and control characters stays valid JSON.
	b.Text = "failed \"x\"\n\t"
	if e, _ = bornEvent(b); !json.Valid(e.Payload) {
		t.Fatalf("payload is not JSON: %s", e.Payload)
	}
}

func TestUnitDetailRouteIsServedBeforeTheProxy(t *testing.T) {
	// If the detail path ever reached the proxy it would be 404d there;
	// the upstream counts to prove it never got that far.
	var upstream atomic.Int32
	unit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		if r.URL.Path == "/camera" {
			_, _ = fmt.Fprint(w, "unit body")
		}
	}))
	defer unit.Close()
	units, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Agate","url":%q,"player":"-12"}]`, unit.URL))
	if err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	sessionKey := []byte("0123456789abcdef0123456789abcdef")
	credentialID := []byte("independent-test-credential")
	passkeys, err := json.Marshal([]auth.Passkey{{
		ID: credentialID, PublicKey: []byte("test-public-key"), UserHandle: []byte("test-user-handle"), Operator: "owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "session.key"), sessionKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "passkeys.json"), passkeys, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{
		Origin: "https://crew.example.test", DataDir: dataDir, InvitesDir: t.TempDir(),
		Operators: `[{"id":"owner","name":"Owner"}]`, Log: logging.New("error"),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), nil, a.Operator, logging.New("error"))

	// Signed out the route refuses, and it is crew-control's own 401
	// rather than the proxy's 404: the ender chest is private.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/units/crew-01/detail", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d, want 401", rec.Code)
	}
	if n := upstream.Load(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want 0", n)
	}

	cookie := sessionCookie(t, sessionKey, credentialID)
	req := httptest.NewRequest(http.MethodGet, "/units/crew-01/detail", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed in: %d body %q", rec.Code, rec.Body)
	}
	var v struct {
		Save struct {
			State string `json:"state"`
		} `json:"save"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body)
	}
	if v.Save.State != "off" {
		t.Errorf("save state = %q, want off with no source configured", v.Save.State)
	}
	if n := upstream.Load(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want 0", n)
	}

	// A method the route does not have stays the family's 404, and the
	// proxy's allow-list is unchanged.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/units/crew-01/detail", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST detail: %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/units/crew-01/camera", nil))
	if rec.Code != http.StatusOK || upstream.Load() != 1 {
		t.Errorf("camera: %d, want the proxy still serving it", rec.Code)
	}
}

func sessionCookie(t *testing.T, sessionKey, credentialID []byte) *http.Cookie {
	t.Helper()
	keyHash := sha256.Sum256(credentialID)
	now := time.Now()
	payload, err := json.Marshal(struct {
		Op  string `json:"op"`
		Key string `json:"key"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}{"owner", base64.RawURLEncoding.EncodeToString(keyHash[:16]), now.Unix(), now.Add(12 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, sessionKey)
	_, _ = mac.Write([]byte(body))
	return &http.Cookie{Name: auth.SessionCookie, Value: body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}
}
