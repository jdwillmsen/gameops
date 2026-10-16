package camera

import (
	"cmp"
	"context"
	"errors"
	"slices"
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

	// The held world is not queued: the viewer pulls it, nearest first, as it
	// reads, so a big world paces itself on the socket and never counts as a
	// backlog. sent is what the viewer already has; pending is the plan for
	// the rest, rebuilt when dirty.
	radius  int // in chunks, around the unit
	hasPose bool
	pos     [3]float32
	center  [3]int32
	sent    map[[3]int32]struct{}
	pending [][3]int32
	dirty   bool
}

func newViewer(h *Hub, limit int, tick time.Duration, radius int) *Viewer {
	return &Viewer{
		hub: h, limit: limit, tick: tick, radius: radius,
		ents: map[uint64][]byte{}, sent: map[[3]int32]struct{}{}, wake: make(chan struct{}, 1),
	}
}

// SetRadius changes how far around the unit the viewer wants sub-chunks, in
// chunks, within the hub's bounds. Growing it replays what was out of range.
func (v *Viewer) SetRadius(r int) {
	r = min(max(r, 1), v.hub.maxRadius)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return
	}
	if r > v.radius {
		v.dirty = true
		v.signal()
	}
	v.radius = r
}

func chunkOf(p [3]float32) [3]int32 {
	var c [3]int32
	for i, f := range p {
		c[i] = int32(floor32(f / 16))
	}
	return c
}

func floor32(f float32) int {
	i := int(f)
	if float32(i) > f {
		i--
	}
	return i
}

func (v *Viewer) wantsLocked(k [3]int32) bool {
	if !v.hasPose {
		return false
	}
	for i := range k {
		d := k[i] - v.center[i]
		if d > int32(v.radius) || -d > int32(v.radius) {
			return false
		}
	}
	return true
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
		v.err, v.head, v.fifo, v.ents, v.self, v.pending = ErrTooSlow, nil, nil, nil, nil, nil
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
	v.setPoseLocked(p)
	v.signal()
}

func (v *Viewer) setPoseLocked(p selfPose) {
	v.pos = [3]float32{p.X, p.Y, p.Z}
	if c := chunkOf(v.pos); !v.hasPose || c != v.center {
		v.hasPose, v.center, v.dirty = true, c, true
	}
}

// noteSub tells the viewer a held sub-chunk changed. A chunk it already has is
// updated in order; one it has not seen waits for the replay, which reads the
// held copy, so nothing stale is ever sent.
func (v *Viewer) noteSub(key [3]int32, frame []byte, present bool) (cut bool) {
	v.mu.Lock()
	if _, have := v.sent[key]; have {
		if !present {
			delete(v.sent, key)
		}
		v.mu.Unlock()
		return v.push(frame)
	}
	defer v.mu.Unlock()
	if present && v.err == nil && v.wantsLocked(key) {
		v.dirty = true
		v.signal()
	}
	return false
}

// noteEdit forwards a single-block edit if the viewer has that chunk.
func (v *Viewer) noteEdit(key [3]int32, frame []byte) (cut bool) {
	v.mu.Lock()
	_, have := v.sent[key]
	v.mu.Unlock()
	return have && v.push(frame)
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
	v.sent, v.pending, v.dirty = map[[3]int32]struct{}{}, nil, false
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
			return v.counted(m), nil
		case len(v.fifo) > 0:
			m := v.fifo[0]
			v.fifo = v.fifo[1:]
			v.mu.Unlock()
			return v.counted(m), nil
		case len(v.ents) > 0 || v.self != nil:
			if wait = v.tick - time.Since(v.lastTick); wait <= 0 {
				m := v.takeTickLocked()
				v.mu.Unlock()
				return v.counted(m), nil
			}
		}
		replay := v.dirty || len(v.pending) > 0
		v.mu.Unlock()

		if replay {
			if m := v.nextReplay(); m != nil {
				return v.counted(m), nil
			}
		}

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

// nextReplay returns the nearest sub-chunk the viewer wants and lacks, or nil.
func (v *Viewer) nextReplay() []byte {
	for {
		v.mu.Lock()
		dirty := v.dirty
		v.mu.Unlock()
		if dirty {
			v.hub.plan(v)
		}
		v.mu.Lock()
		if v.err != nil || len(v.pending) == 0 {
			v.mu.Unlock()
			return nil
		}
		k := v.pending[0]
		v.pending = v.pending[1:]
		if _, have := v.sent[k]; have || !v.wantsLocked(k) {
			v.mu.Unlock()
			continue
		}
		// Marked before the frame is read: a change that lands in between
		// is then queued in order after it instead of being missed.
		v.sent[k] = struct{}{}
		v.mu.Unlock()
		if f := v.hub.heldFrame(k); f != nil {
			return f
		}
		v.mu.Lock()
		delete(v.sent, k)
		v.mu.Unlock()
	}
}

// plan rebuilds the viewer's replay list: held sub-chunks in range that it has
// not been sent, nearest to the unit first.
func (h *Hub) plan(v *Viewer) {
	h.mu.Lock()
	v.mu.Lock()
	v.dirty = false
	if v.err != nil || !v.hasPose {
		v.pending = nil
		v.mu.Unlock()
		h.mu.Unlock()
		return
	}
	pos := v.pos
	var keys [][3]int32
	for k := range h.subs {
		if _, have := v.sent[k]; !have && v.wantsLocked(k) {
			keys = append(keys, k)
		}
	}
	v.mu.Unlock()
	h.mu.Unlock()

	slices.SortFunc(keys, func(a, b [3]int32) int { return cmp.Compare(distSq(a, pos), distSq(b, pos)) })
	v.mu.Lock()
	v.pending = keys
	v.mu.Unlock()
}

func (h *Hub) heldFrame(k [3]int32) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.subs[k]
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

func (v *Viewer) counted(m []byte) []byte {
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
