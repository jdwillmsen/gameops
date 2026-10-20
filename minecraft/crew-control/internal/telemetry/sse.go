package telemetry

import (
	"bytes"
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
	positionEvery      = 2 * time.Second
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

// StreamHandler serves GET /v1/units/stream as server-sent events. Whether a
// stream is signed in is decided once, at connect, by authed; only those
// streams receive work events, which carry task bodies. A nil authed means
// every stream is anonymous.
func (p *Poller) StreamHandler(authed func(*http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.stream(w, r, authed)
	}
}

func (p *Poller) stream(w http.ResponseWriter, r *http.Request, authed func(*http.Request) bool) {
	// The cookie is re-checked before every work event: a session that lapsed
	// or was revoked must stop receiving task bodies on a stream that stays
	// open. Once it fails the stream is anonymous for good.
	signedIn := authed != nil && authed(r)
	stillSignedIn := func() bool {
		if signedIn && !authed(r) {
			signedIn = false
		}
		return signedIn
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

	feedCh, cancelFeed := p.SubscribeEvents()
	defer cancelFeed()

	var (
		workSig     <-chan struct{}
		workPending func() []string
	)
	if signedIn {
		sub, cancelWork := p.subscribeWork()
		defer cancelWork()
		workSig, workPending = sub.sig, sub.take
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Stops the ingress from buffering the stream until it fills.
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	var lastPos []byte
	if !writeUnits(w, rc, p.Snapshot()) {
		return
	}
	// The backlog goes out after the subscription starts, so an event in
	// between arrives twice at worst; the console drops repeats by seq.
	if data, err := json.Marshal(p.Events()); err != nil || !writeEvent(w, rc, "events", data) {
		return
	}
	if signedIn && !p.writePositions(w, rc, &lastPos) {
		return
	}
	if signedIn {
		// The panel renders history from work events alone, so a new stream
		// needs one per unit rather than waiting for the next change.
		for _, u := range p.units {
			if !stillSignedIn() {
				break
			}
			if !p.writeWork(w, rc, r, u.ID) {
				return
			}
		}
	}
	// Events go out only on a visible change, so an idle unit's seen would go
	// stale; a full event on this timer bounds that and keeps the link alive.
	refresh := time.NewTicker(p.keepalive)
	defer refresh.Stop()
	var posTick <-chan time.Time
	if signedIn {
		t := time.NewTicker(positionEvery)
		defer t.Stop()
		posTick = t.C
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case snap := <-updates:
			if !writeUnits(w, rc, snap) {
				return
			}
		case e := <-feedCh:
			data, err := json.Marshal(e)
			if err != nil || !writeEvent(w, rc, "log", data) {
				return
			}
		case <-posTick:
			if !stillSignedIn() {
				posTick = nil
				continue
			}
			if !p.writePositions(w, rc, &lastPos) {
				return
			}
		case <-workSig:
			for _, u := range workPending() {
				if !stillSignedIn() {
					break
				}
				if !p.writeWork(w, rc, r, u) {
					return
				}
			}
		case <-refresh.C:
			if !writeUnits(w, rc, p.Snapshot()) {
				return
			}
		}
	}
}

// writeWork skips a unit whose history cannot be read (the store is down)
// rather than ending the stream: the units event does not depend on it.
func (p *Poller) writeWork(w http.ResponseWriter, rc *http.ResponseController, r *http.Request, unit string) bool {
	src := p.workSource()
	if src == nil {
		return true
	}
	data, err := src(r.Context(), unit)
	if err != nil {
		return true
	}
	return writeEvent(w, rc, "work", data)
}

func writeUnits(w http.ResponseWriter, rc *http.ResponseController, snap []Status) bool {
	data, err := json.Marshal(snap)
	if err != nil {
		return false
	}
	return writeEvent(w, rc, "units", data)
}

// The write deadline frees the slot of a client that stopped reading instead
// of letting it block the handler forever.
func writeEvent(w http.ResponseWriter, rc *http.ResponseController, event string, data []byte) bool {
	if err := rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	return rc.Flush() == nil
}

// writePositions sends only when something moved, so an idle fleet costs a
// signed-in stream nothing. Coordinates are operator-only: the public status
// has none.
func (p *Poller) writePositions(w http.ResponseWriter, rc *http.ResponseController, last *[]byte) bool {
	data, err := json.Marshal(p.Positions())
	if err != nil {
		return false
	}
	if bytes.Equal(data, *last) {
		return true
	}
	*last = data
	return writeEvent(w, rc, "positions", data)
}
