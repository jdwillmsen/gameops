package telemetry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	maxStreams   = 64
	pingInterval = 15 * time.Second
)

// ListHandler serves GET /v1/units.
func (p *Poller) ListHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(p.Snapshot())
}

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
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
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

	if !writeEvent(w, flusher, p.Snapshot()) {
		return
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case snap := <-updates:
			if !writeEvent(w, flusher, snap) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeEvent(w http.ResponseWriter, f http.Flusher, snap []Status) bool {
	data, err := json.Marshal(snap)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: units\ndata: %s\n\n", data); err != nil {
		return false
	}
	f.Flush()
	return true
}
