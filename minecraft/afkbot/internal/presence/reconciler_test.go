package presence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/minecraft-afk-bot/internal/logging"
	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

// fakeAgent serves the two bot routes for afk-bot-1 the way the contract
// says the agent does: ETag "<version>-<effective>", 304 on a match.
type fakeAgent struct {
	mu          sync.Mutex
	state       presenceapi.State
	version     int
	fail        int           // non-zero: answer every request with this status
	hang        time.Duration // non-zero: sleep before answering a GET
	rawBody     string        // non-empty: serve this body on GET instead
	ifNoneMatch []string
	notModified int
	statuses    []presenceapi.Status
}

func (f *fakeAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hang := f.hang
	f.mu.Unlock()
	if hang > 0 && r.Method == http.MethodGet {
		time.Sleep(hang)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_ = json.NewEncoder(w).Encode(presenceapi.Error{Code: "forbidden", Message: "refused"})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/actors/afk-bot-1/presence":
		etag := fmt.Sprintf(`"%d-%s"`, f.version, f.state)
		f.ifNoneMatch = append(f.ifNoneMatch, r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") == etag {
			f.notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		if f.rawBody != "" {
			_, _ = w.Write([]byte(f.rawBody))
			return
		}
		_ = json.NewEncoder(w).Encode(presenceapi.Presence{ActorID: "afk-bot-1", Effective: f.state, Default: presenceapi.StatePresent})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/actors/afk-bot-1/status":
		var st presenceapi.Status
		_ = json.NewDecoder(r.Body).Decode(&st)
		f.statuses = append(f.statuses, st)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAgent) set(state presenceapi.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
	f.version++
}

func (f *fakeAgent) with(fn func(f *fakeAgent)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type logLine struct {
	level, event string
	fields       logging.Fields
}

type recorder struct {
	mu    sync.Mutex
	lines []logLine
}

func (r *recorder) add(level, event string, f logging.Fields) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, logLine{level, event, f})
}
func (r *recorder) Info(e string, f logging.Fields)  { r.add("info", e, f) }
func (r *recorder) Warn(e string, f logging.Fields)  { r.add("warn", e, f) }
func (r *recorder) Error(e string, f logging.Fields) { r.add("error", e, f) }

func (r *recorder) all(event string) []logLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []logLine
	for _, l := range r.lines {
		if l.event == event {
			out = append(out, l)
		}
	}
	return out
}

func newTestReconciler(t *testing.T, agent *fakeAgent, initial presenceapi.State) (*Reconciler, *httptest.Server, *recorder) {
	t.Helper()
	srv := httptest.NewServer(agent)
	t.Cleanup(srv.Close)
	log := &recorder{}
	hc := srv.Client()
	hc.Timeout = 200 * time.Millisecond
	r := NewReconciler(NewClient(srv.URL, "afk-bot-1", "tok", hc), initial, time.Hour, "test", log)
	r.now = func() time.Time { return time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC) }
	return r, srv, log
}

// admitWithin reports whether Admit returns within d. The admitted session
// lives until the test ends, so a test can watch it being cancelled by a park.
func admitWithin(t *testing.T, r *Reconciler, d time.Duration) (context.Context, bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	got := make(chan context.Context, 1)
	go func() {
		if sess, _, err := r.Admit(ctx); err == nil {
			got <- sess
		}
	}()
	select {
	case sess := <-got:
		return sess, true
	case <-time.After(d):
		cancel()
		return nil, false
	}
}

func TestNeverAnsweredStartUsesDefault(t *testing.T) {
	for _, initial := range []presenceapi.State{presenceapi.StatePresent, presenceapi.StateParked} {
		t.Run(string(initial), func(t *testing.T) {
			r, srv, _ := newTestReconciler(t, &fakeAgent{state: presenceapi.StatePresent}, initial)
			srv.Close()
			r.tick(context.Background())

			if got := r.Desired(); got != initial {
				t.Errorf("Desired = %q after an unanswered poll, want the default %q", got, initial)
			}
			if _, admitted := admitWithin(t, r, 50*time.Millisecond); admitted != (initial == presenceapi.StatePresent) {
				t.Errorf("admitted = %v with default %q", admitted, initial)
			}
		})
	}
}

func TestParkCancelsAdmittedSession(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StatePresent}
	r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)

	sess, admitted := admitWithin(t, r, time.Second)
	if !admitted {
		t.Fatal("present bot was not admitted")
	}
	agent.set(presenceapi.StateParked)
	r.tick(context.Background())

	select {
	case <-sess.Done():
	case <-time.After(time.Second):
		t.Fatal("session context survived a park")
	}
	if got := log.all("presence_changed"); len(got) != 1 || got[0].fields["to"] != "parked" {
		t.Errorf("presence_changed lines = %+v, want one to parked", got)
	}
}

func TestResumeAdmitsAfterPark(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked}
	r, _, _ := newTestReconciler(t, agent, presenceapi.StateParked)

	admitted := make(chan struct{})
	go func() {
		if _, _, err := r.Admit(context.Background()); err == nil {
			close(admitted)
		}
	}()
	r.tick(context.Background())
	select {
	case <-admitted:
		t.Fatal("admitted while parked")
	case <-time.After(50 * time.Millisecond):
	}

	agent.set(presenceapi.StatePresent)
	r.tick(context.Background())
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("not admitted after resume")
	}
}

func TestUnreachableAgentKeepsLastAnswer(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked}
	r, srv, log := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.tick(context.Background())
	if r.Desired() != presenceapi.StateParked {
		t.Fatalf("Desired = %q, want parked from the agent", r.Desired())
	}

	srv.Close()
	r.tick(context.Background())
	r.tick(context.Background())

	if r.Desired() != presenceapi.StateParked {
		t.Errorf("Desired = %q after the agent went away, want the last answer parked", r.Desired())
	}
	lines := log.all("presence_fetch_unreachable")
	if len(lines) != 1 {
		t.Fatalf("presence_fetch_unreachable logged %d times over two failed polls, want once", len(lines))
	}
	if lines[0].level != "warn" || lines[0].fields["acting_on"] != "parked" {
		t.Errorf("line = %+v, want warn acting_on parked", lines[0])
	}
}

func TestHungAgentKeepsLastAnswer(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked}
	r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.tick(context.Background())

	agent.with(func(f *fakeAgent) { f.hang = time.Second; f.state = presenceapi.StatePresent; f.version++ })
	started := time.Now()
	r.poll(context.Background())

	if took := time.Since(started); took > 900*time.Millisecond {
		t.Errorf("poll took %v against a hung agent, want it bounded by the client timeout", took)
	}
	if r.Desired() != presenceapi.StateParked {
		t.Errorf("Desired = %q, want the last answer parked", r.Desired())
	}
	if len(log.all("presence_fetch_unreachable")) != 1 {
		t.Error("a timed-out poll was not logged as unreachable")
	}
}

func TestNotModifiedKeepsState(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked, version: 4}
	r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.tick(context.Background())
	r.tick(context.Background())

	agent.with(func(f *fakeAgent) {
		if len(f.ifNoneMatch) != 2 || f.ifNoneMatch[0] != "" || f.ifNoneMatch[1] != `"4-parked"` {
			t.Errorf("If-None-Match sent = %q, want none then the ETag from the first answer", f.ifNoneMatch)
		}
		if f.notModified != 1 {
			t.Errorf("304s served = %d, want 1", f.notModified)
		}
	})
	if r.Desired() != presenceapi.StateParked {
		t.Errorf("Desired = %q after a 304, want parked kept", r.Desired())
	}
	if n := len(log.all("presence_changed")); n != 1 {
		t.Errorf("presence_changed logged %d times, want once (the 304 changes nothing)", n)
	}
}

func TestRejectedTokenIsLoggedNotFatal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			agent := &fakeAgent{state: presenceapi.StateParked, fail: status}
			r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)

			ctx, cancel := context.WithCancel(context.Background())
			r.interval = 10 * time.Millisecond
			done := make(chan struct{})
			go func() { r.Run(ctx); close(done) }()
			time.Sleep(100 * time.Millisecond)

			select {
			case <-done:
				t.Fatal("Run returned on a rejected token")
			default:
			}
			if r.Desired() != presenceapi.StatePresent {
				t.Errorf("Desired = %q, want the default kept", r.Desired())
			}
			lines := log.all("presence_fetch_rejected")
			if len(lines) != 1 {
				t.Fatalf("presence_fetch_rejected logged %d times over repeated polls, want once", len(lines))
			}
			if lines[0].level != "error" || lines[0].fields["status"] != status {
				t.Errorf("line = %+v, want error with status %d", lines[0], status)
			}

			agent.with(func(f *fakeAgent) { f.fail = 0 })
			time.Sleep(100 * time.Millisecond)
			cancel()
			<-done
			if r.Desired() != presenceapi.StateParked {
				t.Errorf("Desired = %q after the token was fixed, want parked", r.Desired())
			}
			if len(log.all("presence_fetch_recovered")) != 1 {
				t.Error("recovery was not logged once")
			}
		})
	}
}

// 5xx means the agent itself is broken, not that the bot did anything wrong
// -- the same as a dropped connection -- so it must warn as unreachable
// rather than error as rejected.
func TestServerErrorClassifiesAsUnreachable(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked, fail: http.StatusServiceUnavailable}
	r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.tick(context.Background())

	lines := log.all("presence_fetch_unreachable")
	if len(lines) != 1 {
		t.Fatalf("presence_fetch_unreachable logged %d times, want once", len(lines))
	}
	if lines[0].level != "warn" || lines[0].fields["status"] != http.StatusServiceUnavailable {
		t.Errorf("line = %+v, want warn with status %d", lines[0], http.StatusServiceUnavailable)
	}
}

// 408 and 429 are the agent asking the bot to slow down or retry, not a bad
// token or an unregistered actor -- they heal on their own, unlike the rest
// of 4xx, so they must warn as unreachable rather than error as rejected.
func TestTimeoutAndTooManyRequestsClassifyAsUnreachable(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			agent := &fakeAgent{state: presenceapi.StateParked, fail: status}
			r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)
			r.tick(context.Background())

			if lines := log.all("presence_fetch_rejected"); len(lines) != 0 {
				t.Errorf("presence_fetch_rejected logged for status %d: %+v", status, lines)
			}
			lines := log.all("presence_fetch_unreachable")
			if len(lines) != 1 || lines[0].level != "warn" || lines[0].fields["status"] != status {
				t.Errorf("presence_fetch_unreachable = %+v, want one warn line with status %d", lines, status)
			}
		})
	}
}

// The streak only re-logs on a kind change, so an unreachable agent that
// starts rejecting the token must produce a second line rather than being
// silently absorbed into the first streak.
func TestFailureKindChangeLogsAgain(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked, fail: http.StatusServiceUnavailable}
	r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.tick(context.Background())

	agent.with(func(f *fakeAgent) { f.fail = http.StatusUnauthorized })
	r.tick(context.Background())

	unreachable := log.all("presence_fetch_unreachable")
	rejected := log.all("presence_fetch_rejected")
	if len(unreachable) != 1 {
		t.Errorf("presence_fetch_unreachable logged %d times, want once", len(unreachable))
	}
	if len(rejected) != 1 {
		t.Errorf("presence_fetch_rejected logged %d times, want once after the kind changed", len(rejected))
	}
}

func TestInvalidStateKeepsLastAnswer(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StateParked}
	r, _, log := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.tick(context.Background())

	agent.with(func(f *fakeAgent) { f.version++; f.rawBody = `{"actor_id":"afk-bot-1","effective":"hibernating"}` })
	r.tick(context.Background())

	if r.Desired() != presenceapi.StateParked {
		t.Errorf("Desired = %q, want parked kept over an unknown state", r.Desired())
	}
	if lines := log.all("presence_fetch_invalid"); len(lines) != 1 || lines[0].level != "error" {
		t.Errorf("presence_fetch_invalid = %+v, want one error line", lines)
	}
}

func TestReportsObservedStatus(t *testing.T) {
	agent := &fakeAgent{state: presenceapi.StatePresent}
	r, _, _ := newTestReconciler(t, agent, presenceapi.StatePresent)
	r.Connected(true)
	r.tick(context.Background())
	r.Connected(false)
	agent.set(presenceapi.StateParked)
	r.tick(context.Background())

	agent.with(func(f *fakeAgent) {
		if len(f.statuses) != 2 {
			t.Fatalf("statuses posted = %d, want one per tick", len(f.statuses))
		}
		first, second := f.statuses[0], f.statuses[1]
		if !first.Connected || first.ObservedState != presenceapi.StatePresent || first.ProcessVersion != "test" {
			t.Errorf("first status = %+v, want connected present from version test", first)
		}
		if !first.LastSeen.Equal(time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC)) {
			t.Errorf("LastSeen = %v, want the reconciler's clock", first.LastSeen)
		}
		if second.Connected || second.ObservedState != presenceapi.StateParked {
			t.Errorf("second status = %+v, want disconnected parked", second)
		}
	})
}

func TestAlwaysPresentAdmitsAtOnceAndNeverCancels(t *testing.T) {
	sess, release, err := AlwaysPresent{}.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	defer release()
	select {
	case <-sess.Done():
		t.Fatal("AlwaysPresent session context is already done")
	case <-time.After(20 * time.Millisecond):
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (AlwaysPresent{}).Admit(ctx); err == nil {
		t.Error("AlwaysPresent admitted on a cancelled context")
	}
}

// gate.go's contract is that Admit fails only when ctx ends, and does so
// before admitting -- AlwaysPresent checks ctx.Err() first for the same
// reason. Without that ordering here, a reconciler holding a present desired
// state would hand back a session built on an already-cancelled context
// instead of the error the caller is checking for.
func TestAdmitReturnsCtxErrFirstEvenWhenDesiredIsPresent(t *testing.T) {
	r, _, _ := newTestReconciler(t, &fakeAgent{state: presenceapi.StatePresent}, presenceapi.StatePresent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := r.Admit(ctx); err == nil {
		t.Error("Admit admitted on an already-cancelled context")
	}
}

func TestAdmitReturnsWhenContextEndsWhileParked(t *testing.T) {
	r, _, _ := newTestReconciler(t, &fakeAgent{state: presenceapi.StateParked}, presenceapi.StateParked)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, _, err := r.Admit(ctx); errc <- err }()
	cancel()

	select {
	case err := <-errc:
		if err == nil {
			t.Error("Admit admitted a parked bot on shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("Admit did not return when its context ended")
	}
}
