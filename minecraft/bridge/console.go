package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// consoleEndpoint and the auth subprotocol name are dictated by
// itzg/mc-server-runner's websocket_shell_service.go and are not
// configurable server-side beyond the password.
const (
	consoleEndpoint = "/console"
	authSubproto    = "mc-server-runner-ws-v1"

	// commandCollectWindow is how long SendCommand waits for stdout/stderr
	// broadcasts after writing a command, before returning whatever arrived.
	// Config.CommandTimeout caps it if it is set lower.
	// The protocol carries no request/response correlation id, so output
	// collected in this window may include unrelated concurrent console
	// activity. That is an inherent limit of the console protocol, not a
	// bridge bug; callers should treat the result as best-effort.
	commandCollectWindow = 800 * time.Millisecond

	minReconnectDelay = 1 * time.Second
	maxReconnectDelay = 30 * time.Second

	// The console is idle for long stretches on a quiet server, so the read
	// loop cannot use a deadline to notice a peer that has gone away.
	// coder/websocket sends no pings of its own and TCP keepalives take
	// minutes to fire, so a half-open connection (NAT idle reap, the server
	// container vanishing without an RST) would otherwise leave Read blocked
	// while /readyz still reported the console up.
	pingInterval = 30 * time.Second
	pingTimeout  = 10 * time.Second

	// firstFrameTimeout bounds the wait for the first frame after a
	// successful handshake. mc-server-runner sends its logHistory backfill
	// immediately; silence past this means the peer is not really serving.
	// The connection is only promoted on that first frame, so without this
	// deadline a silent peer would neither connect nor redial.
	firstFrameTimeout = 15 * time.Second

	// maxResidualLine bounds the partial-line buffer held between websocket
	// frames. A console line longer than this is discarded rather than grown
	// without limit, mirroring ParseEvents' scanner cap.
	maxResidualLine = 1 << 20
)

// wsMessage mirrors the JSON envelope mc-server-runner uses on the wire.
type wsMessage struct {
	Type   string   `json:"type"`
	Data   string   `json:"data,omitempty"`
	Lines  []string `json:"lines,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

// Console maintains a reconnecting websocket connection to the bedrock
// server's console and serializes commands through it. It also feeds
// Events: mc-server-runner pushes every console stdout/stderr line and a
// logHistory backfill over this same connection, so no separate log-tailing
// mechanism is needed for GET /events.
type Console struct {
	addr           string
	password       string
	origin         string
	commandTimeout time.Duration
	logger         *slog.Logger

	// The reconnect bounds and the first-frame deadline are fields rather
	// than constants so tests can drive the connect schedule on a short,
	// deterministic timescale.
	minReconnectDelay time.Duration
	maxReconnectDelay time.Duration
	firstFrameTimeout time.Duration

	mu        sync.Mutex
	conn      *websocket.Conn
	connected bool

	// cmdMu serializes SendCommand's write-plus-collect sequence so two
	// concurrent callers don't interleave writes and each collect the
	// other's output.
	cmdMu sync.Mutex

	subMu       sync.Mutex
	subscribers map[chan wsMessage]struct{}

	// residual holds the trailing bytes of a stdout/stderr frame that did
	// not end on a line boundary. mc-server-runner broadcasts pipe-read
	// chunks, not lines, so a log line can straddle two frames. Only the
	// connectAndRead read loop touches these, so they need no lock.
	residual map[string]string

	Events *EventLog
}

func NewConsole(addr, password, origin string, commandTimeout time.Duration, logger *slog.Logger) *Console {
	return &Console{
		addr:           addr,
		password:       password,
		origin:         origin,
		commandTimeout: commandTimeout,
		logger:         logger,

		minReconnectDelay: minReconnectDelay,
		maxReconnectDelay: maxReconnectDelay,
		firstFrameTimeout: firstFrameTimeout,
		subscribers:       make(map[chan wsMessage]struct{}),
		residual:          make(map[string]string),
		Events:            NewEventLog(),
	}
}

// Run connects and reconnects with backoff until ctx is cancelled.
func (c *Console) Run(ctx context.Context) {
	delay := c.minReconnectDelay
	for {
		if ctx.Err() != nil {
			return
		}

		err := c.connectAndRead(ctx)
		metricConsoleReconnects.Inc()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.logger.Warn("console connection lost", "error", err)
		}

		c.mu.Lock()
		established := c.connected
		c.connected = false
		c.mu.Unlock()
		metricConsoleConnected.Set(0)

		// A connection that actually came up earns a fresh backoff, so a
		// long-lived console that drops once redials immediately instead of
		// inheriting the saturated delay from an earlier startup race.
		if established {
			delay = c.minReconnectDelay
		}

		var jitter time.Duration
		if half := int64(delay) / 2; half > 0 {
			jitter = time.Duration(rand.Int63n(half))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay + jitter):
		}
		if delay < c.maxReconnectDelay {
			delay *= 2
			if delay > c.maxReconnectDelay {
				delay = c.maxReconnectDelay
			}
		}
	}
}

func (c *Console) connectAndRead(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("ws://%s%s", c.addr, consoleEndpoint)
	// The password rides in the subprotocol list because that is the only
	// channel mc-server-runner reads it from. Declaring it via Subprotocols
	// (rather than a hand-set header) also lets the dialer accept the
	// negotiated protocol the server echoes back in its 101 response.
	//
	// coder/websocket sends no Origin of its own, so without this header the
	// server sees an empty origin, which no WEBSOCKET_ALLOWED_ORIGINS entry
	// can match: its flag parser drops blank list entries. Sending a real
	// origin is what lets the server keep its origin check enabled; a server
	// with the check disabled ignores the header.
	conn, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		Subprotocols: []string{authSubproto, c.password},
		HTTPHeader:   http.Header{"Origin": []string{c.origin}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("console auth rejected: %w", err)
		}
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	clear(c.residual)

	// The connection is not promoted to c.conn/c.connected until the first
	// non-authFailure frame arrives. mc-server-runner accepts the websocket
	// handshake before it has checked the password - auth is confirmed only
	// by what arrives afterward - so setting connected right after Dial
	// would mark a rejected password as an established session. Run's
	// backoff-reset logic keys off c.connected, so that earlier bug reset
	// the reconnect delay to minReconnectDelay on every attempt: a wrong
	// WEBSOCKET_PASSWORD hammered the server every second, forever.
	established := false

	// A completed handshake proves nothing about the peer, so both the wait
	// for that first frame and the long idle read afterwards need their own
	// liveness bound. Cancelling readCtx is what unblocks conn.Read and
	// hands control back to Run's backoff.
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()

	firstFrame := time.AfterFunc(c.firstFrameTimeout, func() {
		c.logger.Warn("no console frame after handshake, redialing", "addr", c.addr)
		cancelRead()
	})
	defer firstFrame.Stop()

	go c.keepalive(readCtx, conn, cancelRead)

	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			return err
		}
		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.Warn("console frame not decodable, skipping", "error", err)
			continue
		}
		if msg.Type == "authFailure" {
			return fmt.Errorf("%w: %s", ErrAuthFailure, msg.Reason)
		}
		if !established {
			firstFrame.Stop()
			c.mu.Lock()
			c.conn = conn
			c.connected = true
			c.mu.Unlock()
			metricConsoleConnected.Set(1)
			c.logger.Info("console connected", "addr", c.addr)
			established = true
		}
		c.ingestEvents(msg)
		c.broadcast(msg)
	}
}

// keepalive pings the peer until ctx ends, cancelling the read loop through
// onFailure when a ping goes unanswered. Ping needs the read loop running
// concurrently to see the pong, which is exactly where it is started from.
func (c *Console) keepalive(ctx context.Context, conn *websocket.Conn, onFailure context.CancelFunc) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err == nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("console ping unanswered, redialing", "addr", c.addr, "error", err)
			onFailure()
			return
		}
	}
}

// ingestEvents feeds Events from a console broadcast: the one-shot
// logHistory backfill on connect, and every stdout/stderr line as it
// arrives. Received-at time is used for all events, including backfilled
// history, since the raw line's own timestamp format isn't parsed here -
// backfilled events also carry Backfill=true so a consumer can tell replayed
// history from a live line instead of mistaking a months-old reconnect for
// one happening now.
func (c *Console) ingestEvents(msg wsMessage) {
	now := time.Now()
	switch msg.Type {
	case "logHistory":
		for _, line := range msg.Lines {
			c.Events.IngestBackfill(line, now)
		}
	case "stdout", "stderr":
		buf := c.residual[msg.Type] + msg.Data
		end := strings.LastIndex(buf, "\n")
		if end < 0 {
			if len(buf) > maxResidualLine {
				c.logger.Warn("dropping oversized partial console line", "stream", msg.Type, "bytes", len(buf))
				buf = ""
			}
			c.residual[msg.Type] = buf
			return
		}
		c.residual[msg.Type] = buf[end+1:]
		for _, line := range strings.Split(buf[:end], "\n") {
			if line == "" {
				continue
			}
			c.Events.Ingest(line, now)
		}
	}
}

func (c *Console) broadcast(msg wsMessage) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for ch := range c.subscribers {
		select {
		case ch <- msg:
		default:
			// Slow subscriber: drop rather than block the read loop.
		}
	}
}

func (c *Console) subscribe() (chan wsMessage, func()) {
	ch := make(chan wsMessage, 32)
	c.subMu.Lock()
	c.subscribers[ch] = struct{}{}
	c.subMu.Unlock()
	return ch, func() {
		c.subMu.Lock()
		delete(c.subscribers, ch)
		c.subMu.Unlock()
		close(ch)
	}
}

// Connected reports whether the console websocket is currently established.
func (c *Console) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// ErrNotConnected is returned by SendCommand when no console connection is
// currently established.
var ErrNotConnected = errors.New("console not connected")

// ErrAuthFailure indicates the server rejected the configured
// CONSOLE_PASSWORD. It is returned by connectAndRead so tests can assert on
// it via errors.Is; Run itself needs no special case for it; connectAndRead
// never promotes c.connected to true on this path, so Run's own
// backoff-reset check already declines to reset the delay.
var ErrAuthFailure = errors.New("console auth failure")

// SendCommand writes a command to the console and collects any stdout/stderr
// broadcasts that follow within commandCollectWindow. The output is
// best-effort: the protocol has no correlation id, so concurrent console
// activity from other sources can appear in the result.
func (c *Console) SendCommand(ctx context.Context, cmd string) (string, error) {
	// Serialize the whole write-plus-collect sequence: without this, two
	// concurrent callers can interleave their stdin writes and each collect
	// output that belongs to the other command. The connection is sampled
	// only once this lock is held, so a caller that spent the wait queued
	// behind someone else's collect window writes to whatever connection is
	// live now rather than one that has since been dropped and replaced.
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()

	c.mu.Lock()
	conn := c.conn
	connected := c.connected
	c.mu.Unlock()
	if !connected || conn == nil {
		return "", ErrNotConnected
	}

	sub, unsub := c.subscribe()
	defer unsub()

	writeCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()
	if err := writeJSON(writeCtx, conn, wsMessage{Type: "stdin", Data: cmd + "\n"}); err != nil {
		return "", fmt.Errorf("write command: %w", err)
	}

	collect := commandCollectWindow
	if c.commandTimeout < collect {
		collect = c.commandTimeout
	}

	var out []byte
	deadline := time.After(collect)
	for {
		select {
		case msg := <-sub:
			if msg.Type == "stdout" || msg.Type == "stderr" {
				out = append(out, msg.Data...)
			}
		case <-deadline:
			return string(out), nil
		case <-ctx.Done():
			return string(out), ctx.Err()
		}
	}
}

func writeJSON(ctx context.Context, c *websocket.Conn, v wsMessage) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, data)
}
