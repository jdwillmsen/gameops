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
		"too large":     {"/v1/units/crew-01/events", fmt.Sprintf(`{"id":%q,"state":"failed","reason":%q}`, it.ID, strings.Repeat("a", 2000)), 413},
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
