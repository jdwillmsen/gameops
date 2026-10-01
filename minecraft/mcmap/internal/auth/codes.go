package auth

import (
	"container/list"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnknownCode covers a code that never existed, has expired, or was
	// already used. One answer for all three, so a guess learns nothing.
	ErrUnknownCode = errors.New("that code is not valid or has expired")
)

// State is where a browser's pending login stands.
type State int

const (
	Unknown State = iota
	Pending
	Claimed
)

// No 0, O, 1 or I: the code is read off one screen and typed on another.
const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const codeLength = 6

// Codes is the in-game login. The browser is shown a short code and keeps a
// secret; the player types the code into game chat, which only someone on
// the server can do, and the agent reports who typed it. The browser, still
// holding the secret, then collects the login.
//
// Everything is in memory: a restart only means a player asks for a new code.
type Codes struct {
	TTL time.Duration
	// Max bounds how many logins can be waiting, so that nothing on the
	// internet can grow this table without limit. When it is full the oldest
	// login makes room: refusing instead would let one burst of requests
	// lock every player out for a whole TTL. Pushing out a code a player is
	// still typing therefore takes Max requests inside those few seconds,
	// and has to be kept up.
	Max int
	Now func() time.Time

	mu       sync.Mutex
	byCode   map[string]*pending
	bySecret map[string]*pending
	// Oldest first. Every login gets the same TTL, so this is also the
	// order they expire in, and neither pruning nor making room has to
	// walk the table while holding the lock.
	order list.List
}

type pending struct {
	code    string
	secret  string
	expires time.Time
	claimed bool
	id      Identity
	at      *list.Element
}

// Start returns a code for a browser to show, and the secret it must keep.
// A browser that already holds a live secret gets the same code back, so a
// page reload neither changes the code mid-login nor strands a login the
// player has already typed.
func (c *Codes) Start(secret string) (code, newSecret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	if p, ok := c.bySecret[secret]; ok {
		return p.code, p.secret
	}
	for c.order.Len() > 0 && c.order.Len() >= c.Max {
		c.forget(c.order.Front().Value.(*pending))
	}
	if c.byCode == nil {
		c.byCode, c.bySecret = map[string]*pending{}, map[string]*pending{}
	}
	for {
		code = random(codeLength)
		if _, taken := c.byCode[code]; !taken {
			break
		}
	}
	p := &pending{code: code, secret: encoding.EncodeToString(randomBytes(32)), expires: c.Now().Add(c.TTL)}
	p.at = c.order.PushBack(p)
	c.byCode[p.code], c.bySecret[p.secret] = p, p
	return p.code, p.secret
}

// Claim records that id typed code in game.
func (c *Codes) Claim(code string, id Identity) error {
	if err := id.check(); err != nil {
		return err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	p, ok := c.byCode[code]
	if !ok || p.claimed {
		return ErrUnknownCode
	}
	p.claimed, p.id = true, id
	return nil
}

// Poll reports a pending login's state. A claimed one is handed over once
// and then forgotten.
func (c *Codes) Poll(secret string) (State, Identity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	p, ok := c.bySecret[secret]
	if !ok {
		return Unknown, Identity{}
	}
	if !p.claimed {
		return Pending, Identity{}
	}
	c.forget(p)
	return Claimed, p.id
}

func (c *Codes) prune() {
	now := c.Now()
	for front := c.order.Front(); front != nil; front = c.order.Front() {
		p := front.Value.(*pending)
		if now.Before(p.expires) {
			return
		}
		c.forget(p)
	}
}

func (c *Codes) forget(p *pending) {
	delete(c.byCode, p.code)
	delete(c.bySecret, p.secret)
	c.order.Remove(p.at)
}

func (c *Codes) waiting() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on a working system, and a guessable
		// login secret is worse than no service.
		panic(err)
	}
	return b
}

func random(n int) string {
	b := randomBytes(n)
	for i := range b {
		// 32 symbols, so the low five bits pick one without bias.
		b[i] = alphabet[b[i]&31]
	}
	return string(b)
}
