package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
)

var steve = auth.Identity{XUID: "2535412345678901", Gamertag: "Steve Builds"}

func withLive(t *testing.T) *Server {
	t.Helper()
	s := withLogin(t)
	s.Live = live.New(10*time.Second, 1000, nil)
	return s
}

func session(s *Server, id auth.Identity) *http.Cookie {
	rec := httptest.NewRecorder()
	s.Sessions.Issue(rec, id)
	return cookie(rec, "__Host-mcmap_session")
}

// serve runs the public handler on a real listener with timeouts far
// shorter than a stream lasts, the way the service's own are.
func serve(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.Config.ReadTimeout = 150 * time.Millisecond
	srv.Config.WriteTimeout = 250 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// stream is one open live stream as a browser would read it.
type stream struct {
	resp   *http.Response
	events chan string
	// comments counts the lines a browser would discard.
	comments chan string
	ended    chan struct{}
}

func open(t *testing.T, srv *httptest.Server, path string, c *http.Cookie) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	st := &stream{resp: resp, events: make(chan string, 64), comments: make(chan string, 64), ended: make(chan struct{})}
	go func() {
		defer close(st.ended)
		defer resp.Body.Close()
		lines := bufio.NewReader(resp.Body)
		for {
			line, err := lines.ReadString('\n')
			if err != nil {
				return
			}
			switch line = strings.TrimSuffix(line, "\n"); {
			case strings.HasPrefix(line, "data: "):
				st.events <- strings.TrimPrefix(line, "data: ")
			case strings.HasPrefix(line, ":"):
				st.comments <- line
			}
		}
	}()
	return st
}

type liveFrame struct {
	At        *time.Time `json:"at"`
	ServerNow time.Time  `json:"serverNow"`
	Players   []struct {
		ID   string   `json:"i"`
		Name string   `json:"n"`
		X    float64  `json:"x"`
		Z    float64  `json:"z"`
		Yaw  *float64 `json:"r"`
	} `json:"players"`
	Mobs []struct {
		ID   string `json:"i"`
		Type string `json:"t"`
	} `json:"mobs"`
	More  int  `json:"more"`
	Stale bool `json:"stale"`
}

func (st *stream) next(t *testing.T) liveFrame {
	t.Helper()
	select {
	case data := <-st.events:
		var f liveFrame
		if err := json.Unmarshal([]byte(data), &f); err != nil {
			t.Fatalf("%v: %s", err, data)
		}
		return f
	case <-st.ended:
		t.Fatal("the stream ended with no frame")
	case <-time.After(3 * time.Second):
		t.Fatal("no frame")
	}
	return liveFrame{}
}

func feed(s *Server, gen int, dimension string, players, mobs int) {
	now := time.Now()
	list := func(kind string, n int, extra string) live.RawRecord {
		var items []string
		for i := range n {
			items = append(items, fmt.Sprintf(`{"i":"%s%d","x":%d,"y":64,"z":%d%s}`, kind, i, gen, i, extra))
		}
		return live.RawRecord{At: now, Data: fmt.Sprintf(`{"gen":%d,"dim":%q,"kind":%q,"part":0,"parts":1,"more":0,"items":[%s]}`,
			gen, dimension, kind, strings.Join(items, ","))}
	}
	s.Live.Ingest(list("players", players, `,"n":"Steve Builds","r":90`), now)
	s.Live.Ingest(list("mobs", mobs, `,"t":"zombie"`), now)
	s.Live.Flush(now)
}

// Where every player is standing is behind the same gate as where every
// base is.
func TestLiveStreamRequiresSession(t *testing.T) {
	s := withLive(t)
	feed(s, 1, "overworld", 1, 1)
	srv := serve(t, s)

	forged := &http.Cookie{Name: "__Host-mcmap_session", Value: "e30.nope"}
	for name, c := range map[string]*http.Cookie{"no session": nil, "a forged session": forged} {
		st := open(t, srv, "/api/live?dimension=overworld", c)
		if st.resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("with %s = %d, want 401", name, st.resp.StatusCode)
		}
		select {
		case data := <-st.events:
			t.Errorf("with %s the refusal carries live data: %s", name, data)
		case <-st.ended:
		case <-time.After(3 * time.Second):
			t.Errorf("with %s the refusal was held open", name)
		}
	}

	st := open(t, srv, "/api/live?dimension=overworld", session(s, steve))
	if st.resp.StatusCode != http.StatusOK {
		t.Fatalf("with a session = %d", st.resp.StatusCode)
	}
	if f := st.next(t); len(f.Players) != 1 {
		t.Errorf("frame = %+v", f)
	}
}

// What anyone can ask before logging in must not grow because the live
// layer exists.
func TestConfigStillSaysNothingAboutTheWorld(t *testing.T) {
	s := withLive(t)
	feed(s, 1, "overworld", 1, 1)
	rec := do(s.Handler(), "GET", "/api/config", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"login\":true}\n" {
		t.Errorf("GET /api/config = %d %q", rec.Code, rec.Body.String())
	}
}

func TestLiveStreamSendsCurrentStateImmediately(t *testing.T) {
	s := withLive(t)
	feed(s, 1, "overworld", 1, 3)
	srv := serve(t, s)

	st := open(t, srv, "/api/live?dimension=overworld", session(s, steve))
	f := st.next(t)
	if f.Stale || len(f.Players) != 1 || len(f.Mobs) != 3 || f.At == nil || f.ServerNow.Before(*f.At) {
		t.Fatalf("first frame = %+v", f)
	}
	if p := f.Players[0]; p.Name != "Steve Builds" || p.Yaw == nil || *p.Yaw != 90 {
		t.Errorf("player = %+v", p)
	}
	// A dimension nothing has been heard from still answers at once, so the
	// page can say so.
	if f := open(t, srv, "/api/live?dimension=end", session(s, steve)).next(t); !f.Stale || len(f.Players) != 0 {
		t.Errorf("first frame of an empty dimension = %+v", f)
	}
}

func TestLiveStreamSendsFrames(t *testing.T) {
	s := withLive(t)
	st := open(t, serve(t, s), "/api/live?dimension=nether", session(s, steve))
	st.next(t)

	feed(s, 1, "overworld", 1, 9)
	feed(s, 2, "nether", 1, 2)
	if f := st.next(t); len(f.Mobs) != 2 {
		t.Fatalf("a nether stream got %+v", f)
	}
	feed(s, 3, "nether", 0, 5)
	if f := st.next(t); len(f.Mobs) != 5 || len(f.Players) != 0 {
		t.Errorf("second frame = %+v", f)
	}
}

// The server's own timeouts are for requests that end. A stream has to
// outlast them or it dies at their age, mid-frame.
func TestLiveStreamOutlivesTheServersTimeouts(t *testing.T) {
	s := withLive(t)
	st := open(t, serve(t, s), "/api/live?dimension=overworld", session(s, steve))
	st.next(t)
	for gen := 1; gen <= 6; gen++ {
		time.Sleep(100 * time.Millisecond)
		feed(s, gen, "overworld", 1, gen)
		if f := st.next(t); len(f.Mobs) != gen {
			t.Fatalf("frame %d = %+v", gen, f)
		}
	}
}

// The load balancer cuts a connection that is silent for 30 seconds.
func TestLiveStreamHeartbeats(t *testing.T) {
	s := withLive(t)
	s.LiveKeepalive = 40 * time.Millisecond
	st := open(t, serve(t, s), "/api/live?dimension=overworld", session(s, steve))
	st.next(t)
	for i := range 3 {
		select {
		case c := <-st.comments:
			if c != ": keepalive" {
				t.Errorf("comment = %q", c)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no keepalive %d on a quiet stream", i)
		}
	}
	select {
	case data := <-st.events:
		t.Errorf("a keepalive arrived as an event: %s", data)
	default:
	}
}

// A session is checked when a request arrives. A stream is one request, so
// it has to end by itself for the check to happen again.
func TestLiveStreamEndsAtSessionExpiry(t *testing.T) {
	s := withLive(t)
	// Expiry is kept in whole seconds, so this session has between one and
	// two seconds left.
	s.Sessions.TTL = 2 * time.Second
	c := session(s, steve)
	srv := serve(t, s)
	began := time.Now()
	st := open(t, srv, "/api/live?dimension=overworld", c)
	st.next(t)

	select {
	case <-st.ended:
	case <-time.After(4 * time.Second):
		t.Fatal("the stream outlived its session")
	}
	if lasted := time.Since(began); lasted < 500*time.Millisecond {
		t.Errorf("the stream ended after %s, long before its session", lasted)
	}
	// The browser reconnects, and that is where the expired session is met.
	if st := open(t, srv, "/api/live?dimension=overworld", c); st.resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("reconnecting with the expired session = %d, want 401", st.resp.StatusCode)
	}
}

func TestLiveStreamIsRefusedOnReconnectAfterRevocation(t *testing.T) {
	s := withLive(t)
	c := session(s, steve)
	srv := serve(t, s)
	if st := open(t, srv, "/api/live?dimension=overworld", c); st.resp.StatusCode != http.StatusOK {
		t.Fatalf("before the revocation = %d", st.resp.StatusCode)
	}
	if err := s.Sessions.Revoked.Revoke(steve.XUID, time.Now()); err != nil {
		t.Fatal(err)
	}
	st := open(t, srv, "/api/live?dimension=overworld", c)
	if st.resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after the revocation = %d, want 401", st.resp.StatusCode)
	}
}

func TestLiveStreamRejectsAnUnknownDimension(t *testing.T) {
	s := withLive(t)
	c := []*http.Cookie{session(s, steve)}
	for _, query := range []string{"", "?dimension=", "?dimension=aether", "?dimension=../overworld", "?dimension=OVERWORLD"} {
		if rec := do(s.Handler(), "GET", "/api/live"+query, "", c); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/live%s = %d, want 400", query, rec.Code)
		}
	}
}

func TestLiveStreamSetsNoBufferingHeader(t *testing.T) {
	s := withLive(t)
	st := open(t, serve(t, s), "/api/live?dimension=overworld", session(s, steve))
	h := st.resp.Header
	if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-store" || h.Get("X-Accel-Buffering") != "no" {
		t.Errorf("headers = %v", h)
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("the stream lost the security headers: %v", h)
	}
}

// Turned off, the live layer is not a quieter version of itself: there is
// no route to find.
func TestLiveDisabledRegistersNoRoute(t *testing.T) {
	s := withLogin(t)
	c := []*http.Cookie{session(s, steve)}
	if rec := do(s.Handler(), "GET", "/api/live?dimension=overworld", "", c); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/live with the layer off = %d, want 404", rec.Code)
	}
	if body := do(s.Handler(), "GET", "/api/map", "", c).Body.String(); !strings.Contains(body, `"live":false`) {
		t.Errorf("the map says nothing of the layer being off: %s", body)
	}
	s.Live = live.New(time.Second, 10, nil)
	if body := do(s.Handler(), "GET", "/api/map", "", c).Body.String(); !strings.Contains(body, `"live":true`) {
		t.Errorf("the map says nothing of the layer being on: %s", body)
	}
}

func TestLiveStreamIsRefusedWhenTheHubIsFull(t *testing.T) {
	s := withLive(t)
	for range live.MaxSubscribers {
		if _, ok := s.Live.Subscribe("overworld"); !ok {
			t.Fatal("refused before the bound")
		}
	}
	rec := do(s.Handler(), "GET", "/api/live?dimension=overworld", "", []*http.Cookie{session(s, steve)})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("past the bound = %d %v", rec.Code, rec.Header())
	}
}

func TestLiveStreamEndsWhenTheHubCloses(t *testing.T) {
	s := withLive(t)
	st := open(t, serve(t, s), "/api/live?dimension=overworld", session(s, steve))
	st.next(t)
	s.Live.Hub.Close()
	select {
	case <-st.ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream is still open after the hub closed")
	}
}

// With no login there is no session to expire, and the stream still works.
func TestLiveStreamWithoutALogin(t *testing.T) {
	s, _ := fixture(t)
	s.Live = live.New(10*time.Second, 1000, nil)
	feed(s, 1, "overworld", 0, 2)
	if f := open(t, serve(t, s), "/api/live?dimension=overworld", nil).next(t); len(f.Mobs) != 2 {
		t.Errorf("frame = %+v", f)
	}
}
