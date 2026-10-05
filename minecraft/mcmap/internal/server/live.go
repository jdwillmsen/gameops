package server

import (
	"net/http"
	"slices"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

const (
	// maxLiveStream bounds one stream. A session is checked when a request
	// arrives and never again, so a stream left open would go on serving a
	// player who has since logged out everywhere. Ending it makes the
	// browser reconnect, and the reconnect is a request.
	maxLiveStream = 5 * time.Minute

	defaultLiveKeepalive = 15 * time.Second

	// liveWriteTimeout is how long one frame may take to reach a browser
	// before the stream is given up. It replaces the server's write timeout,
	// which counts from the start of the response and would end every stream
	// at that age.
	liveWriteTimeout = 10 * time.Second
)

var (
	// The retry field is how soon the browser reconnects after a stream
	// ends, in milliseconds. The markers it holds outlast the gap.
	ssePreamble  = []byte("retry: 1000\n")
	sseData      = []byte("data: ")
	sseEnd       = []byte("\n\n")
	sseKeepalive = []byte(": keepalive\n\n")
)

// Why a live stream ended; the label values of the streams metric.
const (
	endClient = "client"
	endLimit  = "limit"
	endWrite  = "write"
	endStop   = "shutdown"
)

// handleLive streams a dimension's players and mobs as server-sent events:
// the current frame at once, then each new one as it is assembled.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	if !slices.Contains(render.Dimensions, dimension) {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	lifetime := maxLiveStream
	if expires, ok := auth.ExpiryFromContext(r.Context()); ok {
		lifetime = min(lifetime, expires.Sub(s.Sessions.Now()))
	}
	sub, ok := s.Live.Subscribe(dimension)
	if !ok {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "too many live streams", http.StatusServiceUnavailable)
		return
	}
	reason := endClient
	defer func() { sub.Close(reason) }()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// The gateway in front of this passes small events straight through with
	// or without being asked. This asks anyway, so that a buffering policy
	// added there later does not silently turn the stream into bursts.
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	write := func(chunks ...[]byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(liveWriteTimeout))
		for _, chunk := range chunks {
			if _, err := w.Write(chunk); err != nil {
				return false
			}
		}
		return rc.Flush() == nil
	}

	first := s.Live.Current(dimension, time.Now())
	if !write(ssePreamble, sseData, first.Data, sseEnd) {
		reason = endWrite
		return
	}

	keepalive := s.LiveKeepalive
	if keepalive <= 0 {
		keepalive = defaultLiveKeepalive
	}
	quiet := time.NewTimer(keepalive)
	defer quiet.Stop()
	end := time.NewTimer(lifetime)
	defer end.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			reason = endLimit
			return
		case m, open := <-sub.C:
			if !open {
				reason = endStop
				return
			}
			if !write(sseData, m.Data, sseEnd) {
				reason = endWrite
				return
			}
			sub.Sent(m)
		case <-quiet.C:
			// A comment, which the browser discards: it exists for the
			// proxies in between, which cut a connection that says nothing.
			if !write(sseKeepalive) {
				reason = endWrite
				return
			}
		}
		quiet.Reset(keepalive)
	}
}
