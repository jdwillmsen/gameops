package live

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBridge answers GET /script from a script of responses, then holds
// every later request until the test ends, as a long poll with nothing new.
type fakeBridge struct {
	t       *testing.T
	mu      sync.Mutex
	answers []func(w http.ResponseWriter, r *http.Request)
	seen    []string
	auth    []string
	*httptest.Server
}

func newFakeBridge(t *testing.T, answers ...func(http.ResponseWriter, *http.Request)) *fakeBridge {
	b := &fakeBridge{t: t, answers: answers}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/script" {
			http.NotFound(w, r)
			return
		}
		b.mu.Lock()
		b.seen = append(b.seen, r.URL.RawQuery)
		b.auth = append(b.auth, r.Header.Get("Authorization"))
		var answer func(http.ResponseWriter, *http.Request)
		if len(b.answers) > 0 {
			answer, b.answers = b.answers[0], b.answers[1:]
		}
		b.mu.Unlock()
		if answer == nil {
			<-r.Context().Done()
			return
		}
		answer(w, r)
	}))
	t.Cleanup(b.Close)
	return b
}

func (b *fakeBridge) queries() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string{}, b.seen...)
}

func body(s string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, s)
	}
}

func status(code int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func run(t *testing.T, s *Source) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the source did not stop with its context")
		}
	})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("never happened: %s", what)
}

func stamp(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339Nano) }

func TestSourceFeedsTheStore(t *testing.T) {
	// The first record's data is the JSON object, as the bridge sends it;
	// the second is a string holding it, which is read as well.
	asString := fmt.Sprintf("%q", mobsJSON(2, "nether", 4))
	bridge := newFakeBridge(t,
		body(`{"records":[{"id":41,"at":"`+stamp(-2*time.Millisecond)+`","data":`+mobsJSON(1, "overworld", 3)+`},`+
			`{"id":42,"at":"`+stamp(-time.Millisecond)+`","data":`+asString+`}]}`),
		body(`{"records":[]}`),
	)
	l := New(10*time.Second, 1000, nil)
	sub, _ := l.Subscribe("overworld")
	run(t, &Source{Poller: NewBridge(bridge.URL+"/", "bridge-token"), Layer: l, Wait: 2 * time.Second, retry: time.Millisecond})

	if f := decode(t, recv(t, sub)); len(f.Mobs) != 3 || f.Stale {
		t.Fatalf("frame = %+v", f)
	}
	if f := l.Store.Snapshot("nether", time.Now()); len(f.Mobs) != 4 {
		t.Errorf("nether = %+v", f)
	}
	eventually(t, "a poll from the new cursor", func() bool { return len(bridge.queries()) >= 3 })
	q := bridge.queries()
	if q[0] != "since=0&wait=2000" || q[1] != "since=42&wait=2000" || q[2] != "since=42&wait=2000" {
		t.Errorf("queries = %v", q)
	}
	for _, a := range bridge.auth {
		if a != "Bearer bridge-token" {
			t.Errorf("Authorization = %q", a)
		}
	}
}

func TestSourceBacksOffAndRecovers(t *testing.T) {
	bridge := newFakeBridge(t,
		status(http.StatusTooManyRequests),
		status(http.StatusInternalServerError),
		body(`not json`),
		body(`{"records":[{"id":1,"at":"`+stamp(0)+`","data":`+mobsJSON(1, "end", 2)+`}],"gap":true}`),
	)
	l := New(10*time.Second, 1000, nil)
	sub, _ := l.Subscribe("end")
	run(t, &Source{Poller: NewBridge(bridge.URL, "tok"), Layer: l, Wait: time.Second, retry: time.Millisecond})

	if f := decode(t, recv(t, sub)); len(f.Mobs) != 2 {
		t.Fatalf("frame after three failures = %+v", f)
	}
	// Nothing was read while it failed, so nothing moved the cursor.
	for i, q := range bridge.queries()[:4] {
		if q != "since=0&wait=1000" {
			t.Errorf("query %d = %s", i, q)
		}
	}
}

func TestRetryDelayDoublesToItsCeiling(t *testing.T) {
	s := &Source{}
	for failures, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 5: 16 * time.Second, 6: 30 * time.Second, 60: 30 * time.Second} {
		if got := s.retryDelay(failures); got != want {
			t.Errorf("after %d failures = %s, want %s", failures, got, want)
		}
	}
}

// The request carries the bridge's token. Wherever a redirect points, it is
// not the bridge.
func TestSourceRefusesRedirects(t *testing.T) {
	var followed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		fmt.Fprint(w, `{"records":[]}`)
	}))
	defer elsewhere.Close()
	bridge := newFakeBridge(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/script", http.StatusTemporaryRedirect)
	})

	_, err := NewBridge(bridge.URL, "tok").Poll(context.Background(), 0, time.Second)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("err = %v, want the redirect reported as a failure", err)
	}
	if followed.Load() != 0 {
		t.Error("the redirect was followed, token and all")
	}
}

// Ids restart at 1 with the bridge, which answers a cursor it has not
// reached with everything it holds and a flag. The next poll has to ask
// from where that response ended, not from the old cursor.
func TestSourceFollowsTheBridgeThroughItsRestart(t *testing.T) {
	bridge := newFakeBridge(t,
		body(`{"records":[{"id":5000,"at":"`+stamp(-time.Second)+`","data":`+mobsJSON(700, "overworld", 1)+`}]}`),
		body(`{"records":[{"id":1,"at":"`+stamp(0)+`","data":`+mobsJSON(1, "overworld", 2)+`},`+
			`{"id":2,"at":"`+stamp(0)+`","data":`+mobsJSON(2, "overworld", 6)+`}],"gap":true}`),
	)
	l := New(10*time.Second, 1000, nil)
	run(t, &Source{Poller: NewBridge(bridge.URL, "tok"), Layer: l, Wait: time.Second, retry: time.Millisecond})

	eventually(t, "a poll after the restart", func() bool { return len(bridge.queries()) >= 3 })
	if q := bridge.queries(); q[1] != "since=5000&wait=1000" || q[2] != "since=2&wait=1000" {
		t.Errorf("queries = %v", q)
	}
	if f := l.Store.Snapshot("overworld", time.Now()); len(f.Mobs) != 6 {
		t.Errorf("after the restart: %+v", f)
	}
}

// The bridge holds only a few callers waiting and says when to come back.
func TestSourceWaitsAsLongAsABusyBridgeAsks(t *testing.T) {
	_, err := NewBridge(newFakeBridge(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}).URL, "tok").Poll(context.Background(), 0, time.Second)
	busy, ok := errors.AsType[busyError](err)
	if !errors.Is(err, ErrBusy) || !ok || busy.after != 7*time.Second {
		t.Fatalf("err = %#v, want busy for 7s", err)
	}

	// Asked to wait a second, it waits that second and not its own first
	// retry delay, which here is a millisecond.
	bridge := newFakeBridge(t,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		},
		body(`{"records":[]}`),
	)
	began := time.Now()
	run(t, &Source{Poller: NewBridge(bridge.URL, "tok"), Layer: New(time.Second, 10, nil), Wait: time.Second, retry: time.Millisecond})
	eventually(t, "a second poll", func() bool { return len(bridge.queries()) >= 2 })
	if waited := time.Since(began); waited < 900*time.Millisecond {
		t.Errorf("asked again after %s, having been told to wait a second", waited)
	}
}

func TestSourceStopsWithItsContextDuringAPoll(t *testing.T) {
	bridge := newFakeBridge(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Source{Poller: NewBridge(bridge.URL, "tok"), Layer: New(time.Second, 10, nil), Wait: 20 * time.Second}).Run(ctx)
	}()
	eventually(t, "a poll in flight", func() bool { return len(bridge.queries()) == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("still running after its context ended")
	}
}
