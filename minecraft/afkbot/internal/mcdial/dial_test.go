package mcdial

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

// slowWorld is a Bedrock server whose world takes spawnDelay to spawn a
// player, and which -- like the real one -- refuses a login from a player it
// believes is still connected.
type slowWorld struct {
	listener   *minecraft.Listener
	spawnDelay time.Duration

	mu      sync.Mutex
	held    map[string]bool
	refused int
	// left receives a player's name when the server learns it has gone.
	left chan string
}

func startSlowWorld(t *testing.T, spawnDelay time.Duration) *slowWorld {
	t.Helper()
	listener, err := minecraft.ListenConfig{AuthenticationDisabled: true}.Listen("raknet", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	w := &slowWorld{
		listener:   listener,
		spawnDelay: spawnDelay,
		held:       map[string]bool{},
		left:       make(chan string, 16),
	}
	t.Cleanup(func() { _ = listener.Close() })
	go w.accept()
	return w
}

func (w *slowWorld) address() string { return w.listener.Addr().String() }

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
