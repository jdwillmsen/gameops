// Package anon bounds what an unauthenticated caller can make the event log
// do. The explain and capacity routes are public but scan the stored events,
// so an anonymous caller gets a narrower window and a short shared result.
package anon

import (
	"net/http"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

const (
	// MaxWindow is the widest span an anonymous caller may ask for; signed-in
	// operators keep the full 24 h.
	MaxWindow = time.Hour

	ttl        = 10 * time.Second
	maxEntries = 128
)

// Gate is the part of auth.Auth that tells signed-in callers apart.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

// GateFunc adapts a function, such as auth.Auth.Operator, to Gate.
type GateFunc func(r *http.Request) (auth.Operator, bool)

func (f GateFunc) Operator(r *http.Request) (auth.Operator, bool) { return f(r) }

// Signed reports whether the request carries an operator session. A nil gate
// means nobody is signed in.
func Signed(g Gate, r *http.Request) bool {
	if g == nil {
		return false
	}
	_, ok := g.Operator(r)
	return ok
}

// Reply is one finished answer.
type Reply struct {
	Status int
	Body   []byte
}

type entry struct {
	reply   Reply
	expires time.Time
}

// Cache holds anonymous answers for a few seconds. Scans are serialised, so
// a burst of anonymous requests costs one scan at a time and repeats of the
// same query cost none; a cache hit never waits behind a scan.
type Cache struct {
	mu   sync.Mutex
	scan sync.Mutex
	now  func() time.Time
	m    map[string]entry
}

func NewCache() *Cache { return &Cache{now: time.Now, m: map[string]entry{}} }

// SetClock replaces the time source; tests use it to expire entries.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }

func (c *Cache) get(key string) (Reply, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || !c.now().Before(e.expires) {
		return Reply{}, false
	}
	return e.reply, true
}

func (c *Cache) put(key string, r Reply) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.m) >= maxEntries {
		oldest, first := "", true
		var oldestAt time.Time
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
			} else if first || e.expires.Before(oldestAt) {
				oldest, oldestAt, first = k, e.expires, false
			}
		}
		// Every entry is still fresh: drop the one closest to expiry rather
		// than the whole cache, so a flood of distinct keys cannot flush hot
		// entries all at once.
		if len(c.m) >= maxEntries {
			delete(c.m, oldest)
		}
	}
	c.m[key] = entry{reply: r, expires: now.Add(ttl)}
}

// Do returns the cached answer for key or computes it. Only successful
// answers are kept: a failed scan must be retried, not replayed.
func (c *Cache) Do(key string, compute func() (Reply, bool)) Reply {
	if r, ok := c.get(key); ok {
		return r
	}
	c.scan.Lock()
	defer c.scan.Unlock()
	// Another request may have filled the entry while this one waited.
	if r, ok := c.get(key); ok {
		return r
	}
	r, keep := compute()
	if keep {
		c.put(key, r)
	}
	return r
}

// Write sends a reply as JSON.
func Write(w http.ResponseWriter, r Reply) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(r.Status)
	_, _ = w.Write(r.Body)
}
