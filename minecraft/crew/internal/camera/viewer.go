package camera

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrFull is returned by Attach when the hub already serves its cap.
	ErrFull = errors.New("camera: viewer limit reached")
	// ErrTooSlow ends a viewer whose backlog outgrew its queue. Dropping is
	// safe because a reconnect replays the held world.
	ErrTooSlow = errors.New("camera: viewer too slow")
	errClosed  = errors.New("camera: viewer closed")
)

// Viewer is one subscriber's bounded queue. The hub only ever enqueues into
// it, under the viewer's own short lock, so a viewer that stops reading can
// slow nothing but itself.
type Viewer struct {
	hub   *Hub
	limit int
	tick  time.Duration

	mu sync.Mutex
	// head is what a connection needs before anything else.
	head [][]byte
	// fifo holds what must arrive in order. ents and self hold state where
	// only the newest value matters, so a burst of moves costs one slot per
	// thing that moved, and is sent as one frame per tick.
	fifo     [][]byte
	ents     map[uint64][]byte
	self     *selfPose
	lastTick time.Time
	err      error
	wake     chan struct{}
}

func newViewer(h *Hub, limit int, tick time.Duration) *Viewer {
	return &Viewer{hub: h, limit: limit, tick: tick, ents: map[uint64][]byte{}, wake: make(chan struct{}, 1)}
}

// push never blocks. It keeps order and counts against the queue bound, and
// reports true when this call is the one that cut the viewer loose.
func (v *Viewer) push(m []byte) (cut bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return false
	}
	if len(v.fifo) >= v.limit {
		v.err, v.head, v.fifo, v.ents, v.self = ErrTooSlow, nil, nil, nil, nil
		v.signal()
		return true
	}
	v.fifo = append(v.fifo, m)
	v.signal()
	return false
}

func (v *Viewer) setEntity(id uint64, rec []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return
	}
	v.ents[id] = rec
	v.signal()
}

// dropEntity forgets one coalesced entry, for an entity that has left.
func (v *Viewer) dropEntity(id uint64) {
	v.mu.Lock()
	delete(v.ents, id)
	v.mu.Unlock()
}

func (v *Viewer) setSelf(p selfPose) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return
	}
	v.self = &p
	v.signal()
}

// restart discards everything queued but the reset marker, because the world
// it described is gone. The head stays: it is about the connection, not the
// world.
func (v *Viewer) restart() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return
	}
	v.fifo, v.ents, v.self = [][]byte{resetFrame()}, map[uint64][]byte{}, nil
	v.signal()
}

func (v *Viewer) signal() {
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

// Next blocks until a message is ready, the context ends, or the viewer was
// dropped. Order: connection head, ordered updates, then at most one batch of
// moved state per tick.
func (v *Viewer) Next(ctx context.Context) ([]byte, error) {
	for {
		v.mu.Lock()
		var wait time.Duration
		switch {
		case v.err != nil:
			err := v.err
			v.mu.Unlock()
			return nil, err
		case len(v.head) > 0:
			m := v.head[0]
			v.head = v.head[1:]
			v.mu.Unlock()
			return v.sent(m), nil
		case len(v.fifo) > 0:
			m := v.fifo[0]
			v.fifo = v.fifo[1:]
			v.mu.Unlock()
			return v.sent(m), nil
		case len(v.ents) > 0 || v.self != nil:
			if wait = v.tick - time.Since(v.lastTick); wait <= 0 {
				m := v.takeTickLocked()
				v.mu.Unlock()
				return v.sent(m), nil
			}
		}
		v.mu.Unlock()

		var due <-chan time.Time
		var timer *time.Timer
		if wait > 0 {
			timer = time.NewTimer(wait)
			due = timer.C
		}
		select {
		case <-v.wake:
		case <-due:
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil, ctx.Err()
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (v *Viewer) takeTickLocked() []byte {
	recs := make([][]byte, 0, len(v.ents))
	for id, r := range v.ents {
		recs = append(recs, r)
		delete(v.ents, id)
	}
	m := encodeTick(v.self, recs)
	v.self = nil
	v.lastTick = time.Now()
	return m
}

func (v *Viewer) sent(m []byte) []byte {
	v.hub.bytesSent.Add(uint64(len(m)))
	if len(m) > 0 && m[0] == frameSubChunk {
		v.hub.subChunksSent.Add(1)
	}
	return m
}

// Close detaches the viewer from the hub. It is safe to call more than once.
func (v *Viewer) Close() {
	v.hub.detach(v)
	v.mu.Lock()
	if v.err == nil {
		v.err = errClosed
	}
	v.head, v.fifo, v.ents, v.self = nil, nil, nil, nil
	v.mu.Unlock()
}
