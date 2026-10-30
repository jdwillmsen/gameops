package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

type fakeUnit struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newFakeUnit(t *testing.T, h http.Handler) *fakeUnit {
	t.Helper()
	u := &fakeUnit{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.reqs = append(u.reqs, r)
		u.mu.Unlock()
		if h != nil {
			h.ServeHTTP(w, r)
			return
		}
		fmt.Fprint(w, "unit body")
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *fakeUnit) seen() []*http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]*http.Request(nil), u.reqs...)
}

func rosterFor(t *testing.T, unitURL string) roster.Roster {
	t.Helper()
	r, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Agate","url":%q}]`, unitURL))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newProxy(t *testing.T, unitURL string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(Handler(rosterFor(t, unitURL), logging.New("error")))
	t.Cleanup(s.Close)
	return s
}

func TestProxyAllowList(t *testing.T) {
	files := []string{"ws", "mesher.js", "sky.js", "entities.js", "entities.png", "entities.json",
		"atlas.png", "atlas.json", "items.png", "items.json", "stats"}
	paths := map[string]string{
		"/units/crew-01/camera":   "/camera",
		"/units/crew-01/v1/state": "/v1/state",
	}
	for _, f := range files {
		paths["/units/crew-01/v1/camera/"+f] = "/v1/camera/" + f
	}
	unit := newFakeUnit(t, nil)
	p := newProxy(t, unit.URL)
	for in, want := range paths {
		resp, err := http.Get(p.URL + in)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "unit body" {
			t.Errorf("%s: %d %q", in, resp.StatusCode, body)
		}
		reqs := unit.seen()
		if got := reqs[len(reqs)-1].URL.Path; got != want {
			t.Errorf("%s: upstream saw %s, want %s", in, got, want)
		}
	}
}

func TestProxyRefuses(t *testing.T) {
	cases := []struct{ method, path string }{
		{"GET", "/units/crew-01/v1/tasks"},
		{"GET", "/units/crew-01/metrics"},
		{"GET", "/units/crew-01/healthz"},
		{"GET", "/units/crew-01/v1/camera"},
		{"GET", "/units/crew-01/v1/camera/ws/x"},
		{"GET", "/units/crew-01/v1/statex"},
		{"GET", "/units/crew-01/v1/camera/../../v1/tasks"},
		{"GET", "/units/crew-01/v1/camera/%2e%2e/tasks"},
		{"GET", "/units/crew-99/camera"},
		{"POST", "/units/crew-01/camera"},
		{"GET", "/units//camera"},
		{"GET", "/units/crew-01/./camera"},
		{"GET", "/units/crew-01/v1/camera/ws/"},
	}
	unit := newFakeUnit(t, nil)
	h := Handler(rosterFor(t, unit.URL), logging.New("error"))
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d (location %q), want 404", c.method, c.path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if n := len(unit.seen()); n != 0 {
		t.Errorf("upstream saw %d requests, want 0", n)
	}
}

func TestProxyStripsCredentials(t *testing.T) {
	unit := newFakeUnit(t, nil)
	p := newProxy(t, unit.URL)
	req, _ := http.NewRequest("GET", p.URL+"/units/crew-01/camera?x=1", nil)
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got := unit.seen()[0]
	for _, h := range []string{"Cookie", "Authorization"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("%s reached the unit: %q", h, v)
		}
	}
	if v := got.Header.Get("X-Forwarded-For"); strings.Contains(v, "6.6.6.6") {
		t.Errorf("client X-Forwarded-For reached the unit: %q", v)
	}
}

func TestProxyUnitDown(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	p := newProxy(t, deadURL)
	start := time.Now()
	resp, err := http.Get(p.URL + "/units/crew-01/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "unit unavailable") {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %s", d)
	}
}

func TestProxyWebSocketSameOrigin(t *testing.T) {
	unit := newFakeUnit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		typ, msg, err := c.Read(ctx)
		if err != nil {
			return
		}
		_ = c.Write(ctx, typ, msg)
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	p := newProxy(t, unit.URL)
	pu, _ := url.Parse(p.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+pu.Host+"/units/crew-01/v1/camera/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://" + pu.Host}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := c.Read(ctx)
	if err != nil || string(msg) != "hello" {
		t.Fatalf("echo = %q, %v", msg, err)
	}
}

func TestProxyForwardsOnlyCameraRadiusAndWire(t *testing.T) {
	cases := []struct{ path, wantQuery string }{
		{"/units/crew-01/v1/camera/ws?r=2", "r=2"},
		{"/units/crew-01/v1/camera/ws?r=2&x=1", "r=2"},
		{"/units/crew-01/v1/camera/ws?x=1", ""},
		{"/units/crew-01/v1/camera/ws?r=abc", ""},
		{"/units/crew-01/v1/camera/ws?r=1234", ""},
		{"/units/crew-01/v1/camera/ws?r=2&wire=4", "r=2&wire=4"},
		{"/units/crew-01/v1/camera/ws?wire=4", "wire=4"},
		{"/units/crew-01/v1/camera/ws?r=abc&wire=4", "wire=4"},
		{"/units/crew-01/v1/camera/ws?wire=abc", ""},
		{"/units/crew-01/v1/camera/ws?wire=123", ""},
		{"/units/crew-01/v1/camera/ws?wire=4&x=1&y=2", "wire=4"},
		{"/units/crew-01/v1/camera/stats?wire=4", ""},
		{"/units/crew-01/v1/camera/stats?r=2", ""},
		{"/units/crew-01/v1/state?r=2&x=1", ""},
	}
	unit := newFakeUnit(t, nil)
	h := Handler(rosterFor(t, unit.URL), logging.New("error"))
	for _, c := range cases {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", c.path, nil))
		reqs := unit.seen()
		if got := reqs[len(reqs)-1].URL.RawQuery; got != c.wantQuery {
			t.Errorf("%s: upstream query %q, want %q", c.path, got, c.wantQuery)
		}
	}
}

// captureStdout swaps the process stdout, which logging.New binds at
// construction, so build the proxy inside fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = orig
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	return string(out)
}

func TestProxyLogsUpstreamFailureButNotClientCancel(t *testing.T) {
	var cancelled, failed string

	cancelled = captureStdout(t, func() {
		release := make(chan struct{})
		unit := newFakeUnit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
		s := httptest.NewServer(Handler(rosterFor(t, unit.URL), logging.New("info")))
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/units/crew-01/v1/state", nil)
		go func() {
			for len(unit.seen()) == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
		}()
		if _, err := http.DefaultClient.Do(req); err == nil {
			t.Error("cancelled request succeeded")
		}
		close(release)
		s.Close()
	})
	if strings.Contains(cancelled, "proxy_upstream_failed") {
		t.Errorf("client cancel was logged: %s", cancelled)
	}

	failed = captureStdout(t, func() {
		dead := httptest.NewServer(http.NotFoundHandler())
		deadURL := dead.URL
		dead.Close()
		s := httptest.NewServer(Handler(rosterFor(t, deadURL), logging.New("info")))
		resp, err := http.Get(s.URL + "/units/crew-01/v1/state")
		if err != nil {
			t.Error(err)
		} else {
			resp.Body.Close()
		}
		s.Close()
	})
	if !strings.Contains(failed, "proxy_upstream_failed") {
		t.Errorf("real upstream failure not logged: %q", failed)
	}
}

func TestProxyAddressesAUnitByName(t *testing.T) {
	unit := newFakeUnit(t, nil)
	p := newProxy(t, unit.URL)
	resp, err := http.Get(p.URL + "/units/Agate/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "unit body" {
		t.Fatalf("by name: %d %q", resp.StatusCode, body)
	}
	if seen := unit.seen(); len(seen) != 1 || seen[0].URL.Path != "/v1/state" {
		t.Fatalf("upstream saw %v, want one /v1/state", seen)
	}
}

func TestProxyNameRoutesStayOnTheAllowList(t *testing.T) {
	unit := newFakeUnit(t, nil)
	h := Handler(rosterFor(t, unit.URL), logging.New("error"))
	for _, c := range []struct{ method, path string }{
		{"GET", "/units/Agate/v1/tasks"},
		{"GET", "/units/Agate/metrics"},
		{"POST", "/units/Agate/v1/state"},
		{"GET", "/units/agate/v1/state"},
		{"GET", "/units/Agate/../crew-01/v1/tasks"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404", c.method, c.path, rec.Code)
		}
	}
	if n := len(unit.seen()); n != 0 {
		t.Errorf("upstream saw %d requests, want 0", n)
	}
}
