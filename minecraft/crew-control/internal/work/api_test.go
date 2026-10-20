package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

const token = "s3cret"

type rig struct {
	api      *API
	store    *Memory
	srv      *httptest.Server
	notified atomic.Value
}

func newRig(t *testing.T, s Store) *rig {
	t.Helper()
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"},{"id":"crew-02","name":"Basalt","url":"http://crew-02:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{}
	if m, ok := s.(*Memory); ok {
		r.store = m
	}
	mux := http.NewServeMux()
	r.api = Routes(mux, s, units, token, func(u string) { r.notified.Store(u) })
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rig) do(t *testing.T, method, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPollReturnsQueuedTaskAndCommandInWireFormat(t *testing.T) {
	r := newRig(t, NewMemory())
	task := must(t, r.store, "crew-01", KindTask, tpl)
	cmd := must(t, r.store, "crew-01", KindCommand, stop)

	got := readBody(t, r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil))
	if want := fmt.Sprintf(`{"id":%q,"kind":"command","command":{"name":"stop"}}`, cmd.ID); got != want {
		t.Fatalf("command: %s\nwant %s", got, want)
	}
	got = readBody(t, r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil))
	if want := fmt.Sprintf(`{"id":%q,"kind":"task","template":{"kind":"goto","x":1}}`, task.ID); got != want {
		t.Fatalf("task: %s\nwant %s", got, want)
	}
}

func TestCommandOnlyPollNeverReturnsTasks(t *testing.T) {
	r := newRig(t, NewMemory())
	task := must(t, r.store, "crew-01", KindTask, tpl)
	resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s&kinds=command", "", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("command-only poll returned task %s with status %d", task.ID, resp.StatusCode)
	}
	command := must(t, r.store, "crew-01", KindCommand, stop)
	r.api.Wake("crew-01")
	got := readBody(t, r.do(t, "GET", "/v1/units/crew-01/work?wait=1s&kinds=command", "", nil))
	if want := fmt.Sprintf(`{"id":%q,"kind":"command","command":{"name":"stop"}}`, command.ID); got != want {
		t.Fatalf("command-only response %s, want %s", got, want)
	}
}

func TestPollRejectsUnknownOrRepeatedKinds(t *testing.T) {
	r := newRig(t, NewMemory())
	for _, query := range []string{"kinds=task", "kinds=", "kinds=command&kinds=command"} {
		resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s&"+query, "", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("query %q: status %d, want 400", query, resp.StatusCode)
		}
	}
}

func TestPollTimesOutWith204(t *testing.T) {
	r := newRig(t, NewMemory())
	start := time.Now()
	resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 3*time.Second {
		t.Fatalf("waited %v", d)
	}
}

func TestPollReturnsAtOnceWhenWoken(t *testing.T) {
	r := newRig(t, NewMemory())
	done := make(chan string, 1)
	go func() {
		done <- readBody(t, r.do(t, "GET", "/v1/units/crew-01/work?wait=20s", "", nil))
	}()
	time.Sleep(200 * time.Millisecond)
	it := must(t, r.store, "crew-01", KindTask, tpl)
	r.api.Wake("crew-01")
	select {
	case body := <-done:
		if !strings.Contains(body, it.ID.String()) {
			t.Fatalf("body %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not return after wake")
	}
}

func TestWakeForAnotherUnitDoesNotEndThePoll(t *testing.T) {
	r := newRig(t, NewMemory())
	start := time.Now()
	go func() { time.Sleep(100 * time.Millisecond); r.api.Wake("crew-02") }()
	resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil)
	if resp.StatusCode != 204 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("status %d after %v", resp.StatusCode, time.Since(start))
	}
}

func TestWaitIsClampedAndDefaulted(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": maxWait, "nonsense": maxWait, "0s": time.Second, "-5s": time.Second,
		"1h": maxWait, "3s": 3 * time.Second,
	} {
		if got := parseWait(in); got != want {
			t.Errorf("parseWait(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEventUpdatesItem(t *testing.T) {
	r := newRig(t, NewMemory())
	it := must(t, r.store, "crew-01", KindTask, tpl)
	resp := r.do(t, "POST", "/v1/units/crew-01/events",
		fmt.Sprintf(`{"id":%q,"state":"running","progress":40}`, it.ID), nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got, _ := r.store.Recent(ctx, "crew-01", 1)
	if got[0].State != Running || got[0].Progress != 40 {
		t.Fatalf("%+v", got[0])
	}
	if r.notified.Load() != "crew-01" {
		t.Fatal("notify not called")
	}

	r.do(t, "POST", "/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"done","progress":100}`, it.ID), nil)
	resp = r.do(t, "POST", "/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"running","progress":1}`, it.ID), nil)
	got, _ = r.store.Recent(ctx, "crew-01", 1)
	if resp.StatusCode != 204 || got[0].State != Done {
		t.Fatalf("terminal event: status %d state %s", resp.StatusCode, got[0].State)
	}
}

func TestEventRefusals(t *testing.T) {
	r := newRig(t, NewMemory())
	it := must(t, r.store, "crew-01", KindTask, tpl)
	for name, c := range map[string]struct {
		path, body string
		want       int
	}{
		"unknown id":    {"/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"running"}`, uuid.New()), 404},
		"other unit":    {"/v1/units/crew-02/events", fmt.Sprintf(`{"id":%q,"state":"running"}`, it.ID), 404},
		"bad state":     {"/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"queued"}`, it.ID), 400},
		"bad id":        {"/v1/units/crew-01/events", `{"id":"x","state":"running"}`, 400},
		"unknown field": {"/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"running","x":1}`, it.ID), 400},
		"trailing data": {"/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"running"} {}`, it.ID), 400},
		"too large":     {"/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"failed","reason":%q}`, it.ID, strings.Repeat("a", 5000)), 413},
	} {
		if resp := r.do(t, "POST", c.path, c.body, nil); resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, c.want)
		}
	}
	if got, _ := r.store.Recent(ctx, "crew-01", 1); got[0].State != Queued {
		t.Fatalf("a refused event changed the item: %+v", got[0])
	}
}

func TestRefusals(t *testing.T) {
	r := newRig(t, NewMemory())
	for name, c := range map[string]struct {
		method, path string
		hdr          map[string]string
		want         int
	}{
		"no token":     {"GET", "/v1/units/crew-01/work?wait=1s", map[string]string{"Authorization": ""}, 401},
		"wrong token":  {"GET", "/v1/units/crew-01/work?wait=1s", map[string]string{"Authorization": "Bearer nope"}, 401},
		"wrong scheme": {"GET", "/v1/units/crew-01/work?wait=1s", map[string]string{"Authorization": "Basic " + token}, 401},
		"xff":          {"GET", "/v1/units/crew-01/work?wait=1s", map[string]string{"X-Forwarded-For": "1.2.3.4"}, 404},
		"unknown unit": {"GET", "/v1/units/crew-99/work?wait=1s", nil, 404},
		"events token": {"POST", "/v1/units/crew-01/events", map[string]string{"Authorization": "Bearer nope"}, 401},
		"events xff":   {"POST", "/v1/units/crew-01/events", map[string]string{"X-Forwarded-For": "1.2.3.4"}, 404},
		"events unit":  {"POST", "/v1/units/crew-99/events", nil, 404},
	} {
		if resp := r.do(t, c.method, c.path, "{}", c.hdr); resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, c.want)
		}
	}
}

func TestStoreNotConnectedAnswers503(t *testing.T) {
	r := newRig(t, &Lazy{})
	if resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil); resp.StatusCode != 503 {
		t.Fatalf("work: %d", resp.StatusCode)
	}
	body := fmt.Sprintf(`{"id":%q,"state":"running"}`, uuid.New())
	if resp := r.do(t, "POST", "/v1/units/crew-01/events", body, nil); resp.StatusCode != 503 {
		t.Fatalf("events: %d", resp.StatusCode)
	}
}

func TestEmptyTokenRegistersNoRoutes(t *testing.T) {
	units, _ := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"}]`)
	mux := http.NewServeMux()
	Routes(mux, NewMemory(), units, "", nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, c := range []struct{ method, path, auth string }{
		{"GET", "/v1/units/crew-01/work?wait=1s", "Bearer "},
		{"GET", "/v1/units/crew-01/work?wait=1s", ""},
		{"POST", "/v1/units/crew-01/events", "Bearer "},
	} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, strings.NewReader("{}"))
		req.Header.Set("Authorization", c.auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s %s: status %d, want 404", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestPollWakesForRedeliveryDueMidWait(t *testing.T) {
	r := newRig(t, NewMemory())
	var skew atomic.Int64
	r.api.now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	it := must(t, r.store, "crew-01", KindTask, tpl)
	if resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil); resp.StatusCode != 200 {
		t.Fatalf("first delivery: %d", resp.StatusCode)
	}
	// Half a second of real time from the 30 s mark; the poll's own wait is
	// far longer than that.
	skew.Store(int64(29*time.Second + 500*time.Millisecond))
	start := time.Now()
	resp := r.do(t, "GET", "/v1/units/crew-01/work?wait=20s", "", nil)
	body := readBody(t, resp)
	if !strings.Contains(body, it.ID.String()) {
		t.Fatalf("body %s", body)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("redelivery took %v, should follow the due time", d)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

func TestDeliveryNotifiesOperators(t *testing.T) {
	r := newRig(t, NewMemory())
	must(t, r.store, "crew-01", KindTask, tpl)
	readBody(t, r.do(t, "GET", "/v1/units/crew-01/work?wait=1s", "", nil))
	if r.notified.Load() != "crew-01" {
		t.Fatal("a delivery did not notify, so live consoles keep showing the item as queued")
	}
	if got, _ := r.store.Recent(ctx, "crew-01", 1); got[0].State != Delivered {
		t.Fatalf("state %s, want delivered", got[0].State)
	}
}

func TestEventSeenReachesTheHook(t *testing.T) {
	r := newRig(t, NewMemory())
	var got []Seen
	var unit string
	r.api.OnSeen = func(u string, s Seen) { unit = u; got = append(got, s) }
	it := must(t, r.store, "crew-01", KindTask, tpl)
	resp := r.do(t, "POST", "/v1/units/crew-01/events", fmt.Sprintf(
		`{"id":%q,"state":"running","progress":10,"seen":{"chest":{"x":3,"y":64,"z":-7},"items":{"oak_log":12,"cobblestone":0},"at":"2026-10-20T10:00:00Z"}}`, it.ID), nil)
	if resp.StatusCode != 204 || len(got) != 1 || unit != "crew-01" {
		t.Fatalf("status %d, seen %+v", resp.StatusCode, got)
	}
	s := got[0]
	if s.Chest.X != 3 || s.Chest.Y != 64 || s.Chest.Z != -7 || s.Items["oak_log"] != 12 || !s.At.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", s)
	}
}

func TestMalformedSeenIsIgnoredButStateApplies(t *testing.T) {
	for name, seen := range map[string]string{
		"wrong type":     `"chest"`,
		"bad item name":  `{"chest":{"x":1,"y":2,"z":3},"items":{"Oak Log":1},"at":"2026-10-20T10:00:00Z"}`,
		"negative count": `{"chest":{"x":1,"y":2,"z":3},"items":{"oak_log":-1},"at":"2026-10-20T10:00:00Z"}`,
		"no time":        `{"chest":{"x":1,"y":2,"z":3},"items":{"oak_log":1}}`,
		"unknown field":  `{"chest":{"x":1,"y":2,"z":3},"items":{},"at":"2026-10-20T10:00:00Z","x":1}`,
		"too many kinds": `{"chest":{"x":1,"y":2,"z":3},"items":{` + manyItems(100) + `},"at":"2026-10-20T10:00:00Z"}`,
		"null":           `null`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, NewMemory())
			called := false
			r.api.OnSeen = func(string, Seen) { called = true }
			it := must(t, r.store, "crew-01", KindTask, tpl)
			resp := r.do(t, "POST", "/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"done","progress":100,"seen":%s}`, it.ID, seen), nil)
			got, _ := r.store.Recent(ctx, "crew-01", 1)
			if resp.StatusCode != 204 || got[0].State != Done || called {
				t.Fatalf("status %d state %s hook called %v", resp.StatusCode, got[0].State, called)
			}
		})
	}
}

func manyItems(n int) string {
	var parts []string
	for i := range n {
		parts = append(parts, fmt.Sprintf(`"item_%c%c":1`, 'a'+rune(i/26), 'a'+rune(i%26)))
	}
	return strings.Join(parts, ",")
}

func TestSeenFromARefusedEventIsDropped(t *testing.T) {
	r := newRig(t, NewMemory())
	called := false
	r.api.OnSeen = func(string, Seen) { called = true }
	other := must(t, r.store, "crew-02", KindTask, tpl)
	resp := r.do(t, "POST", "/v1/units/crew-01/events", fmt.Sprintf(
		`{"id":%q,"state":"done","seen":{"chest":{"x":1,"y":2,"z":3},"items":{},"at":"2026-10-20T10:00:00Z"}}`, other.ID), nil)
	if resp.StatusCode != 404 || called {
		t.Fatalf("status %d, hook called %v", resp.StatusCode, called)
	}
}

func TestEventBodyCapIs4KiB(t *testing.T) {
	r := newRig(t, NewMemory())
	it := must(t, r.store, "crew-01", KindTask, tpl)
	pad := func(n int) string {
		return fmt.Sprintf(`{"id":%q,"state":"running","reason":%q}`, it.ID, strings.Repeat("a", n))
	}
	if resp := r.do(t, "POST", "/v1/units/crew-01/events", pad(3000), nil); resp.StatusCode != 204 {
		t.Fatalf("3 KiB event: %d", resp.StatusCode)
	}
	if resp := r.do(t, "POST", "/v1/units/crew-01/events", pad(4097), nil); resp.StatusCode != 413 {
		t.Fatalf("over 4 KiB: %d", resp.StatusCode)
	}
}

func TestOnTerminalFiresForJobItemsOnly(t *testing.T) {
	r := newRig(t, NewMemory())
	var fired []Item
	r.api.OnTerminal = func(it Item) { fired = append(fired, it) }
	job, step := uuid.New(), uuid.New()
	stepItem, err := r.store.EnqueueStep(ctx, "crew-01", tpl, "op", job, step)
	if err != nil {
		t.Fatal(err)
	}
	hand := must(t, r.store, "crew-01", KindTask, tpl)
	post := func(id uuid.UUID, state string) {
		r.do(t, "POST", "/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":%q}`, id, state), nil)
	}
	post(stepItem.ID, "running")
	post(hand.ID, "done")
	if len(fired) != 0 {
		t.Fatalf("fired early or for hand-queued work: %+v", fired)
	}
	post(stepItem.ID, "failed")
	if len(fired) != 1 || fired[0].ID != stepItem.ID || fired[0].State != Failed || *fired[0].JobID != job || *fired[0].StepID != step {
		t.Fatalf("%+v", fired)
	}
}
