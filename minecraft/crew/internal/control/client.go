// Package control connects a unit to crew-control: it long-polls for work and
// reports what happens to the tasks that work started.
package control

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
	"unicode/utf8"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
)

const (
	pollWait = "25s"
	// Longer than pollWait so a healthy long poll is never cut short, short
	// enough that a half-open connection does not hang the loop for good.
	pollTimeout = 40 * time.Second
	postTimeout = 10 * time.Second

	minBackoff = time.Second
	maxBackoff = 30 * time.Second

	// A running task is re-reported this often so the controller can tell a
	// working unit from one that restarted and lost the task; the controller
	// re-hands a running item after 30 s of silence.
	heartbeatEvery = 15 * time.Second

	// Keeps a report under the controller's 1 KiB body cap with room for the
	// rest of the JSON.
	maxReasonBytes = 400

	// How many handled stop ids are remembered to recognise a redelivery.
	stopMemory = 64

	// Far above a real item (a template is about 100 bytes).
	maxWorkBytes = 64 << 10
	queueWait    = 50 * time.Millisecond
)

// Runner is the part of task.Runner the client drives.
type Runner interface {
	SubmitAs(id string, t task.Template) (task.Task, error)
	Cancel(id string) (task.Task, error)
	List() []task.Task
	Observe(f func(task.Task))
}

type Logger interface {
	Info(event string, fields map[string]any)
	Error(event string, fields map[string]any)
}

// event is one status report. The wire has no queued state: a task is
// unknown to the controller's view until it runs.
type event struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Progress int    `json:"progress"`
	Reason   string `json:"reason,omitempty"`
}

type workItem struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Template json.RawMessage `json:"template"`
	Command  *struct {
		Name string `json:"name"`
	} `json:"command"`
}

type Client struct {
	base   *url.URL
	unit   string
	token  string
	runner Runner
	log    Logger
	http   *http.Client
	// sleep reports false when ctx ended first; tests replace it.
	sleep func(ctx context.Context, d time.Duration) bool
	// heartbeat is the period of the running-task refresh; tests shorten it.
	heartbeat time.Duration

	mu sync.Mutex
	// owned is the tasks control submitted and has not yet seen finish; tasks
	// posted through the local API are not its to report.
	owned   map[string]struct{}
	pending map[string]event // latest unsent event per id
	order   []string         // ids in the order they first became pending
	wake    chan struct{}
	// stopped is the last stopMemory handled stop ids, oldest first: a
	// redelivered stop must not cancel work queued after the first one.
	stopped []string
}

func New(base *url.URL, unit, token string, r Runner, log Logger) *Client {
	c := &Client{
		base: base, unit: unit, token: token, runner: r, log: log,
		http:  &http.Client{},
		sleep: realSleep,

		heartbeat: heartbeatEvery,
		owned:     map[string]struct{}{},
		pending:   map[string]event{},
		wake:      make(chan struct{}, 1),
	}
	r.Observe(c.observe)
	return c
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

// Run polls until ctx ends.
func (c *Client) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.sendEvents(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.beat(ctx)
	}()
	defer wg.Wait()

	var (
		b    backoff
		down bool
		seen bool
		held *workItem
	)
	for ctx.Err() == nil {
		if held != nil {
			if !c.submit(*held) {
				if !c.sleep(ctx, queueWait) {
					return
				}
				continue
			}
			held = nil
		}
		queued := 0
		for _, t := range c.runner.List() {
			if t.State == task.Queued {
				queued++
			}
		}
		if queued >= task.MaxQueued {
			if !c.sleep(ctx, queueWait) {
				return
			}
			continue
		}
		item, err := c.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !down {
				down = true
				c.log.Error("control_unreachable", map[string]any{"error": err.Error()})
			}
			if !c.sleep(ctx, b.next()) {
				return
			}
			continue
		}
		b.reset()
		if down || !seen {
			c.log.Info("control_connected", nil)
		}
		down, seen = false, true
		if item != nil {
			if !c.handle(*item) {
				held = item
			}
		}
	}
}

func (c *Client) endpoint(leaf string) string {
	return c.base.JoinPath("v1", "units", c.unit, leaf).String()
}

func (c *Client) poll(ctx context.Context) (*workItem, error) {
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("work")+"?wait="+pollWait, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		// Reading to the end lets the connection be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var item workItem
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxWorkBytes)).Decode(&item); err != nil {
			return nil, fmt.Errorf("decode work: %w", err)
		}
		return &item, nil
	}
	return nil, fmt.Errorf("work: status %d", resp.StatusCode)
}

func (c *Client) handle(item workItem) bool {
	switch {
	case item.Kind == "task":
		return c.submit(item)
	case item.Kind == "command" && item.Command != nil && item.Command.Name == "stop":
		c.stop(item.ID)
	default:
		c.enqueue(event{ID: item.ID, State: task.Failed, Reason: "unknown work"})
	}
	return true
}

func (c *Client) submit(item workItem) bool {
	tpl, err := task.ParseTemplate(item.Template)
	if err != nil {
		c.enqueue(event{ID: item.ID, State: task.Failed, Reason: err.Error()})
		return true
	}
	// Owned before the submit: the runner reports the queued task from inside
	// SubmitAs, and the observer must already know the id then.
	c.mu.Lock()
	_, had := c.owned[item.ID]
	c.owned[item.ID] = struct{}{}
	c.mu.Unlock()

	_, err = c.runner.SubmitAs(item.ID, tpl)
	switch {
	case err == nil:
	case errors.Is(err, task.ErrDuplicate):
		if !had {
			c.disown(item.ID)
		}
		// The controller handed it again because it has not heard from the
		// task lately; saying where it is now ends the redelivery.
		for _, t := range c.runner.List() {
			if t.ID == item.ID && t.State != task.Queued {
				c.enqueue(eventOf(t))
			}
		}
		return true
	case errors.Is(err, task.ErrQueueFull):
		c.disown(item.ID)
		return false
	default:
		c.disown(item.ID)
		c.enqueue(event{ID: item.ID, State: task.Failed, Reason: err.Error()})
		return true
	}
	return true
}

func (c *Client) disown(id string) {
	c.mu.Lock()
	delete(c.owned, id)
	c.mu.Unlock()
}

// stop cancels everything, not only what control sent: the controller asked
// the unit to stand down.
func (c *Client) stop(commandID string) {
	if c.seenStop(commandID) {
		c.enqueue(event{ID: commandID, State: task.Done})
		return
	}
	for _, t := range c.runner.List() {
		if t.State == task.Queued || t.State == task.Running {
			// A task that finished since List is already as stopped as it gets.
			_, _ = c.runner.Cancel(t.ID)
		}
	}
	c.enqueue(event{ID: commandID, State: task.Done})
}

// seenStop records id and reports whether it was already recorded.
func (c *Client) seenStop(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.stopped {
		if s == id {
			return true
		}
	}
	c.stopped = append(c.stopped, id)
	if len(c.stopped) > stopMemory {
		c.stopped = c.stopped[1:]
	}
	return false
}

func eventOf(t task.Task) event {
	return event{ID: t.ID, State: t.State, Progress: t.Progress, Reason: t.Reason}
}

// beat re-reports every task control owns that is still running.
func (c *Client) beat(ctx context.Context) {
	tick := time.NewTicker(c.heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		running := c.runner.List()
		c.mu.Lock()
		var fresh []event
		for _, t := range running {
			if t.State != task.Running {
				continue
			}
			// A pending event is at least as new as this snapshot.
			if _, owned := c.owned[t.ID]; !owned {
				continue
			}
			if _, queued := c.pending[t.ID]; queued {
				continue
			}
			fresh = append(fresh, eventOf(t))
		}
		c.mu.Unlock()
		for _, ev := range fresh {
			c.enqueue(ev)
		}
	}
}

// observe runs on the runner's goroutines and must never wait on the network.
func (c *Client) observe(t task.Task) {
	if t.State == task.Queued {
		return
	}
	c.mu.Lock()
	if _, ok := c.owned[t.ID]; !ok {
		c.mu.Unlock()
		return
	}
	if t.State != task.Running {
		delete(c.owned, t.ID)
	}
	c.mu.Unlock()
	c.enqueue(eventOf(t))
}

// enqueue keeps only the latest event per id: a controller needs where a task
// is now, and an outage must not build a backlog.
func (c *Client) enqueue(ev event) {
	ev.Reason = clipReason(ev.Reason)
	c.mu.Lock()
	if _, ok := c.pending[ev.ID]; !ok {
		c.order = append(c.order, ev.ID)
	}
	c.pending[ev.ID] = ev
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// clipReason cuts at a rune boundary so the report stays valid UTF-8.
func clipReason(s string) string {
	if len(s) <= maxReasonBytes {
		return s
	}
	cut := maxReasonBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func (c *Client) sendEvents(ctx context.Context) {
	var b backoff
	for {
		if c.drain(ctx) {
			b.reset()
			select {
			case <-ctx.Done():
				return
			case <-c.wake:
			}
			continue
		}
		if !c.sleep(ctx, b.next()) {
			return
		}
	}
}

// drain posts pending events oldest first and reports whether it emptied the
// queue.
func (c *Client) drain(ctx context.Context) bool {
	for {
		c.mu.Lock()
		if len(c.order) == 0 {
			c.mu.Unlock()
			return true
		}
		ev := c.pending[c.order[0]]
		c.mu.Unlock()

		if err := c.post(ctx, ev); err != nil {
			var rej rejected
			if !errors.As(err, &rej) {
				return false
			}
			// The controller will never take this event; keeping it would
			// block every report behind it.
			c.log.Error("control_event_rejected", map[string]any{"id": ev.ID, "status": int(rej)})
		}

		c.mu.Lock()
		// A newer event for the same id stays queued and goes next.
		if c.pending[ev.ID] == ev {
			delete(c.pending, ev.ID)
			c.order = c.order[1:]
		}
		c.mu.Unlock()
	}
}

// rejected is a 4xx the controller will answer the same way every time.
type rejected int

func (r rejected) Error() string { return fmt.Sprintf("events: status %d", int(r)) }

func (c *Client) post(ctx context.Context, ev event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, postTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("events"), bytes.NewReader(body))
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
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode/100 == 2:
	case resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusUnauthorized &&
		resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests:
		return rejected(resp.StatusCode)
	default:
		return fmt.Errorf("events: status %d", resp.StatusCode)
	}
	return nil
}
