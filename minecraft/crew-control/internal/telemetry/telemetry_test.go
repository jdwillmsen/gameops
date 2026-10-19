package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

// Shaped like crew's /v1/state response, position included, so the tests prove
// it is dropped.
const stateKnownWithTask = `{
 "version":"dev","unit":{"id":"crew-01","name":"Ada"},
 "state":{"spawned":true,"dead":false,"dimension":0,"position":[12.5,64.62,-3],"position_known":true,
  "yaw":0,"pitch":0,"health":18.5,"health_known":true,"updated_at":"2026-10-19T10:00:00Z",
  "held_slot":0,"held_known":true,"inventory":[]},
 "tasks":{"current":{"id":"run","template":"gather_and_deposit","state":"running","skill":"mine",
  "item":"minecraft:log","qty":8,"progress":3,"created":"2026-10-19T10:00:00Z","updated":"2026-10-19T10:00:01Z"},"recent":[]}}`

const stateUnknownNoTask = `{
 "version":"dev","unit":{"id":"crew-01","name":"Ada"},
 "state":{"spawned":true,"dead":true,"dimension":0,"position":[0,0,0],"position_known":false,
  "yaw":0,"pitch":0,"health":0,"health_known":false,"updated_at":"2026-10-19T10:00:00Z",
  "held_slot":0,"held_known":false,"inventory":[]},
 "tasks":{"current":null,"recent":[]}}`

type fakeUnit struct {
	mu     sync.Mutex
	code   int
	body   string
	server *httptest.Server
}

func newFakeUnit(t *testing.T, body string) *fakeUnit {
	t.Helper()
	f := &fakeUnit{code: 200, body: body}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/state" {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		code, body := f.code, f.body
		f.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeUnit) set(code int, body string) {
	f.mu.Lock()
	f.code, f.body = code, body
	f.mu.Unlock()
}

func rosterFor(t *testing.T, urls ...string) roster.Roster {
	t.Helper()
	var parts []string
	for i, u := range urls {
		parts = append(parts, fmt.Sprintf(`{"id":"crew-%02d","name":"Unit%d","url":%q}`, i+1, i+1, u))
	}
	r, err := roster.Parse("[" + strings.Join(parts, ",") + "]")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func startPoller(t *testing.T, r roster.Roster, every time.Duration) *Poller {
	t.Helper()
	p := NewPoller(r, &http.Client{}, every)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return p
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPollerMapsState(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)

	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	s := p.Snapshot()[0]
	if s.ID != "crew-01" || s.Name != "Unit1" {
		t.Errorf("identity = %q %q, want roster values", s.ID, s.Name)
	}
	if s.Health == nil || *s.Health != 18.5 || s.Dead {
		t.Errorf("health/dead = %v %v", s.Health, s.Dead)
	}
	want := TaskSummary{Template: "gather_and_deposit", State: "running", Item: "minecraft:log", Qty: 8, Progress: 3}
	if s.Task == nil || *s.Task != want {
		t.Errorf("task = %+v, want %+v", s.Task, want)
	}
	if s.Seen.IsZero() {
		t.Error("seen not set")
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "position") || strings.Contains(string(raw), "64.62") {
		t.Errorf("status leaks position: %s", raw)
	}

	unit.set(200, stateUnknownNoTask)
	waitFor(t, "unknown health", func() bool { return p.Snapshot()[0].Dead })
	s = p.Snapshot()[0]
	if s.Health != nil || s.Task != nil || !s.Online {
		t.Errorf("health=%v task=%v online=%v, want nil nil true", s.Health, s.Task, s.Online)
	}
}

func TestPollerMarksOffline(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })

	unit.set(500, "boom")
	waitFor(t, "offline", func() bool { return !p.Snapshot()[0].Online })
	if s := p.Snapshot()[0]; s.Health != nil || s.Task != nil {
		t.Errorf("offline unit still shows %+v", s)
	}

	unit.set(200, stateKnownWithTask)
	waitFor(t, "back online", func() bool { return p.Snapshot()[0].Online })
}

func TestPollerSingleFailureStaysOnline(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	ctx := context.Background()
	p.tick(ctx)
	unit.set(500, "")
	p.tick(ctx)
	if !p.Snapshot()[0].Online {
		t.Fatal("one failure must not flip the unit offline")
	}
	p.tick(ctx)
	if p.Snapshot()[0].Online {
		t.Fatal("two failures must flip the unit offline")
	}
}

func streamServer(t *testing.T, p *Poller) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units", p.ListHandler)
	mux.HandleFunc("/v1/units/stream", p.StreamHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// events yields the data payload of each "units" event.
func events(t *testing.T, srv *httptest.Server) (<-chan string, *http.Response) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/units/stream")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				out <- d
			}
		}
		close(out)
	}()
	return out, resp
}

func TestStreamSendsOnChange(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	every := 30 * time.Millisecond
	p := startPoller(t, rosterFor(t, unit.server.URL), every)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	srv := streamServer(t, p)

	ev, resp := events(t, srv)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("missing X-Accel-Buffering: no")
	}
	select {
	case d := <-ev:
		var got []Status
		if err := json.Unmarshal([]byte(d), &got); err != nil || len(got) != 1 || !got[0].Online {
			t.Fatalf("first event = %s (%v)", d, err)
		}
	case <-time.After(time.Second):
		t.Fatal("no immediate first event")
	}

	select {
	case d := <-ev:
		t.Fatalf("event without a change: %s", d)
	case <-time.After(2*every + 40*time.Millisecond):
	}

	unit.set(200, stateUnknownNoTask)
	select {
	case d := <-ev:
		var got []Status
		if err := json.Unmarshal([]byte(d), &got); err != nil || !got[0].Dead {
			t.Fatalf("second event = %s (%v)", d, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after the unit changed")
	}
}

func TestStreamLimit(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	srv := streamServer(t, p)

	for i := 0; i < maxStreams; i++ {
		_, resp := events(t, srv)
		if resp.StatusCode != 200 {
			t.Fatalf("client %d: status %d", i+1, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/v1/units/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("65th client status = %d, want 503", resp.StatusCode)
	}
}

func TestListReturnsSnapshot(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	rec := httptest.NewRecorder()
	p.ListHandler(rec, httptest.NewRequest("GET", "/v1/units", nil))
	var got []Status
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Online {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
}
