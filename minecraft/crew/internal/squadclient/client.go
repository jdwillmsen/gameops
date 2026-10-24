// Package squadclient carries one unit's side of squad coordination: it
// joins its squad, reports the status the combat engine gives it, drains
// the engine's call-outs, and keeps the standing order, the leader's
// position and the squad's roster fresh for the engine to read.
//
// The engine is injected by the agent's wiring and its interface lives
// there; this package's methods are shaped to satisfy it. Reads (Leader,
// Orders, Member, Down, Start) may be called from any goroutine; writes happen only on the
// client's own.
package squadclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
)

const (
	// Same bounds as the work client: a long poll must outlive the
	// server's wait, and a post must not hang the loop.
	pollWait    = "25s"
	pollTimeout = 40 * time.Second
	postTimeout = 10 * time.Second
	minBackoff  = time.Second
	maxBackoff  = 30 * time.Second

	// Statuses flow every couple of seconds so a healthy unit never meets
	// the server's leader grace.
	statusEvery = 2 * time.Second
	// Claims are renewed well inside the server's member TTL.
	helloEvery = 30 * time.Second
	// A call-out that could not be posted stops being news after this
	// long, and is dropped rather than read out late.
	maxCalloutAge = 10 * time.Second

	// Far above any real hello, status or call-out.
	maxPostBytes  = 1 << 10
	maxAnswerByte = 64 << 10
)

// Logger is the slice of logging.Logger the client needs.
type Logger interface {
	Info(event string, fields map[string]any)
	Error(event string, fields map[string]any)
}

// Source is what the engine hands over each tick: how the unit is now. It
// runs on the client's ticker goroutine and must not block.
type Source func() squadmsg.Status

// Client keeps the engine's view of the squad fresh. It does nothing until
// Run is called on a context the agent owns.
type Client struct {
	base  *url.URL
	unit  string
	squad string
	role  squadmsg.Role
	name  string
	token string
	http  *http.Client
	log   Logger
	src   Source
	// sleep reports false when ctx ended first; tests replace it.
	sleep func(ctx context.Context, d time.Duration) bool

	statusEvery time.Duration
	helloEvery  time.Duration

	callouts chan squadmsg.Callout

	mu sync.RWMutex
	// The leader and the standing order, as the last poll named them.
	leaderID  string
	leaderPos squadmsg.Pos
	leadKnown bool
	order     squadmsg.Order
	// The whole squad and where it started, as the last poll named them.
	members  []squadmsg.Member
	start    squadmsg.Pos
	hasStart bool
}

// New builds a client for one unit of one squad. role is the part the unit
// claims on hello; name is the optional display name it offers the squad.
func New(base *url.URL, unit, squad, name string, role squadmsg.Role, token string, log Logger) *Client {
	return &Client{
		base: base, unit: unit, squad: squad, name: name, role: role,
		token: token, log: log,
		http:  &http.Client{},
		sleep: realSleep,

		statusEvery: statusEvery,
		helloEvery:  helloEvery,
		callouts:    make(chan squadmsg.Callout, 16),
	}
}

// SetSource wires the status provider. Safe to call while Run is live:
// the next status tick picks the new source up. A nil source reports
// nothing, which is right for a unit that is not fighting.
func (c *Client) SetSource(s Source) {
	c.mu.Lock()
	c.src = s
	c.mu.Unlock()
}

// source reads the provider under the state lock, so wiring can happen
// while the loop runs.
func (c *Client) source() Source {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.src
}

func realSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type backoff struct{ cur time.Duration }

func (b *backoff) next() time.Duration {
	switch {
	case b.cur == 0:
		b.cur = minBackoff
	case b.cur < maxBackoff:
		b.cur = min(b.cur*2, maxBackoff)
	}
	return b.cur
}

func (b *backoff) reset() { b.cur = 0 }

// Run joins, reports and listens until ctx ends.
func (c *Client) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); c.beat(ctx) }()
	go func() { defer wg.Done(); c.listen(ctx) }()
	go func() { defer wg.Done(); c.speak(ctx) }()
	wg.Wait()
}

// The wire shapes the unit posts, mirroring crew-control's route
// contracts. Times are the server's receipt times: every report is about
// now, and a late arrival must not claim older wisdom.
type helloBody struct {
	Unit      string `json:"unit"`
	SquadName string `json:"squad_name,omitempty"`
	Role      string `json:"role"`
}

type statusBody struct {
	Unit   string       `json:"unit"`
	HP     float64      `json:"hp"`
	Food   int          `json:"food"`
	Weapon string       `json:"weapon"`
	Pos    squadmsg.Pos `json:"pos"`
}

type calloutBody struct {
	Unit string `json:"unit"`
	Kind string `json:"kind"`
}

// beat keeps the claim and the report fresh. Failure only logs: the next
// tick costs nothing either way, and a hello or a status is not worth a
// retry queue.
func (c *Client) beat(ctx context.Context) {
	// Say hello before the first tick: a unit joining mid-fight claims
	// its seat now, not one helloEvery from now.
	if err := c.post(ctx, "hello", helloBody{Unit: c.unit, SquadName: c.name, Role: string(c.role)}); err != nil {
		c.log.Error("squad_hello_failed", map[string]any{"error": err.Error()})
	}
	hello := time.NewTicker(c.helloEvery)
	defer hello.Stop()
	status := time.NewTicker(c.statusEvery)
	defer status.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hello.C:
			if err := c.post(ctx, "hello", helloBody{Unit: c.unit, SquadName: c.name, Role: string(c.role)}); err != nil {
				c.log.Error("squad_hello_failed", map[string]any{"error": err.Error()})
			}
		case <-status.C:
			src := c.source()
			if src == nil {
				continue
			}
			s := src()
			if err := c.post(ctx, "status", statusBody{Unit: c.unit, HP: s.HP,
				Food: s.Food, Weapon: s.Weapon, Pos: s.Pos}); err != nil {
				c.log.Error("squad_status_failed", map[string]any{"error": err.Error()})
			}
		}
	}
}

// pollAnswer is what one listen reads: the standing order, the leader and
// its last position, and the deltas past the cursor. Rev is the server's
// revision of the leader line; echoing it earns an answer the moment the
// chain of command moves, with no message to carry the news. Order and
// Callout types are the shared ones, whose JSON shapes the server mirrors.
type pollAnswer struct {
	Cursor   int64              `json:"cursor"`
	Rev      int64              `json:"rev"`
	Leader   *wireLeader        `json:"leader,omitempty"`
	Members  []squadmsg.Member  `json:"members"`
	Start    *squadmsg.Pos      `json:"start,omitempty"`
	Order    *squadmsg.Order    `json:"order"`
	Orders   []squadmsg.Order   `json:"orders"`
	Callouts []squadmsg.Callout `json:"callouts"`
}

type wireLeader struct {
	Unit string        `json:"unit"`
	Pos  *squadmsg.Pos `json:"pos,omitempty"`
}

// listen long-polls and writes the state the engine reads. The cursor and
// the leader-line revision are private state of this one loop.
func (c *Client) listen(ctx context.Context) {
	var (
		b      backoff
		up     bool
		cursor int64
		rev    int64
	)
	for {
		if ctx.Err() != nil {
			return
		}
		ans, idle, err := c.poll(ctx, cursor, rev)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			if up {
				c.log.Error("squad_poll_failed", map[string]any{"error": err.Error()})
			} else {
				c.log.Info("squad_poll_down", nil)
			}
			up = false
			if !c.sleep(ctx, b.next()) {
				return
			}
			continue
		}
		b.reset()
		if !up {
			c.log.Info("squad_listening", nil)
			up = true
		}
		if ans != nil {
			c.apply(ans)
			// The answer is the server's whole truth: a restart of
			// crew-control winds the squad's seq back to 0, so the cursor
			// steps down with it rather than polling past every new
			// message for ever.
			cursor = ans.Cursor
			rev = ans.Rev
		}
		// An answer that carried nothing new came back at once; pace the
		// next one, so a squad with no messages costs one read per beat
		// rather than a spinning loop. A 204 runs its own long poll anyway.
		if idle {
			if !c.sleep(ctx, statusEvery) {
				return
			}
		}
	}
}

// apply writes one answer into the state the engine reads.
func (c *Client) apply(ans *pollAnswer) {
	c.mu.Lock()
	switch {
	case ans.Leader != nil && ans.Leader.Pos != nil:
		c.leaderID = ans.Leader.Unit
		c.leaderPos = *ans.Leader.Pos
		c.leadKnown = true
	case ans.Leader != nil:
		// Named but not yet placed: knowing the id alone is honest, the
		// position arrives with the leader's next status.
		c.leaderID = ans.Leader.Unit
		c.leadKnown = false
	default:
		c.leaderID, c.leadKnown = "", false
	}
	if ans.Order != nil {
		c.order = *ans.Order
	}
	// Every answer carries the whole roster, so an absent one is a squad
	// that is gone or a server too old to send it, not a roster to keep.
	c.members = ans.Members
	c.hasStart = ans.Start != nil
	if c.hasStart {
		c.start = *ans.Start
	}
	c.mu.Unlock()
}

func (c *Client) poll(ctx context.Context, cursor, rev int64) (ans *pollAnswer, idle bool, err error) {
	q := url.Values{"wait": {pollWait},
		"since": {fmt.Sprintf("%d", cursor)},
		"rev":   {fmt.Sprintf("%d", rev)}}
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base.JoinPath("v1", "squads", c.squad, "orders").String()+"?"+q.Encode(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer closeBody(resp)
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, true, nil
	case http.StatusOK:
		var a pollAnswer
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxAnswerByte)).Decode(&a); err != nil {
			return nil, false, fmt.Errorf("squad orders: %w", err)
		}
		return &a, a.Cursor == cursor && a.Rev == rev && len(a.Orders) == 0 && len(a.Callouts) == 0, nil
	default:
		return nil, false, fmt.Errorf("squad orders: status %d", resp.StatusCode)
	}
}

// closeBody drains what was not read so the connection is reusable, and
// closes the body after.
func closeBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// Leader names the squad's current leader and where it was last seen. ok
// is false while nobody leads, or while the leader has not yet reported a
// position.
func (c *Client) Leader() (id string, pos mgl32.Vec3, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.leadKnown {
		return "", mgl32.Vec3{}, false
	}
	return c.leaderID, mgl32.Vec3{c.leaderPos.X, c.leaderPos.Y, c.leaderPos.Z}, true
}

// Member is where the warrior in a role last reported from. ok is false
// while it is dead, has been quiet past the server's leader grace, has not
// reported yet, or no unit in the squad claims the role.
func (c *Client) Member(r squadmsg.Role) (mgl32.Vec3, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.members {
		if m.Role == r && m.Alive && m.Fresh && m.Pos != nil {
			return mgl32.Vec3{m.Pos.X, m.Pos.Y, m.Pos.Z}, true
		}
	}
	return mgl32.Vec3{}, false
}

// Down is how many of the unit's squadmates are dead or quiet past the
// leader grace. The unit itself is never counted: if its own reports were
// not getting through it would be the one place that could not know.
func (c *Client) Down() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, m := range c.members {
		if m.Unit != c.unit && (!m.Alive || !m.Fresh) {
			n++
		}
	}
	return n
}

// Start is where the squad began its fight. ok is false until crew-control
// has one.
func (c *Client) Start() (mgl32.Vec3, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasStart {
		return mgl32.Vec3{}, false
	}
	return mgl32.Vec3{c.start.X, c.start.Y, c.start.Z}, true
}

// Orders returns the standing order, zero while none has arrived.
func (c *Client) Orders() squadmsg.Order {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.order
}

// Callout speaks for the engine. The engine names the intent; the client
// signs the from line with its own unit, which is the only identity the
// server will take from it.
func (c *Client) Callout(k squadmsg.Callout) {
	k.From = c.unit
	if k.At.IsZero() {
		k.At = time.Now()
	}
	select {
	case c.callouts <- k:
	default:
		// A full queue is a jam: old call-outs are not worth reading late.
		// The engine re-raises what still matters.
		c.log.Error("squad_callout_dropped", map[string]any{"kind": string(k.Kind)})
	}
}

// speak drains the engine's call-outs, oldest first: one post in flight,
// a backoff when crew-control will not take it, a drop when the answer
// would be the same for ever.
func (c *Client) speak(ctx context.Context) {
	var (
		b    backoff
		pend *squadmsg.Callout
	)
	for {
		var k squadmsg.Callout
		if pend != nil {
			k = *pend
		} else {
			select {
			case <-ctx.Done():
				return
			case k = <-c.callouts:
			}
		}
		if time.Since(k.At) > maxCalloutAge {
			pend = nil
			continue
		}
		err := c.post(ctx, "callout", calloutBody{Unit: c.unit, Kind: string(k.Kind)})
		switch {
		case err == nil:
			pend = nil
			b.reset()
		case rejectedErr(err):
			// A kind the server does not know will be refused for ever;
			// sending it again is noise.
			c.log.Error("squad_callout_rejected", map[string]any{"kind": string(k.Kind)})
			pend = nil
		default:
			pend = &k
			if !c.sleep(ctx, b.next()) {
				return
			}
		}
	}
}

// post sends one value and treats any 4xx as a rejection the caller can
// read from the error.
func (c *Client) post(ctx context.Context, leaf string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, postTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base.JoinPath("v1", "squads", c.squad, leaf).String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxPostBytes))
	switch n := resp.StatusCode; n / 100 {
	case 2:
		return nil
	case 4:
		return rejected(n)
	default:
		return fmt.Errorf("squad %s: status %d", leaf, n)
	}
}

// rejected is a 4xx the server will answer the same way every time.
type rejected int

func (r rejected) Error() string { return fmt.Sprintf("squad: rejected %d", int(r)) }

func rejectedErr(err error) bool {
	var r rejected
	return errors.As(err, &r)
}
