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
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
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
	conn, _, err := DialWithLeave(ctx, dialer, address, connect, spawn)
	return conn, err
}

// DialWithLeave is Dial that also returns the function to end the session
// with. Plain Close is not enough: the transport only marks itself closing and
// sends the server its goodbye from a background tick up to a second later, so
// a process that exits straight after Close never says it. The server then
// holds the player until its own timeout and renames the next login from the
// same account "name(2)".
//
// leave tells the server the player is going and returns once that has been
// sent, or after wait. It is safe to call more than once.
func DialWithLeave(ctx context.Context, dialer minecraft.Dialer, address string, connect, spawn time.Duration) (*minecraft.Conn, func(wait time.Duration), error) {
	return dialOn(ctx, &transport{connectBy: time.Now().Add(connect)}, dialer, address, spawn)
}

func dialOn(ctx context.Context, transport *transport, dialer minecraft.Dialer, address string, spawn time.Duration) (*minecraft.Conn, func(wait time.Duration), error) {
	ctx, cancel := context.WithDeadline(ctx, transport.connectBy.Add(spawn))
	defer cancel()

	conn, err := dialer.DialContextNetwork(ctx, transport, address)
	if err != nil {
		if transport.abandon() {
			return nil, nil, fmt.Errorf("connected, awaiting spawn: %w", err)
		}
		return nil, nil, err
	}
	var once sync.Once
	return conn, func(wait time.Duration) { once.Do(func() { transport.leave(conn, wait) }) }, nil
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

// leave sends a Disconnect packet, which the server acts on at once, then
// closes and waits for the transport to finish its own goodbye as a fallback
// for a server that ignores the packet.
func (t *transport) leave(conn *minecraft.Conn, wait time.Duration) {
	_ = conn.WritePacket(&packet.Disconnect{HideDisconnectionScreen: true})
	_ = conn.Close()
	_ = conn.Close()
	_ = conn.Close()

	t.mu.Lock()
	raw := t.conn
	t.mu.Unlock()
	done, ok := raw.(interface{ Context() context.Context })
	if !ok {
		return
	}
	select {
	case <-done.Context().Done():
	case <-time.After(wait):
	}
}
