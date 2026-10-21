package telemetry

import (
	"context"
	"sync"
)

// WorkSource renders the work event payload of one unit. The poller does not
// know the queue; main hands it this so only signed-in streams can read it.
type WorkSource func(ctx context.Context, unit string) ([]byte, error)

// feedCache shares the bytes of one rendered payload: the first signed-in
// stream to write a given change renders it from the store, and every other
// stream then reuses those bytes instead of querying again. seq is the change
// counter, bumped when a change is published; at holds the seq the cached
// bytes were rendered at. A nil data means the current seq has not been
// rendered yet, so a stream that opens between changes draws fresh state.
type feedCache struct {
	mu   sync.Mutex
	seq  uint64
	at   uint64
	data []byte
}

// changed counts one change and drops the rendered bytes.
func (c *feedCache) changed() {
	c.mu.Lock()
	c.seq++
	c.mu.Unlock()
}

// payload returns the bytes for the current seq, rendering them once if
// another write has not: callers racing miss the cache together but wait out
// the first render under the lock, so the store is read once per change. An
// error leaves the cache alone and skips this write, as before.
func (c *feedCache) payload(src func() ([]byte, error)) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data != nil && c.at == c.seq {
		return c.data, true
	}
	d, err := src()
	if err != nil {
		return nil, false
	}
	c.at, c.data = c.seq, d
	return d, true
}

// workSub collects which units changed since its stream last wrote, so a
// slow stream coalesces updates instead of dropping one.
type workSub struct {
	mu      sync.Mutex
	pending map[string]struct{}
	sig     chan struct{}
}

func (s *workSub) mark(unit string) {
	s.mu.Lock()
	s.pending[unit] = struct{}{}
	s.mu.Unlock()
	select {
	case s.sig <- struct{}{}:
	default:
	}
}

func (s *workSub) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.pending))
	for u := range s.pending {
		out = append(out, u)
	}
	clear(s.pending)
	return out
}

func (p *Poller) SetWorkSource(src WorkSource) {
	p.mu.Lock()
	p.workSrc = src
	p.mu.Unlock()
}

func (p *Poller) workSource() WorkSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.workSrc
}

// PublishWork tells every signed-in stream that unit's work changed. The
// payload is rendered once per change, when a stream writes, and every
// signed-in stream shares the same bytes.
func (p *Poller) PublishWork(unit string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// The count moves before any stream learns of the change, so the first
	// write of it renders freshly, not from the previous change.
	if c, ok := p.workCaches[unit]; ok {
		c.changed()
	}
	for s := range p.wsubs {
		s.mark(unit)
	}
}

// workFeed returns a unit's shared bytes, or nil for a unit the roster does
// not know, which then renders without a cache.
func (p *Poller) workFeed(unit string) *feedCache {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.workCaches[unit]
}

// PublishAllWork republishes every unit, for a store that just became
// readable after streams opened without history.
func (p *Poller) PublishAllWork() {
	for _, u := range p.units {
		p.PublishWork(u.ID)
	}
}

func (p *Poller) subscribeWork() (*workSub, func()) {
	s := &workSub{pending: map[string]struct{}{}, sig: make(chan struct{}, 1)}
	p.mu.Lock()
	p.wsubs[s] = struct{}{}
	p.mu.Unlock()
	return s, func() {
		p.mu.Lock()
		delete(p.wsubs, s)
		p.mu.Unlock()
	}
}
