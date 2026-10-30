// Copied from github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcdial.
//
// Copied rather than imported: importing would make the agent's dependency
// floors this bot's, and put every agent release up for review here, to share
// code upstream has stopped changing. docs/decisions.md has the full reasoning.
//
// Fix bugs upstream first, then port here.

// Package mcdial logs a headless client in to a Bedrock server without
// leaving a session behind when it gives up.
//
// The client library's dial covers everything from the first packet to the
// moment the world spawns the player, and it abandons the connection rather
// than closing it when its context ends: the transport goes on acknowledging
// the server, the login it started completes unattended, and the server holds
// a player nobody is driving. Until the server times that player out it turns
// away every further login from the same account.
package mcdial

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/df-mc/go-nethernet/endpoint"
	"github.com/jdwillmsen/gameops/minecraft/afkbot/internal/mcaddr"
	"github.com/sandertv/go-raknet"
	"github.com/sandertv/gophertunnel/minecraft"
)

// Dial connects to address and returns once the world has spawned the player.
//
// The two budgets are separate because they answer different questions.
// connect bounds everything up to an established transport, and is what
// detects a server that is down. spawn bounds the wait that follows, in which
// the server is demonstrably up and the only thing outstanding is its world:
// a freshly started server loads the world for its first player, and that
// can outlast any deadline short enough to be a useful answer to "is it up".
//
// A dial that fails after the transport came up closes it, so the server is
// told the client has left.
func Dial(ctx context.Context, dialer minecraft.Dialer, address string, connect, spawn time.Duration) (*minecraft.Conn, error) {
	connectBy := time.Now().Add(connect)
	ctx, cancel := context.WithDeadline(ctx, connectBy.Add(spawn))
	defer cancel()

	network, t := newTransport(address, connectBy, dialer.ErrorLog)
	conn, err := dialer.DialContextNetwork(ctx, network, address)
	if err != nil {
		if t.abandon() {
			return nil, fmt.Errorf("connected, awaiting spawn: %w", err)
		}
		return nil, err
	}
	return conn, nil
}

// transport is the network under a dial -- RakNet for a host:port address,
// NetherNet for an http(s) signaling URL -- with the connect budget applied.
// It keeps hold of the connection it dialled: the library returns no
// connection from a failed dial, so this is the only handle on one it
// abandoned.
type transport struct {
	base      minecraft.Network
	connectBy time.Time

	mu   sync.Mutex
	conn net.Conn
}

// netherNetTransport adds the identity-bound dial. gophertunnel selects it
// by interface when it holds a Minecraft multiplayer token, and a wrapper
// without it would silently dial anonymously and be refused with
// IdentityNotAllowed. RakNet has no such method, so it must not be on the
// shared type.
type netherNetTransport struct {
	*transport
	identity identityDialer
}

type identityDialer interface {
	DialContextIdentity(ctx context.Context, address, token string, key *ecdsa.PrivateKey) (net.Conn, error)
}

func newTransport(address string, connectBy time.Time, log *slog.Logger) (minecraft.Network, *transport) {
	if !mcaddr.IsNetherNet(address) {
		t := &transport{base: raknetNetwork{}, connectBy: connectBy}
		return t, t
	}
	nn := minecraft.NetherNet{Signaling: endpoint.NewClient(), Log: log}
	t := &transport{base: nn, connectBy: connectBy}
	return netherNetTransport{transport: t, identity: nn}, t
}

// raknetNetwork is RakNet without the library's registration, which is not
// importable by name here.
type raknetNetwork struct{}

func (raknetNetwork) PingContext(ctx context.Context, address string) ([]byte, error) {
	return raknet.Dialer{}.PingContext(ctx, address)
}

func (raknetNetwork) DialContext(ctx context.Context, address string) (net.Conn, error) {
	return raknet.Dialer{}.DialContext(ctx, address)
}

func (raknetNetwork) Listen(string) (minecraft.NetworkListener, error) {
	return nil, errors.New("mcdial: listening is not supported")
}

// PingContext implements minecraft.Network.
func (t *transport) PingContext(ctx context.Context, address string) ([]byte, error) {
	ctx, cancel := context.WithDeadline(ctx, t.connectBy)
	defer cancel()
	return t.base.PingContext(ctx, address)
}

// DialContext implements minecraft.Network.
func (t *transport) DialContext(ctx context.Context, address string) (net.Conn, error) {
	ctx, cancel := context.WithDeadline(ctx, t.connectBy)
	defer cancel()
	conn, err := t.base.DialContext(ctx, address)
	return t.keep(conn, err)
}

// DialContextIdentity dials NetherNet as the signed-in player.
func (n netherNetTransport) DialContextIdentity(ctx context.Context, address, token string, key *ecdsa.PrivateKey) (net.Conn, error) {
	ctx, cancel := context.WithDeadline(ctx, n.connectBy)
	defer cancel()
	conn, err := n.identity.DialContextIdentity(ctx, address, token, key)
	return n.keep(conn, err)
}

func (t *transport) keep(conn net.Conn, err error) (net.Conn, error) {
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()
	return conn, nil
}

// Listen implements minecraft.Network. A client never listens.
func (t *transport) Listen(string) (minecraft.NetworkListener, error) {
	return nil, errors.New("mcdial: listening is not supported")
}

// abandon closes the connection a failed dial left open, reporting whether
// there was one. For RakNet, closing sends the disconnect the server frees
// the player on. For NetherNet it tears down the data channels and the SCTP,
// DTLS and ICE transports, so the server sees the peer connection end rather
// than waiting for it to time out.
func (t *transport) abandon() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn == nil {
		return false
	}
	_ = t.conn.Close()
	return true
}
