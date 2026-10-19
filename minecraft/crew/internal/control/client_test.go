package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

	mu        sync.Mutex
	events    []event
	polls     int
	workCode  int // when non-zero, the work endpoint answers this instead
	eventFail int // how many event posts to refuse before accepting
	badAuth   bool
	rejectID  string // events about this id get a 400
	conns     int    // connections the server accepted
}

func newFakeControl(t *testing.T) *fakeControl {
	t.Helper()
	f := &fakeControl{work: make(chan string, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/units/"+unit+"/work", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.polls++
		code := f.workCode
		if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Query().Get("wait") != "25s" {
			f.badAuth = true
		}
		f.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			return
		}
		select {
		case item := <-f.work:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(item))
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
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			w.WriteHeader(http.StatusBadRequest)
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

func has(ev []event, id, state string) bool {
	for _, e := range ev {
		if e.ID == id && e.State == state {
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

func TestSubmitErrorPostsFailed(t *testing.T) {
	f, r := newFakeControl(t), &stubRunner{failErr: task.ErrQueueFull}
	start(t, r, f)

	f.work <- taskItem(taskA, gotoBody)
	ev := f.waitFor(t, "failed", func(ev []event) bool { return has(ev, taskA, "failed") })
	if !strings.Contains(ev[0].Reason, task.ErrQueueFull.Error()) {
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
