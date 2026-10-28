// Package telemetry polls each unit's status and fans the result out to the
// console as a snapshot and a change-driven event stream.
package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

const (
	offlineAfter = 2
	// A unit's state is small; the cap keeps a misbehaving upstream from
	// filling memory on a 50m-CPU pod.
	maxBodyBytes = 1 << 20
)

// Status is what the console shows per unit. Position is left out on purpose:
// the public view carries no coordinates.
type Status struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Role is the unit's claimed job as its own state reported it, one of
	// roles; empty when it has none or reported something else.
	Role   string       `json:"role,omitempty"`
	Online bool         `json:"online"`
	Health *float32     `json:"health"`
	Hunger *float32     `json:"hunger"`
	Dead   bool         `json:"dead"`
	Task   *TaskSummary `json:"task"`
	Seen   time.Time    `json:"seen"`
}

type TaskSummary struct {
	ID       string `json:"id"`
	Template string `json:"template"`
	State    string `json:"state"`
	Item     string `json:"item"`
	Qty      int    `json:"qty"`
	Progress int    `json:"progress"`
}

// LiveItem is one non-empty slot of the unit's own inventory, as its state
// reported it.
type LiveItem struct {
	Slot  int    `json:"slot"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// LiveEffect is one status effect on the unit, as its state reported it.
// Duration is in ticks and negative means it does not expire.
type LiveEffect struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Amplifier int    `json:"amplifier"`
	Duration  int    `json:"duration"`
}

// Live is what a unit's own /v1/state said at the last poll. It answers the
// operator-only detail view; unlike Status it is never broadcast, because
// it carries what the unit holds.
type Live struct {
	ID     string
	Online bool
	Dead   bool
	// Health and Hunger are nil until the unit reported them.
	Health, Hunger *float32
	Inventory      []LiveItem
	Effects        []LiveEffect
	// PlayerID is the id the unit reports for its player, which is the one
	// its record in the world save is filed under; empty until it has one.
	PlayerID string
	// At is when the poll that filled this succeeded.
	At time.Time
}

// roles is the closed set a unit may report. The state body is the unit's own
// word, and Status goes to every viewer of the public console, so anything
// outside the set is dropped rather than shown.
var roles = map[string]bool{"vanguard": true, "striker": true, "ranger": true, "mender": true, "builder": true}

// Only the fields the summary needs; crew's own types are internal to crew.
type stateResponse struct {
	Unit struct {
		Role string `json:"role"`
	} `json:"unit"`
	State struct {
		Dead        bool    `json:"dead"`
		Health      float32 `json:"health"`
		HealthKnown bool    `json:"health_known"`
		Hunger      float32 `json:"hunger"`
		HungerKnown bool    `json:"hunger_known"`

		Position      [3]float32 `json:"position"`
		PositionKnown bool       `json:"position_known"`
		Yaw           float32    `json:"yaw"`
		Dimension     int32      `json:"dimension"`

		Inventory []LiveItem   `json:"inventory"`
		Effects   []LiveEffect `json:"effects"`
		UniqueID  string       `json:"unique_id"`
	} `json:"state"`
	Tasks struct {
		Current *TaskSummary `json:"current"`
		Recent  []recentTask `json:"recent"`
	} `json:"tasks"`
}

type Poller struct {
	units   []roster.Unit
	client  *http.Client
	every   time.Duration
	timeout time.Duration
	// Interval of the full-snapshot refresh on an otherwise quiet stream.
	keepalive time.Duration

	mu       sync.Mutex
	statuses []Status
	failures []int
	subs     map[chan []Status]struct{}
	streams  int
	workSrc  WorkSource
	wsubs    map[*workSub]struct{}
	// Per-unit shared bytes of the work feed; see feedCache for why.
	workCaches map[string]*feedCache

	tracks    []track
	feed      []Event
	seq       uint64
	esubs     map[chan Event]struct{}
	positions []Position
	// born is the events of the tick in progress, until OnEvents takes them.
	born []Born
	// OnEvents, when set, receives the events born in one tick with each
	// unit's position at that moment, after the tick's state is published.
	// It runs on the poll loop, so it must not block.
	OnEvents func([]Born)
	// OnPoll, when set, receives every unit's state once per tick, after the
	// tick's events. Like OnEvents it runs on the poll loop and must not block.
	OnPoll  func(time.Time, []UnitState)
	lives   []Live
	jobsSrc JobsSource
	// Shared bytes of the jobs feed; see feedCache for why.
	jobsCache feedCache
	jsubs     map[chan struct{}]struct{}
}

func NewPoller(r roster.Roster, client *http.Client, every time.Duration) *Poller {
	units := r.Units()
	p := &Poller{
		units:      units,
		client:     client,
		every:      every,
		timeout:    every * 8 / 10,
		keepalive:  defaultKeepalive,
		statuses:   make([]Status, len(units)),
		failures:   make([]int, len(units)),
		subs:       map[chan []Status]struct{}{},
		wsubs:      map[*workSub]struct{}{},
		tracks:     make([]track, len(units)),
		esubs:      map[chan Event]struct{}{},
		positions:  make([]Position, len(units)),
		lives:      make([]Live, len(units)),
		jsubs:      map[chan struct{}]struct{}{},
		workCaches: map[string]*feedCache{},
	}
	for i, u := range units {
		p.positions[i] = Position{ID: u.ID}
		p.statuses[i] = Status{ID: u.ID, Name: u.Name}
		p.lives[i] = Live{ID: u.ID}
		p.workCaches[u.ID] = &feedCache{}
	}
	return p
}

// Run polls until ctx ends. Each tick waits for its polls, and every poll is
// bounded by the request timeout, so ticks never pile up.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		p.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type result struct {
	st  stateResponse
	err error
}

func (p *Poller) tick(ctx context.Context) {
	results := make([]result, len(p.units))
	var wg sync.WaitGroup
	for i := range p.units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].st, results[i].err = p.fetch(ctx, p.units[i])
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	now := time.Now()

	p.mu.Lock()
	changed := false
	for i, res := range results {
		next := p.statuses[i]
		if res.err == nil {
			p.failures[i] = 0
			next.Online = true
			next.Seen = now
			next.Role = ""
			if roles[res.st.Unit.Role] {
				next.Role = res.st.Unit.Role
			}
			next.Dead = res.st.State.Dead
			next.Health = nil
			if res.st.State.HealthKnown {
				h := res.st.State.Health
				next.Health = &h
			}
			next.Hunger = nil
			if res.st.State.HungerKnown {
				h := res.st.State.Hunger
				next.Hunger = &h
			}
			next.Task = res.st.Tasks.Current
			s := res.st.State
			p.positions[i] = Position{ID: p.units[i].ID, X: s.Position[0], Y: s.Position[1], Z: s.Position[2], Yaw: s.Yaw, Dim: s.Dimension, Known: s.PositionKnown}

			p.lives[i] = Live{
				ID: p.units[i].ID, Online: true, Dead: s.Dead, At: now,
				Health: next.Health, Hunger: next.Hunger,
				Inventory: s.Inventory, Effects: s.Effects, PlayerID: s.UniqueID,
			}
		} else {
			p.failures[i]++
			if p.failures[i] >= offlineAfter {
				// Stale health or task on an offline unit would read as live.
				next.Online, next.Health, next.Hunger, next.Task, next.Dead = false, nil, nil, nil, false
				p.positions[i] = Position{ID: p.units[i].ID}
				p.lives[i] = Live{ID: p.units[i].ID}
			}
		}
		// A single failed poll leaves the unit online, so it is not an event.
		if res.err == nil || !next.Online {
			p.observeLocked(i, next, res, now)
		}
		if !sameShown(p.statuses[i], next) {
			changed = true
		}
		p.statuses[i] = next
	}
	if changed {
		p.publishLocked()
	}
	// History is handed over off the lock: the sink may take longer than a
	// poll is allowed to.
	born := p.born
	p.born = nil
	states := p.statesLocked()
	p.mu.Unlock()
	if p.OnEvents != nil && len(born) > 0 {
		p.OnEvents(born)
	}
	if p.OnPoll != nil {
		p.OnPoll(now, states)
	}
}

// Seen moves every tick, so it is left out of the comparison or every poll
// would count as a change.
func sameShown(a, b Status) bool {
	if a.Online != b.Online || a.Dead != b.Dead || a.Role != b.Role {
		return false
	}
	if (a.Hunger == nil) != (b.Hunger == nil) || (a.Hunger != nil && *a.Hunger != *b.Hunger) {
		return false
	}
	if (a.Health == nil) != (b.Health == nil) || (a.Health != nil && *a.Health != *b.Health) {
		return false
	}
	if (a.Task == nil) != (b.Task == nil) || (a.Task != nil && *a.Task != *b.Task) {
		return false
	}
	return true
}

func (p *Poller) fetch(ctx context.Context, u roster.Unit) (stateResponse, error) {
	var out stateResponse
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.URL.JoinPath("v1/state").String(), nil)
	if err != nil {
		return out, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		return out, fmt.Errorf("status %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&out)
	return out, err
}

// Snapshot returns the statuses in roster order.
func (p *Poller) Snapshot() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

// Live returns the unit's own state as its last successful poll read it.
// It is for the operator-only views: unlike Snapshot it carries what the
// unit holds, so it is never fanned out to the public stream.
func (p *Poller) Live(id string) (Live, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, u := range p.units {
		if u.ID == id {
			return p.lives[i], true
		}
	}
	return Live{}, false
}

func (p *Poller) snapshotLocked() []Status {
	return append([]Status(nil), p.statuses...)
}

// Subscribe delivers a snapshot whenever something shown changed. The buffer
// holds one value and a slow reader loses stale ones: only the latest matters.
func (p *Poller) Subscribe() (<-chan []Status, func()) {
	ch := make(chan []Status, 1)
	p.mu.Lock()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()
	return ch, func() {
		p.mu.Lock()
		delete(p.subs, ch)
		p.mu.Unlock()
	}
}

func (p *Poller) publishLocked() {
	snap := p.snapshotLocked()
	for ch := range p.subs {
		select {
		case <-ch:
		default:
		}
		// Sends happen only under p.mu, so the slot just freed stays free.
		ch <- snap
	}
}

func (p *Poller) observeLocked(i int, st Status, res result, now time.Time) {
	o := observation{online: st.Online, dead: st.Dead, health: st.Health, hunger: st.Hunger}
	if c := st.Task; c != nil {
		o.curID, o.curTemplate, o.curProgress = c.ID, c.Template, c.Progress
	}
	if pos := p.positions[i]; pos.Known {
		o.pos, o.posKnown = [3]float32{pos.X, pos.Y, pos.Z}, true
	}
	o.recent = res.st.Tasks.Recent
	for _, e := range p.tracks[i].observe(o, now) {
		p.born = append(p.born, Born{Event: p.appendEventLocked(i, e), Pos: p.positions[i]})
	}
}
