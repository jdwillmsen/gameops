package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
)

const (
	unit      = "crew-01"
	token     = "s3cret"
	gotoBody  = `{"template":"goto","to":{"x":1,"y":64,"z":1}}`
	taskA     = "aaaaaaaa-0001"
	taskB     = "aaaaaaaa-0002"
	commandID = "cccccccc-0001"
)

func taskItem(id, body string) string {
	return `{"id":"` + id + `","kind":"task","template":` + body + `}`
}

func stopItem(id string) string {
	return `{"id":"` + id + `","kind":"command","command":{"name":"stop"}}`
}

// fakeControl is crew-control as the unit sees it.
type fakeControl struct {
	srv  *httptest.Server
	work chan string

	mu           sync.Mutex
	events       []event
	polls        int
	commandPolls int
	workCode     int // when non-zero, the work endpoint answers this instead
	eventFail    int // how many event posts to refuse before accepting
	badAuth      bool
	rejectID     string // events about this id get a 400
	refuseSeen   bool   // events carrying seen get a 413
	legacy       bool   // decodes like the deployed controller: unknown fields get a 400
	bodies       []int  // sizes of the event bodies received
	conns        int    // connections the server accepted
	releaseFirst chan struct{}
	releaseID    string
	releaseIDCh  chan struct{}
}

func newFakeControl(t *testing.T) *fakeControl {
	t.Helper()
	f := &fakeControl{work: make(chan string, 32)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/units/"+unit+"/work", func(w http.ResponseWriter, r *http.Request) {
		kinds := r.URL.Query().Get("kinds")
		f.mu.Lock()
		f.polls++
		if kinds == "command" {
			f.commandPolls++
		}
		code := f.workCode
		wait := pollWait
		if kinds == "command" {
			wait = commandPollWait
		}
		if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Query().Get("wait") != wait || (kinds != "" && kinds != "command") {
			f.badAuth = true
		}
		releaseFirst := f.releaseFirst
		releaseID, releaseIDCh := f.releaseID, f.releaseIDCh
		f.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			return
		}
		var item string
		found := false
		if kinds == "command" {
			for i, n := 0, len(f.work); i < n; i++ {
				candidate := <-f.work
				var parsed struct {
					Kind string `json:"kind"`
				}
				_ = json.Unmarshal([]byte(candidate), &parsed)
				if parsed.Kind == "command" {
					item, found = candidate, true
					break
				}
				f.work <- candidate
			}
		} else {
			select {
			case item = <-f.work:
				found = true
			default:
			}
		}
		if found {
			var selected struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal([]byte(item), &selected)
			release := releaseFirst
			if releaseID != "" {
				if selected.ID == releaseID {
					release = releaseIDCh
				} else {
					release = nil
				}
			}
			if release != nil {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(item))
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("POST /v1/units/"+unit+"/events", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			f.badAuth = true
		}
		if f.eventFail > 0 {
			f.eventFail--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var ev event
		raw, _ := io.ReadAll(r.Body)
		f.bodies = append(f.bodies, len(raw))
		if err := json.Unmarshal(raw, &ev); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if f.legacy {
			var old struct {
				ID       string `json:"id"`
				State    string `json:"state"`
				Progress int    `json:"progress"`
				Reason   string `json:"reason"`
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&old); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		if f.refuseSeen && ev.Seen != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		if ev.ID == f.rejectID {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.events = append(f.events, ev)
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewUnstartedServer(mux)
	f.srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
		}
	}
	f.srv.Start()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeControl) setWorkCode(c int) {
	f.mu.Lock()
	f.workCode = c
	f.mu.Unlock()
}

func (f *fakeControl) setRejectID(id string) {
	f.mu.Lock()
	f.rejectID = id
	f.mu.Unlock()
}

func (f *fakeControl) seen() []event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]event(nil), f.events...)
}

// waitFor polls until the events satisfy ok.
func (f *fakeControl) waitFor(t *testing.T, what string, ok func([]event) bool) []event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ev := f.seen(); ok(ev) {
			return ev
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; events = %+v", what, f.seen())
	return nil
}

// pollCounts reports how many work polls the client made, and how many of
// them asked only for commands.
func (f *fakeControl) pollCounts() (all, commands int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls, f.commandPolls
}

func has(ev []event, id, state string) bool {
	for _, e := range ev {
		if e.ID == id && e.State == state {
			return true
		}
	}
	return false
}

func hasSubmit(r *stubRunner, id string) bool {
	for _, submitted := range r.submitted() {
		if submitted == id {
			return true
		}
	}
	return false
}

type capLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *capLog) Info(e string, _ map[string]any) {
	l.mu.Lock()
	l.lines = append(l.lines, e)
	l.mu.Unlock()
}
func (l *capLog) Error(e string, f map[string]any) { l.Info(e, f) }
func (l *capLog) count(e string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, x := range l.lines {
		if x == e {
			n++
		}
	}
	return n
}

// stubRunner holds tasks in memory and lets a test play the runner's part.
type stubRunner struct {
	mu      sync.Mutex
	obs     func(task.Task)
	tasks   []task.Task
	submits []string
	failErr error
}

func (s *stubRunner) SubmitAs(id string, _ task.Template) (task.Task, error) {
	s.mu.Lock()
	if s.failErr != nil {
		s.mu.Unlock()
		return task.Task{}, s.failErr
	}
	for _, t := range s.tasks {
		if t.ID == id {
			s.mu.Unlock()
			return task.Task{}, task.ErrDuplicate
		}
	}
	queued := 0
	for _, t := range s.tasks {
		if t.State == task.Queued {
			queued++
		}
	}
	if queued >= task.MaxQueued {
		s.mu.Unlock()
		return task.Task{}, task.ErrQueueFull
	}
	t := task.Task{ID: id, State: task.Queued}
	s.tasks = append(s.tasks, t)
	s.submits = append(s.submits, id)
	obs := s.obs
	s.mu.Unlock()
	obs(t)
	return t, nil
}

func (s *stubRunner) Cancel(id string) (task.Task, error) {
	s.mu.Lock()
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			if s.tasks[i].State != task.Queued && s.tasks[i].State != task.Running {
				s.mu.Unlock()
				return task.Task{}, task.ErrFinished
			}
			s.tasks[i].State = task.Cancelled
			t, obs := s.tasks[i], s.obs
			s.mu.Unlock()
			obs(t)
			return t, nil
		}
	}
	s.mu.Unlock()
	return task.Task{}, task.ErrNotFound
}

func (s *stubRunner) List() []task.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]task.Task(nil), s.tasks...)
}

func (s *stubRunner) Observe(f func(task.Task)) {
	s.mu.Lock()
	s.obs = f
	s.mu.Unlock()
}

// set moves a task as the runner would.
func (s *stubRunner) set(id, state string, progress int, reason string) {
	s.mu.Lock()
	var t task.Task
	found := false
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			s.tasks[i].State, s.tasks[i].Progress, s.tasks[i].Reason = state, progress, reason
			t, found = s.tasks[i], true
		}
	}
	obs := s.obs
	s.mu.Unlock()
	if !found {
		t = task.Task{ID: id, State: state, Progress: progress, Reason: reason}
	}
	obs(t)
}

func (s *stubRunner) submitted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.submits...)
}

type harness struct {
	f      *fakeControl
	run    Runner
	c      *Client
	log    *capLog
	sleeps *sleepRecorder
	cancel context.CancelFunc
	done   chan struct{}
}

type sleepRecorder struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleepRecorder) sleep(ctx context.Context, d time.Duration) bool {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	// Yielding keeps a fake that never waits from starving the other goroutines.
	time.Sleep(time.Millisecond)
	return ctx.Err() == nil
}

func (s *sleepRecorder) got() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

func start(t *testing.T, r Runner, f *fakeControl) *harness {
	t.Helper()
	return startWith(t, r, f, nil)
}

// startWith lets a test adjust the client before it runs.
func startWith(t *testing.T, r Runner, f *fakeControl, tune func(*Client)) *harness {
	t.Helper()
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	log := &capLog{}
	c := New(base, unit, token, r, log)
	rec := &sleepRecorder{}
	c.sleep = rec.sleep
	if tune != nil {
		tune(c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{f: f, run: r, c: c, log: log, sleeps: rec, cancel: cancel, done: make(chan struct{})}
	go func() { c.Run(ctx); close(h.done) }()
	t.Cleanup(func() {
		cancel()
		<-h.done
	})
	return h
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTaskDeliveredThenDone(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	h := start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "submit", func() bool { return len(r.submitted()) == 1 })
	r.set(taskA, task.Running, 3, "")
	f.waitFor(t, "running", func(ev []event) bool { return has(ev, taskA, "running") })
	r.set(taskA, task.Done, 16, "")
	ev := f.waitFor(t, "done", func(ev []event) bool { return has(ev, taskA, "done") })

	last := ev[len(ev)-1]
	if last != (event{ID: taskA, State: "done", Progress: 16}) {
		t.Errorf("last event = %+v", last)
	}
	if has(ev, taskA, "queued") {
		t.Error("queued is not a wire state and must not be posted")
	}
	if f.badAuth {
		t.Error("a request lacked the bearer token or the wait parameter")
	}
	if h.log.count("control_connected") != 1 {
		t.Errorf("control_connected logged %d times, want 1", h.log.count("control_connected"))
	}
}

func TestRedeliveredIDIsIgnored(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	f.work <- taskItem(taskA, gotoBody)
	f.work <- taskItem(taskB, gotoBody)
	waitUntil(t, "second task", func() bool { return len(r.submitted()) == 2 })
	time.Sleep(30 * time.Millisecond)
	if got := r.submitted(); len(got) != 2 || got[0] != taskA || got[1] != taskB {
		t.Errorf("submitted = %v, want each id once", got)
	}
	for _, e := range f.seen() {
		if e.State == "failed" {
			t.Errorf("a redelivery posted %+v", e)
		}
	}
}

func TestStopCancelsQueuedAndRunning(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	f.work <- taskItem(taskB, gotoBody)
	waitUntil(t, "both queued", func() bool { return len(r.submitted()) == 2 })
	r.set(taskA, task.Running, 0, "")
	f.work <- stopItem(commandID)

	ev := f.waitFor(t, "stop done", func(ev []event) bool { return has(ev, commandID, "done") })
	var terminal []string
	for _, e := range ev {
		if e.State != "running" {
			terminal = append(terminal, e.ID+":"+e.State)
		}
	}
	want := []string{taskA + ":cancelled", taskB + ":cancelled", commandID + ":done"}
	if strings.Join(terminal, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", terminal, want)
	}
}

func TestBadTemplatePostsFailed(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, `{"template":"build_house"}`)
	ev := f.waitFor(t, "failed", func(ev []event) bool { return has(ev, taskA, "failed") })
	if ev[0].Reason == "" {
		t.Error("failed event carries no reason")
	}
	if len(r.submitted()) != 0 {
		t.Error("a template that does not parse reached the runner")
	}
}

func TestClientWaitsForRunnerCapacity(t *testing.T) {
	f := newFakeControl(t)
	r := &stubRunner{}
	start(t, r, f)
	for i := 1; i <= 20; i++ {
		f.work <- fmt.Sprintf(`{"id":"%08x-0000","kind":"task","template":%s}`, i, gotoBody)
	}
	waitUntil(t, "runner queue to fill", func() bool { return len(r.submitted()) == task.MaxQueued })
	waitUntil(t, "client to pause polling at capacity", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.commandPolls > 0 && len(f.work) == 4
	})

	for i := 0; i < 20; i++ {
		var queued string
		for _, item := range r.List() {
			if item.State == task.Queued {
				queued = item.ID
				break
			}
		}
		if queued != "" {
			r.set(queued, task.Running, 0, "")
		}
		want := min(task.MaxQueued+i+1, 20)
		waitUntil(t, "next task to be delivered", func() bool { return len(r.submitted()) >= want })
	}

	ids := r.submitted()
	for _, id := range ids {
		r.set(id, task.Running, 1, "")
		r.set(id, task.Done, 1, "")
	}
	events := f.waitFor(t, "all twenty tasks to finish", func(events []event) bool {
		done := 0
		for _, e := range events {
			if e.State == task.Done {
				done++
			}
		}
		return done == 20
	})
	if len(ids) != 20 {
		t.Fatalf("submitted %d tasks", len(ids))
	}
	for _, e := range events {
		if e.State == task.Failed && e.Reason == task.ErrQueueFull.Error() {
			t.Fatalf("task %s failed because the runner queue was full", e.ID)
		}
	}
}

func TestDeliveredTaskWaitsForCapacityAfterLocalSubmit(t *testing.T) {
	f := newFakeControl(t)
	f.releaseFirst = make(chan struct{})
	r := &stubRunner{}
	r.Observe(func(task.Task) {})
	for i := 0; i < task.MaxQueued-1; i++ {
		if _, err := r.SubmitAs(fmt.Sprintf("local-%02d", i), task.Template{}); err != nil {
			t.Fatal(err)
		}
	}
	f.work <- taskItem("00000001-0000", gotoBody)
	h := start(t, r, f)
	waitUntil(t, "control poll to block", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.polls == 1
	})
	if _, err := r.SubmitAs("local-last", task.Template{}); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 20; i++ {
		f.work <- taskItem(fmt.Sprintf("%08x-0000", i), gotoBody)
	}
	close(f.releaseFirst)
	waitUntil(t, "delivered item to enter local holding", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.commandPolls > 0 && len(f.work) == 19
	})

	advance := func() {
		t.Helper()
		for _, item := range r.List() {
			if item.State == task.Queued {
				r.set(item.ID, task.Running, 0, "")
				return
			}
		}
		t.Fatal("runner had no queued task to start")
	}
	advance()
	waitUntil(t, "first held task to submit", func() bool { return hasSubmit(r, "00000001-0000") })
	for i := 2; i <= 20; i++ {
		advance()
		id := fmt.Sprintf("%08x-0000", i)
		waitUntil(t, "next work item to submit", func() bool { return hasSubmit(r, id) })
	}

	for _, item := range r.List() {
		if strings.HasPrefix(item.ID, "local-") {
			continue
		}
		r.set(item.ID, task.Running, 1, "")
		r.set(item.ID, task.Done, 1, "")
	}
	events := f.waitFor(t, "all delivered work to finish", func(events []event) bool {
		done := 0
		for _, e := range events {
			if e.State == task.Done {
				done++
			}
		}
		return done == 20
	})
	for _, e := range events {
		if e.State == task.Failed && e.Reason == task.ErrQueueFull.Error() {
			t.Fatalf("task %s failed because a local submit filled the runner", e.ID)
		}
	}
	if h.log.count("control_connected") != 1 {
		t.Fatalf("control connection log count = %d", h.log.count("control_connected"))
	}
}

func TestStopArrivesWhileRunnerIsFullAndTaskIsHeld(t *testing.T) {
	f := newFakeControl(t)
	f.releaseFirst = make(chan struct{})
	r := &stubRunner{}
	r.Observe(func(task.Task) {})
	for i := 0; i < task.MaxQueued; i++ {
		if _, err := r.SubmitAs(fmt.Sprintf("local-%02d", i), task.Template{}); err != nil {
			t.Fatal(err)
		}
	}
	r.set("local-00", task.Running, 0, "")
	f.work <- taskItem(taskA, gotoBody)
	h := start(t, r, f)
	waitUntil(t, "initial full poll", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.polls == 1
	})
	if _, err := r.SubmitAs("local-last", task.Template{}); err != nil {
		t.Fatal(err)
	}
	f.work <- stopItem(commandID)
	close(f.releaseFirst)
	waitUntil(t, "stop to arrive over command-only polling", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.commandPolls == 1 && has(f.events, commandID, task.Done)
	})
	if hasSubmit(r, taskA) {
		t.Fatal("held task was submitted after the stop")
	}
	for _, item := range r.List() {
		if item.State != task.Cancelled {
			t.Errorf("runner task %s remained %s after stop", item.ID, item.State)
		}
	}
	if h.log.count("control_connected") != 1 {
		t.Errorf("control connection log count = %d", h.log.count("control_connected"))
	}
}

func TestSubmitErrorPostsFailed(t *testing.T) {
	failed := errors.New("runner unavailable")
	f, r := newFakeControl(t), &stubRunner{failErr: failed}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	ev := f.waitFor(t, "failed", func(ev []event) bool { return has(ev, taskA, "failed") })
	if !strings.Contains(ev[0].Reason, failed.Error()) {
		t.Errorf("reason = %q", ev[0].Reason)
	}
}

func TestUnknownCommandPostsFailed(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- `{"id":"` + commandID + `","kind":"command","command":{"name":"dance"}}`
	f.waitFor(t, "failed", func(ev []event) bool { return has(ev, commandID, "failed") })
}

func TestBacksOffOnServerErrors(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	f.setWorkCode(http.StatusInternalServerError)
	h := start(t, r, f)

	waitUntil(t, "seven back-offs", func() bool { return len(h.sleeps.got()) >= 7 })
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	got := h.sleeps.got()
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("sleeps = %v, want to start %v", got, want)
		}
	}

	f.setWorkCode(0)
	waitUntil(t, "reconnect", func() bool { return h.log.count("control_connected") == 1 })
	if n := h.log.count("control_unreachable"); n != 1 {
		t.Errorf("control_unreachable logged %d times for one outage, want 1", n)
	}

	n := len(h.sleeps.got())
	f.setWorkCode(http.StatusBadGateway)
	waitUntil(t, "a fresh back-off", func() bool { return len(h.sleeps.got()) > n })
	if got := h.sleeps.got()[n]; got != time.Second {
		t.Errorf("first sleep after recovery = %v, want it reset to 1s", got)
	}
	f.setWorkCode(0)
	waitUntil(t, "second outage logged", func() bool { return h.log.count("control_unreachable") == 2 })
}

func TestLocalTasksAreNotReported(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	r.set("deadbeef00", task.Running, 1, "")
	r.set("deadbeef00", task.Done, 2, "")
	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "submit", func() bool { return len(r.submitted()) == 1 })
	r.set(taskA, task.Done, 1, "")
	ev := f.waitFor(t, "controlled task", func(ev []event) bool { return has(ev, taskA, "done") })
	for _, e := range ev {
		if e.ID == "deadbeef00" {
			t.Errorf("local task reported: %+v", e)
		}
	}
}

func TestEventPostFailureKeepsLatestAndNeverBlocks(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	f.mu.Lock()
	f.eventFail = 3
	f.mu.Unlock()
	h := start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "submit", func() bool { return len(r.submitted()) == 1 })

	began := time.Now()
	r.set(taskA, task.Running, 1, "")
	r.set(taskA, task.Done, 9, "")
	if d := time.Since(began); d > 100*time.Millisecond {
		t.Errorf("observer calls took %v while posts were failing", d)
	}

	ev := f.waitFor(t, "done after retries", func(ev []event) bool { return has(ev, taskA, "done") })
	if has(ev, taskA, "running") {
		t.Errorf("a superseded state was posted after the failures: %+v", ev)
	}
	if len(h.sleeps.got()) == 0 {
		t.Error("retries did not wait")
	}
}

// blockingSkill holds its first step open so a test can act while the runner
// is mid-step.
type blockingSkill struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSkill) Name() string                { return "goto" }
func (b *blockingSkill) Ready(belief.Snapshot) error { return nil }
func (b *blockingSkill) Step(context.Context, skill.Actuator) (skill.Status, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	return skill.Running, nil
}

type tickActuator struct {
	skill.Actuator
	n uint64
}

func (a *tickActuator) Ticks() uint64        { return a.n }
func (a *tickActuator) Pose() act.Pose       { return act.Pose{} }
func (a *tickActuator) Tick(act.Input) error { a.n++; return nil }

// A cancel that lands while the unit is mid-step must still reach
// crew-control as a cancelled event.
func TestCancelDuringStepIsReported(t *testing.T) {
	sk := &blockingSkill{entered: make(chan struct{}), release: make(chan struct{})}
	runner := task.NewRunner(
		func(task.Template) ([]skill.Skill, error) { return []skill.Skill{sk}, nil },
		&tickActuator{},
		func() belief.Snapshot { return belief.Snapshot{Spawned: true} },
		time.Minute, &capLog{},
	)
	ctx, stop := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { runner.Run(ctx); close(runDone) }()
	t.Cleanup(func() {
		select {
		case <-sk.release:
		default:
			close(sk.release)
		}
		stop()
		<-runDone
	})

	f := newFakeControl(t)
	start(t, runner, f)

	f.work <- taskItem(taskA, gotoBody)
	select {
	case <-sk.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never stepped the task")
	}

	if _, err := runner.Cancel(taskA); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(sk.release)

	ev := f.waitFor(t, "cancelled", func(ev []event) bool { return has(ev, taskA, "cancelled") })
	if ev[len(ev)-1].State != "cancelled" {
		t.Errorf("cancelled was superseded by a later event: %+v", ev)
	}
	if _, err := runner.Cancel(taskA); !errors.Is(err, task.ErrFinished) {
		t.Errorf("second Cancel = %v, want ErrFinished", err)
	}
}

func countOf(ev []event, id, state string) int {
	n := 0
	for _, e := range ev {
		if e.ID == id && e.State == state {
			n++
		}
	}
	return n
}

func TestRedeliveryOfRunningTaskReportsItsState(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "submit", func() bool { return len(r.submitted()) == 1 })
	r.set(taskA, task.Running, 5, "")
	f.waitFor(t, "running", func(ev []event) bool { return countOf(ev, taskA, "running") == 1 })

	f.work <- taskItem(taskA, gotoBody)
	ev := f.waitFor(t, "second report", func(ev []event) bool { return countOf(ev, taskA, "running") == 2 })
	if got := ev[len(ev)-1]; got != (event{ID: taskA, State: "running", Progress: 5}) {
		t.Errorf("refresh = %+v", got)
	}
	if n := len(r.submitted()); n != 1 {
		t.Errorf("task submitted %d times", n)
	}
}

func TestHeartbeatRefreshesOnlyRunningOwnedTasks(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	startWith(t, r, f, func(c *Client) { c.heartbeat = 10 * time.Millisecond })

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "submit", func() bool { return len(r.submitted()) == 1 })
	// A task from the local API is not control's to report.
	r.mu.Lock()
	r.tasks = append(r.tasks, task.Task{ID: "local-1", State: task.Running})
	r.mu.Unlock()
	r.set(taskA, task.Running, 5, "")
	f.waitFor(t, "heartbeats", func(ev []event) bool { return countOf(ev, taskA, "running") >= 4 })

	r.set(taskA, task.Done, 9, "")
	ev := f.waitFor(t, "done", func(ev []event) bool { return has(ev, taskA, "done") })
	time.Sleep(60 * time.Millisecond)
	ev = f.seen()
	if last := ev[len(ev)-1]; last.State != "done" {
		t.Errorf("heartbeat reported after the task finished: %+v", last)
	}
	for _, e := range ev {
		if e.ID == "local-1" {
			t.Errorf("heartbeat reported a task control does not own: %+v", e)
		}
	}
}

func TestRedeliveredStopLeavesNewerTasksAlone(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "A queued", func() bool { return len(r.submitted()) == 1 })
	f.work <- stopItem(commandID)
	f.waitFor(t, "first stop", func(ev []event) bool { return countOf(ev, commandID, "done") == 1 })

	f.work <- taskItem(taskB, gotoBody)
	waitUntil(t, "B queued", func() bool { return len(r.submitted()) == 2 })
	r.set(taskB, task.Running, 1, "")
	f.work <- stopItem(commandID)
	f.waitFor(t, "second stop", func(ev []event) bool { return countOf(ev, commandID, "done") == 2 })

	for _, tk := range r.List() {
		if tk.ID == taskB && tk.State != task.Running {
			t.Errorf("redelivered stop moved the newer task to %s", tk.State)
		}
	}
}

func TestRedeliveredStopDoesNotDiscardHeldTask(t *testing.T) {
	f := newFakeControl(t)
	f.rejectID = commandID
	f.releaseID = taskA
	f.releaseIDCh = make(chan struct{})
	r := &stubRunner{}
	h := start(t, r, f)
	f.work <- stopItem(commandID)
	waitUntil(t, "first stop event to be rejected", func() bool { return h.log.count("control_event_rejected") == 1 })
	for i := 0; i < task.MaxQueued-1; i++ {
		if _, err := r.SubmitAs(fmt.Sprintf("after-stop-%02d", i), task.Template{}); err != nil {
			t.Fatal(err)
		}
	}
	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "task response to be in flight", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.work) == 0 && f.polls >= 2
	})
	if _, err := r.SubmitAs("after-stop-last", task.Template{}); err != nil {
		t.Fatal(err)
	}
	close(f.releaseIDCh)
	waitUntil(t, "task to be held locally", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.commandPolls > 0
	})
	f.setRejectID("")
	f.work <- stopItem(commandID)
	f.waitFor(t, "redelivered stop acknowledgement", func(ev []event) bool { return countOf(ev, commandID, task.Done) == 1 })
	if hasSubmit(r, taskA) {
		t.Fatal("held task was submitted before runner capacity became available")
	}
	for _, item := range r.List() {
		if item.State == task.Queued {
			r.set(item.ID, task.Running, 0, "")
			break
		}
	}
	waitUntil(t, "held task to submit after capacity is available", func() bool { return hasSubmit(r, taskA) })
}

func TestRejectedEventDoesNotBlockLaterOnes(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	f.rejectID = taskA
	h := start(t, r, f)

	f.work <- taskItem(taskA, `{"template":"nope"}`)
	f.work <- taskItem(taskB, `{"template":"nope"}`)
	f.waitFor(t, "B reported", func(ev []event) bool { return has(ev, taskB, "failed") })
	waitUntil(t, "rejection logged", func() bool { return h.log.count("control_event_rejected") == 1 })
	if has(f.seen(), taskA, "failed") {
		t.Error("the rejected event was accepted")
	}
}

func TestLongReasonFitsTheControllersBodyCap(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	r.failErr = errors.New(strings.Repeat("\u00e9", 1000))
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	ev := f.waitFor(t, "failed", func(ev []event) bool { return has(ev, taskA, "failed") })
	got := ev[len(ev)-1].Reason
	if len(got) > 400 || !utf8.ValidString(got) || got == "" {
		t.Errorf("reason is %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	body, _ := json.Marshal(ev[len(ev)-1])
	if len(body) > 512 {
		t.Errorf("event body is %d bytes", len(body))
	}
}

func cancelTaskItem(id, target string) string {
	return `{"id":"` + id + `","kind":"command","command":{"name":"cancel_task","task":"` + target + `"}}`
}

func (s *stubRunner) finishWithSeen(id string, seen *skill.ChestSeen) {
	s.mu.Lock()
	var t task.Task
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			s.tasks[i].State, s.tasks[i].Seen = task.Done, seen
			t = s.tasks[i]
		}
	}
	obs := s.obs
	s.mu.Unlock()
	obs(t)
}

func TestTerminalEventCarriesWhatTheTaskSaw(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	f.work <- taskItem(taskB, gotoBody)
	waitUntil(t, "both queued", func() bool { return len(r.submitted()) == 2 })
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	r.finishWithSeen(taskA, &skill.ChestSeen{Chest: protocol.BlockPos{3, 64, -7}, Items: map[string]int{"cobblestone": 16, "dirt": 5}, At: at})
	r.set(taskB, task.Done, 0, "")

	ev := f.waitFor(t, "both done", func(ev []event) bool { return has(ev, taskA, "done") && has(ev, taskB, "done") })
	for _, e := range ev {
		switch {
		case e.ID == taskA && e.State == "done":
			if e.Seen == nil || e.Seen.Chest.X != 3 || e.Seen.Chest.Y != 64 || e.Seen.Chest.Z != -7 ||
				e.Seen.Items["cobblestone"] != 16 || e.Seen.Items["dirt"] != 5 || !e.Seen.At.Equal(at) {
				t.Errorf("seen = %+v", e.Seen)
			}
		case e.ID == taskB && e.State == "done":
			if e.Seen != nil {
				t.Errorf("a task that saw nothing reported %+v", e.Seen)
			}
		}
	}
}

func TestControllerRejectingSeenStillGetsTheState(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	f.legacy = true
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "queued", func() bool { return len(r.submitted()) == 1 })
	r.finishWithSeen(taskA, &skill.ChestSeen{Chest: protocol.BlockPos{3, 64, -7}, Items: map[string]int{"dirt": 5}, At: time.Now()})

	ev := f.waitFor(t, "done", func(ev []event) bool { return has(ev, taskA, "done") })
	for _, e := range ev {
		if e.Seen != nil {
			t.Errorf("a controller that rejects unknown fields was sent %+v", e.Seen)
		}
	}
}

func TestSeenWireShapeAndOmission(t *testing.T) {
	b, _ := json.Marshal(event{ID: "x", State: "done"})
	if strings.Contains(string(b), "seen") {
		t.Errorf("empty seen on the wire: %s", b)
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	b, _ = json.Marshal(eventOf(task.Task{ID: "x", State: "done", Seen: &skill.ChestSeen{Chest: protocol.BlockPos{1, 2, 3}, Items: map[string]int{"dirt": 2}, At: at}}))
	want := `"seen":{"chest":{"x":1,"y":2,"z":3},"items":{"dirt":2},"at":"2026-10-20T12:00:00Z"}`
	if !strings.Contains(string(b), want) {
		t.Errorf("wire = %s, want it to contain %s", b, want)
	}
}

func TestCancelTaskCancelsOnlyTheNamedTask(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	f.work <- taskItem(taskB, gotoBody)
	waitUntil(t, "both queued", func() bool { return len(r.submitted()) == 2 })
	r.set(taskA, task.Running, 0, "")
	f.work <- cancelTaskItem(commandID, taskA)

	f.waitFor(t, "cancel done", func(ev []event) bool { return has(ev, commandID, "done") && has(ev, taskA, "cancelled") })
	for _, tk := range r.List() {
		if tk.ID == taskB && tk.State != task.Queued {
			t.Errorf("task B is %s, want it left queued", tk.State)
		}
	}
}

func TestCancelTaskForAnUnknownIDStillFinishes(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- cancelTaskItem(commandID, "ffffffff-0009")
	ev := f.waitFor(t, "cancel done", func(ev []event) bool { return has(ev, commandID, "done") })
	if len(ev) != 1 {
		t.Errorf("events = %+v", ev)
	}
}

func TestCancelTaskForAFinishedTaskStillFinishes(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "A queued", func() bool { return len(r.submitted()) == 1 })
	r.set(taskA, task.Done, 0, "")
	f.work <- cancelTaskItem(commandID, taskA)
	f.waitFor(t, "cancel done", func(ev []event) bool { return has(ev, commandID, "done") })
	for _, tk := range r.List() {
		if tk.ID == taskA && tk.State != task.Done {
			t.Errorf("a finished task became %s", tk.State)
		}
	}
}

func TestRedeliveredCancelTaskIsANoOp(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "A queued", func() bool { return len(r.submitted()) == 1 })
	f.work <- cancelTaskItem(commandID, taskA)
	f.waitFor(t, "first cancel", func(ev []event) bool { return countOf(ev, commandID, "done") == 1 })

	// The runner now reuses nothing, but a task with the same id can only be
	// running again if control resubmitted it; the redelivery must not touch it.
	r.mu.Lock()
	r.tasks[0].State = task.Running
	r.mu.Unlock()
	f.work <- cancelTaskItem(commandID, taskA)
	f.waitFor(t, "second cancel", func(ev []event) bool { return countOf(ev, commandID, "done") == 2 })
	for _, tk := range r.List() {
		if tk.ID == taskA && tk.State != task.Running {
			t.Errorf("redelivered cancel_task moved the task to %s", tk.State)
		}
	}
}

func TestCancelTaskDropsAHeldTask(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{failErr: task.ErrQueueFull}
	start(t, r, f)

	// The runner refuses the task, so the client holds it back.
	f.work <- taskItem(taskB, gotoBody)
	// A client holding a task asks only for commands, so the command poll
	// says the task is held without waiting for a clock.
	waitUntil(t, "the task held back", func() bool {
		_, commands := f.pollCounts()
		return commands > 0
	})
	f.work <- cancelTaskItem(commandID, taskB)
	f.waitFor(t, "cancel done", func(ev []event) bool { return has(ev, commandID, "done") && has(ev, taskB, "cancelled") })
	r.mu.Lock()
	r.failErr = nil
	r.mu.Unlock()
	// Two more polls give the client a whole turn to submit the task it
	// held, which it must not.
	before, _ := f.pollCounts()
	waitUntil(t, "the client polled again", func() bool {
		all, _ := f.pollCounts()
		return all >= before+2
	})
	if hasSubmit(r, taskB) {
		t.Error("a cancelled task was submitted after it was held")
	}
}

func TestOversizedSeenIsTrimmedSoTheStateStillArrives(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "queued", func() bool { return len(r.submitted()) == 1 })
	items := map[string]int{"item#12": 9999}
	for i := 0; i < 27; i++ {
		items[strings.Repeat("a", 28)+string(rune('a'+i%26))+string(rune('a'+i/26))] = 100 - i
	}
	r.mu.Lock()
	r.tasks[0].State, r.tasks[0].Reason = task.Failed, strings.Repeat("r", 400)
	r.tasks[0].Seen = &skill.ChestSeen{Chest: protocol.BlockPos{1, 2, 3}, Items: items, At: time.Now()}
	t0, obs := r.tasks[0], r.obs
	r.mu.Unlock()
	obs(t0)

	ev := f.waitFor(t, "failed", func(ev []event) bool { return has(ev, taskA, "failed") })
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.bodies {
		if n > 1024 {
			t.Errorf("event body of %d bytes exceeds the controller's cap", n)
		}
	}
	for _, e := range ev {
		if e.Seen == nil {
			continue
		}
		for name := range e.Seen.Items {
			if strings.HasPrefix(name, "item#") {
				t.Errorf("unnameable item on the wire: %q", name)
			}
		}
		if e.Seen.Items[strings.Repeat("a", 28)+"aa"] != 100 {
			t.Errorf("highest count was not kept first: %v", e.Seen.Items)
		}
		if !e.Seen.Partial {
			t.Errorf("trimmed seen is not marked partial: %+v", e.Seen)
		}
	}
}

func TestFitDropsUnnameableItemsAndKeepsTheBiggest(t *testing.T) {
	seen := &Seen{Items: map[string]int{"item#5": 500, "Bad-Name": 400, "dirt": 3, "cobblestone": 70, "music_disc_13": 1}}
	got := fit(event{ID: "x", State: "done", Seen: seen})
	if len(got.Seen.Items) != 3 || got.Seen.Items["music_disc_13"] != 1 || got.Seen.Items["dirt"] != 3 || got.Seen.Items["cobblestone"] != 70 || !got.Seen.Partial {
		t.Errorf("seen = %+v", got.Seen)
	}
	b, _ := json.Marshal(fit(event{ID: "x", State: "done", Seen: &Seen{Items: map[string]int{"item#5": 1}}}))
	if strings.Contains(string(b), "item#") || !strings.Contains(string(b), `"items":{}`) || !strings.Contains(string(b), `"partial":true`) {
		t.Errorf("wire = %s", b)
	}
}

func TestFitLeavesACompleteSeenUnmarked(t *testing.T) {
	for name, items := range map[string]map[string]int{
		"some":  {"dirt": 3, "cobblestone": 70},
		"empty": {},
	} {
		b, _ := json.Marshal(fit(event{ID: "x", State: "done", Seen: &Seen{Items: items}}))
		var wire struct {
			Seen map[string]json.RawMessage `json:"seen"`
		}
		if err := json.Unmarshal(b, &wire); err != nil || wire.Seen == nil {
			t.Fatalf("%s: wire = %s err=%v", name, b, err)
		}
		if _, ok := wire.Seen["partial"]; ok {
			t.Errorf("%s: a complete chest was marked partial: %s", name, b)
		}
	}
}

func TestTooLargeAnswerRetriesWithoutSeen(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	f.mu.Lock()
	f.refuseSeen = true
	f.mu.Unlock()
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	waitUntil(t, "queued", func() bool { return len(r.submitted()) == 1 })
	r.finishWithSeen(taskA, &skill.ChestSeen{Items: map[string]int{"dirt": 2}, At: time.Now()})
	ev := f.waitFor(t, "done", func(ev []event) bool { return has(ev, taskA, "done") })
	for _, e := range ev {
		if e.ID == taskA && e.Seen != nil {
			t.Errorf("accepted event still carries seen: %+v", e)
		}
	}
}

// dnsDown fails the first n round trips the way a resolver that cannot be
// reached does, and then lets requests through.
type dnsDown struct {
	next http.RoundTripper
	mu   sync.Mutex
	left int
}

func (d *dnsDown) RoundTrip(r *http.Request) (*http.Response, error) {
	d.mu.Lock()
	fail := d.left > 0
	if fail {
		d.left--
	}
	d.mu.Unlock()
	if fail {
		return nil, &net.DNSError{Err: "i/o timeout", Name: "crew-control", Server: "10.96.0.10:53", IsTimeout: true}
	}
	return d.next.RoundTrip(r)
}

// A unit that starts while the cluster's DNS cannot resolve crew-control must
// keep running and retry with a growing delay, then connect once it can.
func TestStartupDNSTimeoutIsRetriedWithBackoff(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{}
	h := startWith(t, r, f, func(c *Client) { c.http = &http.Client{Transport: &dnsDown{next: http.DefaultTransport, left: 4}} })

	waitUntil(t, "connected after the lookups recover", func() bool { return h.log.count("control_connected") == 1 })
	got := h.sleeps.got()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(got) < len(want) {
		t.Fatalf("sleeps = %v, want %v before connecting", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("sleeps = %v, want to start %v", got, want)
		}
	}
	if n := h.log.count("control_unreachable"); n != 1 {
		t.Errorf("control_unreachable logged %d times for one outage, want 1", n)
	}
	select {
	case <-h.done:
		t.Fatal("the client stopped instead of retrying")
	default:
	}
}
