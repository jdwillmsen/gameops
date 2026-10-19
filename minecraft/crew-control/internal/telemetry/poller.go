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
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Online bool         `json:"online"`
	Health *float32     `json:"health"`
	Dead   bool         `json:"dead"`
	Task   *TaskSummary `json:"task"`
	Seen   time.Time    `json:"seen"`
}

type TaskSummary struct {
	Template string `json:"template"`
	State    string `json:"state"`
	Item     string `json:"item"`
	Qty      int    `json:"qty"`
	Progress int    `json:"progress"`
}

// Only the fields the summary needs; crew's own types are internal to crew.
type stateResponse struct {
	State struct {
		Dead        bool    `json:"dead"`
		Health      float32 `json:"health"`
		HealthKnown bool    `json:"health_known"`
	} `json:"state"`
	Tasks struct {
		Current *TaskSummary `json:"current"`
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
}

func NewPoller(r roster.Roster, client *http.Client, every time.Duration) *Poller {
	units := r.Units()
	p := &Poller{
		units:     units,
		client:    client,
		every:     every,
		timeout:   every * 8 / 10,
		keepalive: defaultKeepalive,
		statuses:  make([]Status, len(units)),
		failures:  make([]int, len(units)),
		subs:      map[chan []Status]struct{}{},
	}
	for i, u := range units {
		p.statuses[i] = Status{ID: u.ID, Name: u.Name}
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
	defer p.mu.Unlock()
	changed := false
	for i, res := range results {
		next := p.statuses[i]
		if res.err == nil {
			p.failures[i] = 0
			next.Online = true
			next.Seen = now
			next.Dead = res.st.State.Dead
			next.Health = nil
			if res.st.State.HealthKnown {
				h := res.st.State.Health
				next.Health = &h
			}
			next.Task = res.st.Tasks.Current
		} else {
			p.failures[i]++
			if p.failures[i] >= offlineAfter {
				// Stale health or task on an offline unit would read as live.
				next.Online, next.Health, next.Task, next.Dead = false, nil, nil, false
			}
		}
		if !sameShown(p.statuses[i], next) {
			changed = true
		}
		p.statuses[i] = next
	}
	if changed {
		p.publishLocked()
	}
}

// Seen moves every tick, so it is left out of the comparison or every poll
// would count as a change.
func sameShown(a, b Status) bool {
	if a.Online != b.Online || a.Dead != b.Dead {
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
