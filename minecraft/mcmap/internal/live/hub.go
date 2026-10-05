package live

import (
	"sync"
	"time"
)

// MaxSubscribers bounds the streams held open at once. Each is a goroutine
// and, at the entity cap, tens of kilobytes a second; a session is all it
// takes to open one, and nothing else limits how many a browser opens.
const MaxSubscribers = 256

// Message is one frame, already serialised, on its way to a browser.
type Message struct {
	Data []byte
	// Ready is when the frame was serialised, which is where the time to
	// reach a browser is measured from.
	Ready time.Time
}

// Hub hands each dimension's frames to the browsers watching it.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
	n    int
	// closed is set on shutdown, after which nothing is delivered.
	closed bool
}

// Subscription is one browser's place in the hub.
type Subscription struct {
	// C carries the newest frame not yet read. It holds one: a frame is the
	// whole picture, so an unread older one is only ever in the way. It is
	// closed when the hub is.
	C <-chan Message

	c         chan Message
	hub       *Hub
	dimension string
	once      sync.Once
}

// Subscribe starts delivering a dimension's frames. It reports false when
// the hub is full or closed.
func (h *Hub) Subscribe(dimension string) (*Subscription, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.n >= MaxSubscribers {
		return nil, false
	}
	c := make(chan Message, 1)
	sub := &Subscription{C: c, c: c, hub: h, dimension: dimension}
	if h.subs == nil {
		h.subs = map[string]map[*Subscription]struct{}{}
	}
	if h.subs[dimension] == nil {
		h.subs[dimension] = map[*Subscription]struct{}{}
	}
	h.subs[dimension][sub] = struct{}{}
	h.n++
	metricSubscribers.Inc()
	return sub, true
}

// Close ends the subscription, recording why the stream it fed ended.
func (s *Subscription) Close(reason string) {
	s.once.Do(func() {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()
		delete(s.hub.subs[s.dimension], s)
		s.hub.n--
		metricSubscribers.Dec()
		metricStreams.WithLabelValues(reason).Inc()
	})
}

// Sent records that m reached the browser.
func (s *Subscription) Sent(m Message) {
	metricFanout.Observe(time.Since(m.Ready).Seconds())
}

// Publish offers a frame to everyone watching the dimension and never
// waits for any of them. A browser that has not read its last frame has it
// replaced: one slow reader must not hold up the rest, and the frame it
// missed is superseded anyway.
func (h *Hub) Publish(dimension string, m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for sub := range h.subs[dimension] {
		select {
		case <-sub.c:
			metricFanoutDropped.Inc()
		default:
		}
		// Only Publish sends, and it holds the lock, so the slot just
		// emptied is still empty.
		select {
		case sub.c <- m:
		default:
		}
	}
}

// Close ends every stream. A graceful HTTP shutdown waits for handlers
// without telling them, so without this each open stream would hold the
// shutdown until it timed out.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, subs := range h.subs {
		for sub := range subs {
			close(sub.c)
		}
	}
}
