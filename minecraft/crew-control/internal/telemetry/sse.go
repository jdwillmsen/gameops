package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	maxStreams         = 64
	defaultKeepalive   = 15 * time.Second
	streamWriteTimeout = 5 * time.Second
)

func (p *Poller) acquireStream() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.streams >= maxStreams {
		return false
	}
	p.streams++
	return true
}

func (p *Poller) releaseStream() {
	p.mu.Lock()
	p.streams--
	p.mu.Unlock()
}

// StreamHandler serves GET /v1/units/stream as server-sent events.
func (p *Poller) StreamHandler(w http.ResponseWriter, r *http.Request) {
	if !p.acquireStream() {
		http.Error(w, "too many viewers", http.StatusServiceUnavailable)
		return
	}
	defer p.releaseStream()

	// Subscribing before the first snapshot means a change in between is
	// delivered twice at worst, never missed.
	updates, cancel := p.Subscribe()
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Stops the ingress from buffering the stream until it fills.
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if !writeEvent(w, rc, p.Snapshot()) {
		return
	}
	// Events go out only on a visible change, so an idle unit's seen would go
	// stale; a full event on this timer bounds that and keeps the link alive.
	refresh := time.NewTicker(p.keepalive)
	defer refresh.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case snap := <-updates:
			if !writeEvent(w, rc, snap) {
				return
			}
		case <-refresh.C:
			if !writeEvent(w, rc, p.Snapshot()) {
				return
			}
		}
	}
}

// The write deadline frees the slot of a client that stopped reading instead
// of letting it block the handler forever.
func writeEvent(w http.ResponseWriter, rc *http.ResponseController, snap []Status) bool {
	data, err := json.Marshal(snap)
	if err != nil {
		return false
	}
	if err := rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: units\ndata: %s\n\n", data); err != nil {
		return false
	}
	return rc.Flush() == nil
}
