package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testConsole() *Console {
	return NewConsole("127.0.0.1:0", "pw", 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestConsoleIngestEvents_LineSplitAcrossFrames(t *testing.T) {
	c := testConsole()

	c.ingestEvents(wsMessage{Type: "stdout", Data: "[INFO] Player conn"})
	if got := c.Events.Since(0); len(got) != 0 {
		t.Fatalf("partial line produced %d events, want 0: %+v", len(got), got)
	}

	c.ingestEvents(wsMessage{Type: "stdout", Data: "ected: Steve, xuid: 111\n"})

	got := c.Events.Since(0)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Type != EventConnect || got[0].Player != "Steve" {
		t.Errorf("event = %+v, want connect for Steve", got[0])
	}
	if got[0].Raw != "[INFO] Player connected: Steve, xuid: 111" {
		t.Errorf("raw = %q, want the rejoined line", got[0].Raw)
	}
}

func TestConsoleIngestEvents_StdoutAndStderrResidualsAreIndependent(t *testing.T) {
	c := testConsole()

	c.ingestEvents(wsMessage{Type: "stdout", Data: "Player connected: Steve"})
	c.ingestEvents(wsMessage{Type: "stderr", Data: "Player disconnected: Alex"})
	c.ingestEvents(wsMessage{Type: "stdout", Data: ", xuid: 111\n"})
	c.ingestEvents(wsMessage{Type: "stderr", Data: ", xuid: 222\n"})

	got := c.Events.Since(0)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Type != EventConnect || got[0].Player != "Steve" {
		t.Errorf("event 0 = %+v", got[0])
	}
	if got[1].Type != EventDisconnect || got[1].Player != "Alex" {
		t.Errorf("event 1 = %+v", got[1])
	}
}

func TestConsoleIngestEvents_DropsOversizedPartialLine(t *testing.T) {
	c := testConsole()

	c.ingestEvents(wsMessage{Type: "stdout", Data: strings.Repeat("x", maxResidualLine+1)})
	if c.residual["stdout"] != "" {
		t.Fatalf("residual retained %d bytes, want it dropped", len(c.residual["stdout"]))
	}

	c.ingestEvents(wsMessage{Type: "stdout", Data: "Player connected: Steve, xuid: 111\n"})
	if got := c.Events.Since(0); len(got) != 1 {
		t.Fatalf("got %d events after drop, want 1: %+v", len(got), got)
	}
}

func TestConsoleSendCommand_NotConnected(t *testing.T) {
	c := testConsole()
	if _, err := c.SendCommand(t.Context(), "list"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func testServer(t *testing.T, c *Console) http.Handler {
	t.Helper()
	return newMux(&server{
		cfg:     Config{BridgeToken: "tok"},
		console: c,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestReadyzReflectsConsoleConnection(t *testing.T) {
	c := testConsole()
	mux := testServer(t, c)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("disconnected /readyz = %d, want 503", rec.Code)
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("connected /readyz = %d, want 200", rec.Code)
	}
}

func TestHealthzStaysGreenWhileConsoleIsDown(t *testing.T) {
	mux := testServer(t, testConsole())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", rec.Code)
	}
}

// startFakeConsole runs a minimal server that speaks just enough of
// mc-server-runner's websocket console protocol to drive Console against it.
// handler receives the connection and the password the client offered via
// the Sec-WebSocket-Protocol subprotocol list (there is no other channel
// mc-server-runner reads it from), and owns the whole connection lifecycle.
func startFakeConsole(t *testing.T, handler func(ctx context.Context, conn *websocket.Conn, password string)) (addr string, connections *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		handler(r.Context(), conn, subprotocolPassword(r))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &count
}

// subprotocolPassword extracts the password from the Sec-WebSocket-Protocol
// header the way mc-server-runner does: the client offers
// [authSubproto, password], and the real server reads the second element
// directly from the header rather than from whichever protocol got
// negotiated.
func subprotocolPassword(r *http.Request) string {
	parts := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeTestMsg(t *testing.T, ctx context.Context, conn *websocket.Conn, msg wsMessage) {
	t.Helper()
	if err := writeJSON(ctx, conn, msg); err != nil {
		t.Logf("fake console: write failed (client likely closed): %v", err)
	}
}

func readTestMsg(ctx context.Context, conn *websocket.Conn) (wsMessage, error) {
	var msg wsMessage
	_, data, err := conn.Read(ctx)
	if err != nil {
		return msg, err
	}
	err = json.Unmarshal(data, &msg)
	return msg, err
}

// waitConnected polls until c reports connected, or fails the test after a
// generous deadline. This is a state-condition poll, not a fixed sleep: it
// returns as soon as the condition is true and only fails if it genuinely
// never becomes true.
func waitConnected(t *testing.T, c *Console) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if c.Connected() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("console never reported connected")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConsoleConnectAndRead_AuthFailureNeverEstablishes(t *testing.T) {
	addr, attempts := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, password string) {
		writeTestMsg(t, ctx, conn, wsMessage{Type: "authFailure", Reason: "bad password"})
	})

	c := NewConsole(addr, "wrong-password", 2*time.Second, testLogger())

	err := c.connectAndRead(context.Background())
	if !errors.Is(err, ErrAuthFailure) {
		t.Fatalf("err = %v, want ErrAuthFailure", err)
	}
	if c.Connected() {
		t.Error("Connected() = true after an auth failure, want false — Run's backoff-reset check keys off this and would otherwise reset the delay on every rejected password, hammering the server every second forever")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("fake server saw %d connections, want exactly 1", got)
	}
}

func TestConsoleRun_AuthFailureDoesNotResetBackoff(t *testing.T) {
	var mu sync.Mutex
	var attempts []time.Time
	addr, _ := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, password string) {
		mu.Lock()
		attempts = append(attempts, time.Now())
		mu.Unlock()
		writeTestMsg(t, ctx, conn, wsMessage{Type: "authFailure", Reason: "bad password"})
	})

	c := NewConsole(addr, "wrong-password", 2*time.Second, testLogger())
	c.minReconnectDelay = 200 * time.Millisecond
	c.maxReconnectDelay = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(attempts)
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake server saw %d connection attempts, want 3", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	dials := append([]time.Time(nil), attempts[:3]...)
	mu.Unlock()

	// Every wait is delay+jitter with jitter < delay/2, so under the
	// reconnect-storm bug (connected promoted right after the handshake, so
	// Run resets the delay after every rejected password) both gaps land in
	// [min, 1.5*min). Growing backoff puts the second gap at 2*min plus
	// jitter, and a sleep can only ever overshoot its floor.
	gap1, gap2 := dials[1].Sub(dials[0]), dials[2].Sub(dials[1])
	if want := 2 * c.minReconnectDelay; gap2 < want {
		t.Errorf("reconnect gaps were %v then %v: backoff did not grow, want the second gap >= %v", gap1, gap2, want)
	}
	if c.Connected() {
		t.Error("Connected() = true after repeated rejected passwords, want false")
	}
}

func TestConsoleConnectAndRead_SilentPeerDoesNotHangForever(t *testing.T) {
	addr, _ := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, password string) {
		<-ctx.Done()
	})

	c := NewConsole(addr, "pw", 2*time.Second, testLogger())
	c.firstFrameTimeout = 200 * time.Millisecond

	// A peer that completes the handshake and then sends nothing never
	// promotes the connection, so without a first-frame deadline the read
	// loop parks forever: never connected, never redialed.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errc := make(chan error, 1)
	go func() { errc <- c.connectAndRead(ctx) }()

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("connectAndRead returned nil, want an error for a silent peer")
		}
	case <-ctx.Done():
		t.Fatalf("connectAndRead never returned for a peer that sent no frame within %v", c.firstFrameTimeout)
	}
	if c.Connected() {
		t.Error("Connected() = true for a peer that sent no frame, want false")
	}
}

func TestConsoleConnectAndRead_EstablishesOnFirstRealFrame(t *testing.T) {
	addr, _ := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, password string) {
		writeTestMsg(t, ctx, conn, wsMessage{Type: "logHistory"})
		<-ctx.Done()
	})

	c := NewConsole(addr, "pw", 2*time.Second, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.connectAndRead(ctx)
		close(done)
	}()

	waitConnected(t, c)
	cancel()
	<-done
}

func TestConsoleSendCommand_ConcurrentCallsDoNotInterleave(t *testing.T) {
	addr, _ := startFakeConsole(t, func(ctx context.Context, conn *websocket.Conn, password string) {
		writeTestMsg(t, ctx, conn, wsMessage{Type: "logHistory"})
		for {
			msg, err := readTestMsg(ctx, conn)
			if err != nil {
				return
			}
			if msg.Type != "stdin" {
				continue
			}
			cmd := strings.TrimSuffix(msg.Data, "\n")
			// A small delay widens the window in which a missing lock would
			// let two concurrent SendCommand calls collect each other's
			// output.
			time.Sleep(20 * time.Millisecond)
			writeTestMsg(t, ctx, conn, wsMessage{Type: "stdout", Data: "echo:" + cmd + "\n"})
		}
	})

	c := NewConsole(addr, "pw", 2*time.Second, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitConnected(t, c)

	cmds := []string{"list", "time query day"}
	results := make([]string, len(cmds))
	var wg sync.WaitGroup
	for i, cmd := range cmds {
		wg.Add(1)
		go func(i int, cmd string) {
			defer wg.Done()
			out, err := c.SendCommand(context.Background(), cmd)
			if err != nil {
				t.Errorf("SendCommand(%q): %v", cmd, err)
			}
			results[i] = out
		}(i, cmd)
	}
	wg.Wait()

	for i, cmd := range cmds {
		want := "echo:" + cmd
		if strings.Count(results[i], "echo:") != 1 || !strings.Contains(results[i], want) {
			t.Errorf("SendCommand(%q) output = %q, want exactly %q with nothing interleaved from the other concurrent command", cmd, results[i], want)
		}
	}
}

func TestCommandRejectsOversizedBody(t *testing.T) {
	mux := testServer(t, testConsole())

	body := `{"command":"say ` + strings.Repeat("a", maxCommandBodyBytes) + `"}`
	req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body = %d, want 400", rec.Code)
	}
}
