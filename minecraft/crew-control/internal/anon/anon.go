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

// Cache holds anonymous answers for a few seconds. Computation runs under the
// lock, so a burst of anonymous requests costs one scan at a time and repeats
// of the same query cost none.
type Cache struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]entry
}

func NewCache() *Cache { return &Cache{now: time.Now, m: map[string]entry{}} }

// SetClock replaces the time source; tests use it to expire entries.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }

// Do returns the cached answer for key or computes it. Only successful
// answers are kept: a failed scan must be retried, not replayed.
func (c *Cache) Do(key string, compute func() (Reply, bool)) Reply {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if e, ok := c.m[key]; ok && now.Before(e.expires) {
		return e.reply
	}
	r, keep := compute()
	if !keep {
		return r
	}
	if len(c.m) >= maxEntries {
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
			}
		}
		// Every entry is still fresh: distinct queries are arriving faster
		// than they expire, so start over rather than grow.
		if len(c.m) >= maxEntries {
			clear(c.m)
		}
	}
	c.m[key] = entry{reply: r, expires: now.Add(ttl)}
	return r
}

// Write sends a reply as JSON.
func Write(w http.ResponseWriter, r Reply) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(r.Status)
	_, _ = w.Write(r.Body)
}
