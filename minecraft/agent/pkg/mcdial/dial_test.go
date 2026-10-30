package mcdial

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/df-mc/go-nethernet"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

// slowWorld is a Bedrock server whose world takes spawnDelay to spawn a
// player, and which -- like the real one -- refuses a login from a player it
// believes is still connected.
type slowWorld struct {
	listener *minecraft.Listener
	// dialAddr overrides the listener's own address where clients reach the
	// server some other way, as they do a NetherNet one.
	dialAddr   string
	spawnDelay time.Duration

	mu      sync.Mutex
	held    map[string]bool
	refused int
	// left receives a player's name when the server learns it has gone.
	left chan string
}

func startSlowWorld(t *testing.T, spawnDelay time.Duration) *slowWorld {
	t.Helper()
	return startWorldOn(t, spawnDelay, nil, "127.0.0.1:0", "")
}

// startNetherNetWorld is the same server behind a NetherNet signaling port.
func startNetherNetWorld(t *testing.T, spawnDelay time.Duration) *slowWorld {
	t.Helper()
	// The signaling server binds its own socket, so a port is reserved first
	// and handed over: the URL clients dial has to be known up front.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	hostport := l.Addr().String()
	_ = l.Close()
	return startWorldOn(t, spawnDelay, minecraft.NetherNet{ListenConfig: nethernet.ListenConfig{AllowAnonymous: true}}, hostport, "http://"+hostport)
}

func startWorldOn(t *testing.T, spawnDelay time.Duration, network minecraft.Network, listenAddr, dialAddr string) *slowWorld {
	t.Helper()
	cfg := minecraft.ListenConfig{AuthenticationDisabled: true}
	var (
		listener *minecraft.Listener
		err      error
	)
	if network == nil {
		listener, err = cfg.Listen("raknet", listenAddr)
	} else {
		listener, err = cfg.ListenNetwork(network, listenAddr)
	}
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	w := &slowWorld{
		dialAddr:   dialAddr,
		listener:   listener,
		spawnDelay: spawnDelay,
		held:       map[string]bool{},
		left:       make(chan string, 16),
	}
	t.Cleanup(func() { _ = listener.Close() })
	go w.accept()
	return w
}

func (w *slowWorld) address() string {
	if w.dialAddr != "" {
		return w.dialAddr
	}
	return w.listener.Addr().String()
}

func (w *slowWorld) refusals() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.refused
}

func (w *slowWorld) accept() {
	for {
		c, err := w.listener.Accept()
		if err != nil {
			return
		}
		conn := c.(*minecraft.Conn)
		name := conn.IdentityData().DisplayName

		w.mu.Lock()
		if w.held[name] {
			w.refused++
			w.mu.Unlock()
			_ = w.listener.Disconnect(conn, "")
			continue
		}
		w.held[name] = true
		w.mu.Unlock()

		go w.play(conn, name)
	}
}

// play holds the player's session for as long as the server has no reason to
// think the client is gone, which is the whole of what makes a ghost.
func (w *slowWorld) play(conn *minecraft.Conn, name string) {
	select {
	case <-time.After(w.spawnDelay):
		_ = conn.StartGameContext(conn.Context(), minecraft.GameData{})
	case <-conn.Context().Done():
	}
	<-conn.Context().Done()

	w.mu.Lock()
	delete(w.held, name)
	w.mu.Unlock()
	w.left <- name
}

func dialerFor(name string) minecraft.Dialer {
	return minecraft.Dialer{IdentityData: login.IdentityData{DisplayName: name}}
}

// The failure this package exists for: the world spawns the player after the
// client has stopped waiting. The server must learn the client left, and the
// next login must be let in.
func TestDialThatGivesUpBeforeSpawnLeavesNoSession(t *testing.T) {
	world := startSlowWorld(t, 1500*time.Millisecond)

	// The transport comes up well inside connect; the spawn does not arrive
	// inside spawn.
	_, err := Dial(context.Background(), dialerFor("Agent"), world.address(), 400*time.Millisecond, 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial error = %v, want a deadline", err)
	}
	if !strings.Contains(err.Error(), "awaiting spawn") {
		t.Errorf("Dial error = %q, want it to say the server had been reached", err)
	}

	select {
	case name := <-world.left:
		if name != "Agent" {
			t.Fatalf("server saw %q leave, want Agent", name)
		}
	case <-time.After(10 * time.Second):
		// Not fatal: the redial below shows what holding it costs.
		t.Error("server still holds the abandoned session after 10s: the client never told it it left")
	}

	conn, err := Dial(context.Background(), dialerFor("Agent"), world.address(), 5*time.Second, 20*time.Second)
	if err != nil {
		t.Fatalf("redial after giving up: %v (server refused %d logins)", err, world.refusals())
	}
	_ = conn.Close()
	if n := world.refusals(); n != 0 {
		t.Errorf("server refused %d logins, want 0", n)
	}
}

// A spawn slower than the connect budget is not a failure: the server
// answered, and the spawn budget is what the wait is measured against.
func TestDialWaitsPastConnectBudgetForSpawn(t *testing.T) {
	world := startSlowWorld(t, 1500*time.Millisecond)

	started := time.Now()
	conn, err := Dial(context.Background(), dialerFor("Agent"), world.address(), time.Second, 20*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if waited := time.Since(started); waited < time.Second {
		t.Errorf("spawned after %v, want it to have outlasted the 1s connect budget", waited)
	}
	if err := conn.DoSpawnContext(context.Background()); err != nil {
		t.Errorf("DoSpawnContext on a dialled connection: %v", err)
	}
}

// A server that answers nothing is given up on at the connect budget, not
// at the far longer one a spawn is allowed.
func TestDialGivesUpOnSilentServerAtConnectBudget(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = silent.Close() }()

	started := time.Now()
	_, err = Dial(context.Background(), dialerFor("Agent"), silent.LocalAddr().String(), 500*time.Millisecond, time.Minute)
	waited := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial error = %v, want a deadline", err)
	}
	if strings.Contains(err.Error(), "awaiting spawn") {
		t.Errorf("Dial error = %q, want no claim that the server was reached", err)
	}
	if waited < 400*time.Millisecond || waited > 5*time.Second {
		t.Errorf("gave up after %v, want about the 500ms connect budget", waited)
	}
}

// Cancelling the caller's context ends a dial in either phase.
func TestDialStopsWhenCallerCancels(t *testing.T) {
	world := startSlowWorld(t, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	_, err := Dial(ctx, dialerFor("Agent"), world.address(), 5*time.Second, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial error = %v, want cancellation", err)
	}
	select {
	case <-world.left:
	case <-time.After(10 * time.Second):
		t.Fatal("server still holds the session of a cancelled dial after 10s")
	}
}

// The failure behind crew rejoining as "name(2)": a process that exits right
// after leaving must already have told the server, because nothing is left
// to send the goodbye afterwards.
func TestLeaveTellsServerBeforeReturning(t *testing.T) {
	world := startSlowWorld(t, 0)

	tr := &transport{connectBy: time.Now().Add(5 * time.Second)}
	conn, leave, err := dialOn(context.Background(), tr, dialerFor("Agent"), world.address(), 20*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := conn.DoSpawnContext(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	leave(5 * time.Second)

	// Close alone only marks the transport closing; its goodbye goes out on a
	// later tick, which a process that is exiting never gets to run.
	if tr.conn.(interface{ Context() context.Context }).Context().Err() == nil {
		t.Error("transport still open when leave returned: the goodbye has not been sent")
	}

	select {
	case name := <-world.left:
		if name != "Agent" {
			t.Fatalf("server saw %q leave, want Agent", name)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("server had not learned the player left by the time leave returned")
	}

	again, leaveAgain, err := DialWithLeave(context.Background(), dialerFor("Agent"), world.address(), 5*time.Second, 20*time.Second)
	if err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	defer leaveAgain(time.Second)
	_ = again
	if n := world.refusals(); n != 0 {
		t.Errorf("server refused %d logins, want 0", n)
	}
	leave(time.Second) // idempotent
}

// The same guarantees over NetherNet, whose connection is torn down by Close
// itself rather than by a later tick -- so the server must learn of a gave-up
// dial just the same.
func TestNetherNetDialThatGivesUpBeforeSpawnLeavesNoSession(t *testing.T) {
	world := startNetherNetWorld(t, 8*time.Second)

	_, err := Dial(context.Background(), dialerFor("Agent"), world.address(), 4*time.Second, time.Second)
	if err == nil {
		t.Fatal("Dial succeeded before the world could spawn the player")
	}
	if !strings.Contains(err.Error(), "awaiting spawn") {
		t.Fatalf("Dial error = %q, want it to say the server had been reached", err)
	}

	select {
	case name := <-world.left:
		if name != "Agent" {
			t.Fatalf("server saw %q leave, want Agent", name)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server still holds the abandoned session: the client never told it it left")
	}

	conn, err := Dial(context.Background(), dialerFor("Agent"), world.address(), 20*time.Second, 20*time.Second)
	if err != nil {
		t.Fatalf("redial after giving up: %v (server refused %d logins)", err, world.refusals())
	}
	_ = conn.Close()
}

func TestNetherNetLeaveTellsServerBeforeReturning(t *testing.T) {
	world := startNetherNetWorld(t, 0)

	conn, leave, err := DialWithLeave(context.Background(), dialerFor("Agent"), world.address(), 20*time.Second, 20*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := conn.DoSpawnContext(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	leave(5 * time.Second)

	select {
	case name := <-world.left:
		if name != "Agent" {
			t.Fatalf("server saw %q leave, want Agent", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server had not learned the player left")
	}
	leave(time.Second) // idempotent
}

// A signaling port with nothing behind it is a down server: the dial ends at
// the connect budget and claims no session.
func TestNetherNetDialGivesUpOnDeadSignalingAtConnectBudget(t *testing.T) {
	started := time.Now()
	_, err := Dial(context.Background(), dialerFor("Agent"), "http://127.0.0.1:1", time.Second, time.Minute)
	if err == nil {
		t.Fatal("Dial succeeded against nothing")
	}
	if strings.Contains(err.Error(), "awaiting spawn") {
		t.Errorf("Dial error = %q, want no claim that the server was reached", err)
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("gave up after %v, want about the 1s connect budget", waited)
	}
}
