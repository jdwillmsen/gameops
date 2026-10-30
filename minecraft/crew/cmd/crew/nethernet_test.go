package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/df-mc/go-nethernet"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

// netherNetWorld is a NetherNet server on loopback that refuses a login from a
// name it still believes is connected, as the real one does. It lets anonymous
// clients in: the real server's Xbox check cannot run without Microsoft, so
// these tests cover the transport and the session loop, not the sign-in.
type netherNetWorld struct {
	listener *minecraft.Listener
	url      string
	// firstSpawnDelay holds the first player's spawn; later ones are instant.
	firstSpawnDelay time.Duration

	mu      sync.Mutex
	held    map[string]bool
	logins  int
	refused int
	left    chan string
}

func startNetherNetWorld(t *testing.T, firstSpawnDelay time.Duration) *netherNetWorld {
	t.Helper()
	// The signaling server binds its own socket, so a port is reserved first
	// and handed over: the URL clients dial has to be known up front.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	hostport := l.Addr().String()
	_ = l.Close()

	listener, err := minecraft.ListenConfig{AuthenticationDisabled: true}.ListenNetwork(
		minecraft.NetherNet{ListenConfig: nethernet.ListenConfig{AllowAnonymous: true}}, hostport)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	w := &netherNetWorld{
		listener:        listener,
		url:             "http://" + hostport,
		firstSpawnDelay: firstSpawnDelay,
		held:            map[string]bool{},
		left:            make(chan string, 16),
	}
	t.Cleanup(func() { _ = listener.Close() })
	go w.accept()
	return w
}

func (w *netherNetWorld) accept() {
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
		w.logins++
		delay := time.Duration(0)
		if w.logins == 1 {
			delay = w.firstSpawnDelay
		}
		w.mu.Unlock()

		go w.play(conn, name, delay)
	}
}

func (w *netherNetWorld) play(conn *minecraft.Conn, name string, delay time.Duration) {
	select {
	case <-time.After(delay):
		_ = conn.StartGameContext(conn.Context(), minecraft.GameData{})
	case <-conn.Context().Done():
	}
	<-conn.Context().Done()

	w.mu.Lock()
	delete(w.held, name)
	w.mu.Unlock()
	w.left <- name
}

func (w *netherNetWorld) refusals() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.refused
}

// netherNetConfig builds the unit's config the way the process does, from the
// environment.
func netherNetConfig(t *testing.T, url string) config.Config {
	t.Helper()
	t.Setenv("MC_HOST", url)
	t.Setenv("MC_USERNAME", "crew-01")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestUnitSpawnsOverNetherNet(t *testing.T) {
	world := startNetherNetWorld(t, 0)
	cfg := netherNetConfig(t, world.url)
	if cfg.Address() != world.url {
		t.Fatalf("Address() = %q, want %q", cfg.Address(), world.url)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	auth := authn{identity: login.IdentityData{DisplayName: "crew-01"}}
	conn, leave, err := dialServer(ctx, cfg, auth, skin.Skin{}, cfg.Address(), logging.New(""))
	if err != nil {
		t.Fatalf("dialServer: %v", err)
	}
	if err := conn.DoSpawnContext(ctx); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	leave(5 * time.Second)
	select {
	case name := <-world.left:
		if name != "crew-01" {
			t.Fatalf("server saw %q leave, want crew-01", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server had not learned the unit left by the time leave returned")
	}
}

// A unit that gives up on a slow spawn and redials through the session loop
// must find its name free: the server has to learn of the abandoned login,
// or the retry is refused for as long as it keeps the ghost.
func TestUnitRedialsOverNetherNetAfterGivingUpOnASlowSpawn(t *testing.T) {
	world := startNetherNetWorld(t, 30*time.Second)
	cfg := netherNetConfig(t, world.url)
	auth := authn{identity: login.IdentityData{DisplayName: "crew-01"}}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var attempts []error
	connect := func(ctx context.Context) error {
		// Dialling returns once the world has spawned the unit, so the first
		// attempt, against a world that spawns far later, is abandoned with the
		// server already holding its login.
		attemptCtx := ctx
		if len(attempts) == 0 {
			var stop context.CancelFunc
			attemptCtx, stop = context.WithTimeout(ctx, 8*time.Second)
			defer stop()
		}
		_, leave, err := dialServer(attemptCtx, cfg, auth, skin.Skin{}, cfg.Address(), logging.New(""))
		if err == nil {
			leave(leaveTimeout)
		}
		attempts = append(attempts, err)
		if len(attempts) == 2 {
			cancel()
		}
		return err
	}
	runConnectLoop(ctx, loopOptions{minDelay: time.Millisecond, maxDelay: time.Millisecond, after: instant}, connect, func() {}, discardLog{})

	if len(attempts) != 2 {
		t.Fatalf("%d attempts, want 2", len(attempts))
	}
	if !errors.Is(attempts[0], context.DeadlineExceeded) || !strings.Contains(attempts[0].Error(), "awaiting spawn") {
		t.Errorf("first attempt = %v, want it to give up on the spawn after reaching the server", attempts[0])
	}
	if attempts[1] != nil {
		t.Errorf("redial = %v, want a spawn", attempts[1])
	}
	if n := world.refusals(); n != 0 {
		t.Errorf("server refused %d logins, want 0: the abandoned one was never released", n)
	}
}

// A signaling port with nothing behind it is a down server: the unit's dial
// fails inside its budget and the loop keeps retrying rather than exiting.
func TestUnitDialFailsCleanlyWhenNetherNetSignalingIsDown(t *testing.T) {
	cfg := netherNetConfig(t, "http://127.0.0.1:1")
	if got := connectBudget(cfg); got != netherNetConnectTimeout {
		t.Fatalf("connect budget = %v, want %v for NetherNet", got, netherNetConnectTimeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	auth := authn{identity: login.IdentityData{DisplayName: "crew-01"}}
	if _, _, err := dialServer(ctx, cfg, auth, skin.Skin{}, cfg.Address(), logging.New("")); err == nil {
		t.Fatal("dialServer succeeded against nothing")
	}
}

func TestConnectBudgetOfARakNetUnitIsUnchanged(t *testing.T) {
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew-01")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := connectBudget(cfg); got != connectTimeout {
		t.Errorf("connect budget = %v, want %v", got, connectTimeout)
	}
}

// The camera's atlas is keyed by the server version, so on NetherNet it must
// come from the signaling port's status.
func TestPingVersionReadsANetherNetServersStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/join" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"name":"x","protocol":2200,"version":"1.26.60","level":"x","players":0,"maxPlayers":1,"gameType":0}`))
	}))
	defer srv.Close()
	if got := pingVersion(context.Background(), srv.URL); got != "1.26.60" {
		t.Errorf("pingVersion = %q, want 1.26.60", got)
	}
	if got := pingVersion(context.Background(), "http://127.0.0.1:1"); got != "" {
		t.Errorf("pingVersion of a dead server = %q, want empty", got)
	}
}

// The default transport still reads the version out of the RakNet pong.
func TestPingVersionStillReadsARakNetPong(t *testing.T) {
	l, err := minecraft.ListenConfig{
		AuthenticationDisabled: true,
		StatusProvider:         minecraft.NewStatusProvider("crew test", "x"),
	}.Listen("raknet", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	// The ping is a single best-effort UDP exchange, and the listener may not
	// answer the very first one on a loaded machine, so ask until it does.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := pingVersion(context.Background(), l.Addr().String()); got != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("pingVersion of a RakNet server stayed empty for 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
