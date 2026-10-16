package camera

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrFull is returned by Attach when the hub already serves its cap.
	ErrFull = errors.New("camera: viewer limit reached")
	// ErrTooSlow ends a viewer whose backlog outgrew its queue. Dropping is
	// safe because a reconnect replays the whole held world.
	ErrTooSlow = errors.New("camera: viewer too slow")
	errClosed  = errors.New("camera: viewer closed")
)

// Message is one websocket frame for a viewer: JSON text, or a binary frame.
type Message struct {
	Text bool
	Data []byte
}

// Viewer is one subscriber's bounded queue. The hub only ever enqueues into
// it, under the viewer's own short lock, so a viewer that stops reading can
// slow nothing but itself.
type Viewer struct {
	hub   *Hub
	limit int

	mu sync.Mutex
	// head is the replay of the held world, sent before anything live.
	head []Message
	// fifo holds what must arrive in order. latest holds state where only the
	// newest value matters (a position, an entity's pose), so a burst of moves
	// costs one slot per thing that moved, not one per move.
	fifo   []Message
	latest map[string]Message
	err    error
	wake   chan struct{}
}

func newViewer(h *Hub, limit int) *Viewer {
	return &Viewer{hub: h, limit: limit, latest: map[string]Message{}, wake: make(chan struct{}, 1)}
}

// push never blocks. A non-empty key coalesces; an empty one keeps order and
// counts against the queue bound.
func (v *Viewer) push(m Message, key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return
	}
	if key != "" {
		v.latest[key] = m
	} else {
		if len(v.fifo) >= v.limit {
			v.err, v.head, v.fifo, v.latest = ErrTooSlow, nil, nil, nil
			v.signal()
			return
		}
		v.fifo = append(v.fifo, m)
	}
	v.signal()
}

// drop forgets one coalesced entry, for an entity that has left.
func (v *Viewer) drop(key string) {
	v.mu.Lock()
	delete(v.latest, key)
	v.mu.Unlock()
}

// restart discards everything queued but the reset marker, because the world
// it described is gone.
func (v *Viewer) restart(reset Message) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return
	}
	v.head, v.fifo, v.latest = nil, []Message{reset}, map[string]Message{}
	v.signal()
}

func (v *Viewer) signal() {
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

// Next blocks until a message is ready, the context ends, or the viewer was
// dropped. Order: held-world replay, then ordered updates, then latest state.
func (v *Viewer) Next(ctx context.Context) (Message, error) {
	for {
		v.mu.Lock()
		switch {
		case v.err != nil:
			err := v.err
			v.mu.Unlock()
			return Message{}, err
		case len(v.head) > 0:
			m := v.head[0]
			v.head = v.head[1:]
			v.mu.Unlock()
			return m, nil
		case len(v.fifo) > 0:
			m := v.fifo[0]
			v.fifo = v.fifo[1:]
			v.mu.Unlock()
			return m, nil
		case len(v.latest) > 0:
			for k, m := range v.latest {
				delete(v.latest, k)
				v.mu.Unlock()
				return m, nil
			}
		}
		v.mu.Unlock()
		select {
		case <-v.wake:
		case <-ctx.Done():
			return Message{}, ctx.Err()
		}
	}
}

// Close detaches the viewer from the hub. It is safe to call more than once.
func (v *Viewer) Close() {
	v.hub.detach(v)
	v.mu.Lock()
	if v.err == nil {
		v.err = errClosed
	}
	v.head, v.fifo, v.latest = nil, nil, nil
	v.mu.Unlock()
}

// dropped reports whether the viewer was cut for being too slow, so the hub
// can stop feeding it.
func (v *Viewer) dropped() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err != nil
}
