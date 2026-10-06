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
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

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

	transport := &transport{connectBy: connectBy}
	conn, err := dialer.DialContextNetwork(ctx, transport, address)
	if err != nil {
		if transport.abandon() {
			return nil, fmt.Errorf("connected, awaiting spawn: %w", err)
		}
		return nil, err
	}
	return conn, nil
}

// transport is RakNet with the connect budget applied, which keeps hold of
// the connection it dialled: the library returns no connection from a failed
// dial, so this is the only handle on one it abandoned.
type transport struct {
	connectBy time.Time

	mu   sync.Mutex
	conn net.Conn
}

// PingContext implements minecraft.Network.
func (t *transport) PingContext(ctx context.Context, address string) ([]byte, error) {
	ctx, cancel := context.WithDeadline(ctx, t.connectBy)
	defer cancel()
	return raknet.Dialer{}.PingContext(ctx, address)
}

// DialContext implements minecraft.Network.
func (t *transport) DialContext(ctx context.Context, address string) (net.Conn, error) {
	ctx, cancel := context.WithDeadline(ctx, t.connectBy)
	defer cancel()
	conn, err := raknet.Dialer{}.DialContext(ctx, address)
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
// there was one. Closing is what sends the disconnect the server frees the
// player on.
func (t *transport) abandon() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn == nil {
		return false
	}
	_ = t.conn.Close()
	return true
}
