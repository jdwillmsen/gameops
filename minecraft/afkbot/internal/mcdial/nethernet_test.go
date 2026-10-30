package mcdial

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNetherNetDialSpawnsAndOutlastsConnectBudget(t *testing.T) {
	world := startSlowNetherNetWorld(t, 1500*time.Millisecond)

	started := time.Now()
	conn, err := Dial(context.Background(), dialerFor("Bot"), world.address(), 2*time.Second, 30*time.Second)
	if err != nil {
		t.Fatalf("Dial over NetherNet: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if waited := time.Since(started); waited < time.Second {
		t.Errorf("spawned after %v, want it to have waited out the 1.5s world load", waited)
	}
	if err := conn.DoSpawnContext(context.Background()); err != nil {
		t.Errorf("DoSpawnContext: %v", err)
	}
}

// The NetherNet half of the guarantee this package exists for.
func TestNetherNetDialThatGivesUpBeforeSpawnLeavesNoSession(t *testing.T) {
	world := startSlowNetherNetWorld(t, 4*time.Second)

	_, err := Dial(context.Background(), dialerFor("Bot"), world.address(), 3*time.Second, 300*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial error = %v, want a deadline", err)
	}
	if !strings.Contains(err.Error(), "awaiting spawn") {
		t.Errorf("Dial error = %q, want it to say the server had been reached", err)
	}

	select {
	case name := <-world.left:
		if name != "Bot" {
			t.Fatalf("server saw %q leave, want Bot", name)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server still holds the abandoned NetherNet session after 15s")
	}

	conn, err := Dial(context.Background(), dialerFor("Bot"), world.address(), 5*time.Second, 20*time.Second)
	if err != nil {
		t.Fatalf("redial after giving up: %v (server refused %d logins)", err, world.refusals())
	}
	_ = conn.Close()
}

func TestNetherNetDialToNothingFailsAtConnectBudget(t *testing.T) {
	started := time.Now()
	_, err := Dial(context.Background(), dialerFor("Bot"), "http://127.0.0.1:1", 500*time.Millisecond, time.Minute)
	if err == nil {
		t.Fatal("Dial to a closed signaling port succeeded")
	}
	if strings.Contains(err.Error(), "awaiting spawn") {
		t.Errorf("Dial error = %q, want no claim that the server was reached", err)
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("gave up after %v, want about the 500ms connect budget", waited)
	}
}

// gophertunnel picks the identity-bound dial by interface. If the NetherNet
// wrapper lost it the bot would dial anonymously and be refused with
// IdentityNotAllowed; if RakNet gained it the bot would try the wrong path.
func TestOnlyNetherNetTransportDialsWithIdentity(t *testing.T) {
	nn, _ := newTransport("http://127.0.0.1:31134", time.Now(), nil)
	if _, ok := nn.(identityDialer); !ok {
		t.Error("NetherNet transport must implement the identity-bound dial")
	}
	rn, _ := newTransport("127.0.0.1:19132", time.Now(), nil)
	if _, ok := rn.(identityDialer); ok {
		t.Error("RakNet transport must not implement the identity-bound dial")
	}
}
