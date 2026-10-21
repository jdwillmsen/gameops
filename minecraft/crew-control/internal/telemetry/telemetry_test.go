package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	want := TaskSummary{ID: "run", Template: "gather_and_deposit", State: "running", Item: "minecraft:log", Qty: 8, Progress: 3}
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
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(nil))
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
		name := ""
		for sc.Scan() {
			if n, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				name = n
			}
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok && name == "units" {
				out <- d
			}
		}
		close(out)
	}()
	return out, resp
}

func TestStreamSendsOnChange(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	every := 100 * time.Millisecond
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

func TestIdleStreamRefreshesSeen(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	every := 20 * time.Millisecond
	p := startPoller(t, rosterFor(t, unit.server.URL), every)
	p.keepalive = 100 * time.Millisecond
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	ev, _ := events(t, streamServer(t, p))

	var first, second []Status
	for _, dst := range []*[]Status{&first, &second} {
		select {
		case d := <-ev:
			if err := json.Unmarshal([]byte(d), dst); err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no refreshed event on an idle stream")
		}
	}
	if !second[0].Seen.After(first[0].Seen) {
		t.Errorf("seen did not advance: %v then %v", first[0].Seen, second[0].Seen)
	}
}

func TestShutdownEndsActiveStreams(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(nil))

	ctx, end := context.WithCancel(context.Background())
	defer end()
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	srv.Config.RegisterOnShutdown(end)
	srv.Start()
	defer srv.Close()

	ev, _ := events(t, srv)
	<-ev
	start := time.Now()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(shut); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("shutdown took %v with a viewer connected", d)
	}
}

// workEvents yields "event: name" and the data line that follows, joined.
func workEvents(t *testing.T, url string, hdr http.Header) <-chan string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/v1/units/stream", nil)
	req.Header = hdr
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			if n, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				name = n
			}
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok && name != "events" && name != "log" && name != "positions" {
				out <- name + " " + d
			}
		}
		close(out)
	}()
	return out
}

func authedByHeader(r *http.Request) bool { return r.Header.Get("X-Test-Auth") == "yes" }

func TestWorkEventsReachOnlyAuthenticatedStreams(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	p.SetWorkSource(func(_ context.Context, id string) ([]byte, error) {
		return []byte(`{"unit":"` + id + `","items":[]}`), nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(authedByHeader))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	anon := workEvents(t, srv.URL, http.Header{})
	authed := workEvents(t, srv.URL, http.Header{"X-Test-Auth": {"yes"}})

	next := func(ch <-chan string) string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	if got := next(authed); !strings.HasPrefix(got, "units ") {
		t.Fatalf("first authed event %q", got)
	}
	if got := next(authed); got != `work {"unit":"crew-01","items":[]}` {
		t.Fatalf("history event %q", got)
	}
	if got := next(anon); !strings.HasPrefix(got, "units ") {
		t.Fatalf("first anon event %q", got)
	}

	p.PublishWork("crew-01")
	if got := next(authed); !strings.HasPrefix(got, "work ") {
		t.Fatalf("after publish %q", got)
	}
	select {
	case got := <-anon:
		t.Fatalf("anonymous stream got %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWorkEventsStopWhenTheSessionLapses(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	p.SetWorkSource(func(_ context.Context, id string) ([]byte, error) {
		return []byte(`{"unit":"` + id + `","items":[]}`), nil
	})
	var valid atomic.Bool
	valid.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(func(*http.Request) bool { return valid.Load() }))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ch := workEvents(t, srv.URL, http.Header{})
	next := func() string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	next() // units
	next() // history
	p.PublishWork("crew-01")
	if got := next(); !strings.HasPrefix(got, "work ") {
		t.Fatalf("while signed in: %q", got)
	}

	valid.Store(false)
	p.PublishWork("crew-01")
	select {
	case got := <-ch:
		t.Fatalf("work event after the session lapsed: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
	// Signing back in on the same stream must not resume it either.
	valid.Store(true)
	p.PublishWork("crew-01")
	select {
	case got := <-ch:
		t.Fatalf("stream resumed work events: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestHistoryArrivesWhenTheStoreBecomesReady(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	var ready atomic.Bool
	p.SetWorkSource(func(_ context.Context, id string) ([]byte, error) {
		if !ready.Load() {
			return nil, errors.New("store not connected")
		}
		return []byte(`{"unit":"` + id + `","items":[]}`), nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(func(*http.Request) bool { return true }))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ch := workEvents(t, srv.URL, http.Header{})
	next := func() string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	if got := next(); !strings.HasPrefix(got, "units ") {
		t.Fatalf("first event %q", got)
	}
	select {
	case got := <-ch:
		t.Fatalf("history sent before the store was ready: %q", got)
	case <-time.After(150 * time.Millisecond):
	}

	ready.Store(true)
	p.PublishAllWork()
	if got := next(); got != `work {"unit":"crew-01","items":[]}` {
		t.Fatalf("after ready %q", got)
	}
}

func rawStream(t *testing.T, url string, hdr http.Header) <-chan [2]string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/v1/units/stream", nil)
	req.Header = hdr
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan [2]string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			if n, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				name = n
			}
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				out <- [2]string{name, d}
			}
		}
		close(out)
	}()
	return out
}

func nextNamed(t *testing.T, ch <-chan [2]string, name string) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev[0] == name {
				return ev[1]
			}
		case <-deadline:
			t.Fatalf("no %q event", name)
		}
	}
}

func TestPositionsReachOnlyAuthenticatedStreams(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(authedByHeader))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	signed := rawStream(t, srv.URL, http.Header{"X-Test-Auth": {"yes"}})
	anon := rawStream(t, srv.URL, nil)
	got := nextNamed(t, signed, "positions")
	if !strings.Contains(got, `"x":12.5`) || !strings.Contains(got, `"known":true`) {
		t.Errorf("positions = %s", got)
	}
	nextNamed(t, anon, "events")
	deadline := time.After(2*positionEvery + 200*time.Millisecond)
	for {
		select {
		case ev := <-anon:
			if ev[0] == "positions" || strings.Contains(ev[1], "12.5") {
				t.Fatalf("anonymous stream got coordinates: %v", ev)
			}
		case <-deadline:
			return
		}
	}
}

func TestStreamBacklogAndLiveLog(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	srv := streamServer(t, p)
	ch := rawStream(t, srv.URL, nil)
	if got := nextNamed(t, ch, "events"); got != "[]" {
		t.Fatalf("backlog = %s, want empty array", got)
	}
	unit.set(200, stateUnknownNoTask)
	got := nextNamed(t, ch, "log")
	if !strings.Contains(got, `"kind":"died"`) && !strings.Contains(got, `"kind":"task_ended"`) {
		t.Errorf("log = %s", got)
	}
}

func jobsPoller(t *testing.T, authed func(*http.Request) bool) (*Poller, *httptest.Server) {
	t.Helper()
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	p.SetWorkSource(func(_ context.Context, id string) ([]byte, error) {
		return []byte(`{"unit":"` + id + `","items":[]}`), nil
	})
	var n atomic.Int32
	p.SetJobsSource(func(context.Context) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"jobs":[],"n":%d}`, n.Add(1))), nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(authed))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return p, srv
}

func TestJobsEventsReachOnlyAuthenticatedStreams(t *testing.T) {
	p, srv := jobsPoller(t, authedByHeader)
	anon := workEvents(t, srv.URL, http.Header{})
	authed := workEvents(t, srv.URL, http.Header{"X-Test-Auth": {"yes"}})
	next := func(ch <-chan string) string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	if got := next(authed); !strings.HasPrefix(got, "units ") {
		t.Fatalf("first %q", got)
	}
	if got := next(authed); !strings.HasPrefix(got, "work ") {
		t.Fatalf("history %q", got)
	}
	if got := next(authed); !strings.HasPrefix(got, "jobs ") {
		t.Fatalf("jobs on open %q", got)
	}
	next(anon)
	p.PublishJobs()
	if got := next(authed); !strings.HasPrefix(got, "jobs ") {
		t.Fatalf("after publish %q", got)
	}
	select {
	case got := <-anon:
		t.Fatalf("anonymous stream got %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestJobsEventsStopWhenTheSessionLapses(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	p, srv := jobsPoller(t, func(*http.Request) bool { return valid.Load() })
	ch := workEvents(t, srv.URL, http.Header{})
	next := func() string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	next() // units
	next() // history
	next() // jobs
	valid.Store(false)
	p.PublishJobs()
	select {
	case got := <-ch:
		t.Fatalf("jobs event after the session lapsed: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
	valid.Store(true)
	p.PublishJobs()
	select {
	case got := <-ch:
		t.Fatalf("stream resumed jobs events: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestJobsPublishesCoalesce(t *testing.T) {
	p, srv := jobsPoller(t, func(*http.Request) bool { return true })
	ch := workEvents(t, srv.URL, http.Header{})
	for i := 0; i < 3; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
		}
	}
	for i := 0; i < 50; i++ {
		p.PublishJobs()
	}
	time.Sleep(300 * time.Millisecond)
	got := 0
	for done := false; !done; {
		select {
		case s := <-ch:
			if strings.HasPrefix(s, "jobs ") {
				got++
			}
		default:
			done = true
		}
	}
	if got < 1 || got > 3 {
		t.Fatalf("50 publishes produced %d events", got)
	}
}

// TestFeedsRenderOncePerChange proves the work and jobs payloads are rendered
// once and shared: five signed-in streams make the store's Recent and List
// (the sources) count one call on connect and one more for the next change,
// not one call per stream.
func TestFeedsRenderOncePerChange(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	var workCalls, jobsCalls atomic.Int32
	p.SetWorkSource(func(_ context.Context, id string) ([]byte, error) {
		workCalls.Add(1)
		return []byte(`{"unit":"` + id + `","items":[]}`), nil
	})
	p.SetJobsSource(func(context.Context) ([]byte, error) {
		jobsCalls.Add(1)
		return []byte(`{"jobs":[]}`), nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(authedByHeader))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const streams = 5
	authed := make([]<-chan string, streams)
	for i := range authed {
		authed[i] = workEvents(t, srv.URL, http.Header{"X-Test-Auth": {"yes"}})
	}
	next := func(ch <-chan string) string {
		select {
		case s := <-ch:
			return s
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	// Each stream draws its snapshot, the one-unit work history and the jobs
	// list on open; the shared cache means 5 viewers read the store once.
	for _, ch := range authed {
		if got := next(ch); !strings.HasPrefix(got, "units ") {
			t.Fatalf("first event %q", got)
		}
		if got := next(ch); !strings.HasPrefix(got, "work ") {
			t.Fatalf("history event %q", got)
		}
		if got := next(ch); !strings.HasPrefix(got, "jobs ") {
			t.Fatalf("jobs on open %q", got)
		}
	}
	if got := workCalls.Load(); got != 1 {
		t.Errorf("connect rendered work %d times, want 1", got)
	}
	if got := jobsCalls.Load(); got != 1 {
		t.Errorf("connect rendered jobs %d times, want 1", got)
	}

	p.PublishWork("crew-01")
	p.PublishJobs()
	// A stream with both changes pending may write either first.
	for _, ch := range authed {
		got := map[string]bool{}
		for range 2 {
			name, _, _ := strings.Cut(next(ch), " ")
			got[name] = true
		}
		if !got["work"] || !got["jobs"] {
			t.Fatalf("after publish, events %v, want work and jobs", got)
		}
	}
	if got := workCalls.Load(); got != 2 {
		t.Errorf("one change rendered work %d times, want 2 (connect plus change)", got)
	}
	if got := jobsCalls.Load(); got != 2 {
		t.Errorf("one change rendered jobs %d times, want 2 (connect plus change)", got)
	}
}

// TestPublishDoesNotWaitOnARender proves a stalled store cannot hold up the
// poller: while a stream is stuck rendering both feeds, publishing changes and
// reading the snapshot still return at once.
func TestPublishDoesNotWaitOnARender(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, time.Second)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	block := func(ctx context.Context) ([]byte, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, errors.New("stalled")
	}
	p.SetWorkSource(func(ctx context.Context, _ string) ([]byte, error) { return block(ctx) })
	p.SetJobsSource(block)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/units/stream", p.StreamHandler(authedByHeader))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	workEvents(t, srv.URL, http.Header{"X-Test-Auth": {"yes"}})
	t.Cleanup(func() { close(release) })
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream never started rendering")
	}
	done := make(chan struct{})
	go func() {
		p.PublishWork("crew-01")
		p.PublishJobs()
		p.Snapshot()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publishing waited on a stalled render")
	}
}
