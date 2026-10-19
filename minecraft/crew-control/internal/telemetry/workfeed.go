package telemetry

import (
	"context"
	"sync"
)

// WorkSource renders the work event payload of one unit. The poller does not
// know the queue; main hands it this so only signed-in streams can read it.
type WorkSource func(ctx context.Context, unit string) ([]byte, error)

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

// PublishWork tells every signed-in stream that unit's work changed.
func (p *Poller) PublishWork(unit string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for s := range p.wsubs {
		s.mark(unit)
	}
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
