package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"

	// MaxQueued bounds waiting tasks so a caller with the token cannot grow
	// memory without limit while the unit is away.
	MaxQueued = 16
	// MaxFinished is how much history List keeps.
	MaxFinished = 50

	tickEvery = 50 * time.Millisecond
)

var (
	ErrNotFound  = errors.New("no such task")
	ErrFinished  = errors.New("task already finished")
	ErrQueueFull = errors.New("task queue is full")
)

// Task is the public record. It deliberately omits the request, which holds
// lab coordinates.
type Task struct {
	ID       string `json:"id"`
	Template string `json:"template"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Skill    string `json:"skill,omitempty"`
	// Item and Qty are what the task gathers; Progress counts toward Qty. A
	// haul has no single item: its Qty is the total to move.
	Item     string    `json:"item,omitempty"`
	Qty      int       `json:"qty,omitempty"`
	Progress int       `json:"progress"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

// Reflex is what the unit does on its own: fight or flee when threatened, and
// keep itself ticking and alive-looking when it has no task. The runner calls
// every method from the goroutine that steps it.
type Reflex interface {
	// Engaged advances the reflex's clock by one tick and says whether the
	// unit must fight or flee this tick instead of working or idling.
	Engaged(act.Pose, belief.Snapshot) bool
	// Tending says whether the unit has something to see to, such as a meal or
	// a chest to rearm from, before its task or its idling. It is asked only
	// when the unit is not engaged, and working says a task is running, which
	// some errands never interrupt.
	Tending(s belief.Snapshot, working bool) bool
	// Fight, Tend and Idle each send exactly one input.
	Fight(context.Context, skill.Actuator, belief.Snapshot) error
	Tend(context.Context, skill.Actuator, belief.Snapshot) error
	Idle(context.Context, skill.Actuator) error
	// Cancel drops an idle walk because a task is about to start.
	Cancel()
	// Reset forgets everything about the last fight or walk.
	Reset()
}

type entry struct {
	Task
	item    string
	skills  []skill.Skill
	idx     int
	started time.Time
	ready   bool
	// fought is the time the reflex had the unit, in a fight or at a meal,
	// which the timeout does not count.
	fought time.Duration
	// resume is set when the reflex interrupted the skill, so it picks up
	// where it left off instead of starting over.
	resume bool
}

type Runner struct {
	newSkills func(Template) ([]skill.Skill, error)
	self      func() belief.Snapshot
	timeout   time.Duration
	log       skill.Logger
	now       func() time.Time

	mu      sync.Mutex
	act     skill.Actuator
	entries []*entry // creation order
	cur     *entry
	// pending is the skill of a task cancelled or timed out mid-run. Its
	// cleanup sends inputs, so it runs on the stepping goroutine, once, before
	// anything else uses the actuator.
	pending skill.Stopper
	reflex  Reflex
	// resetReflex defers Reset to the stepping goroutine, which owns the
	// reflex's state; the read loop only raises the flag.
	resetReflex bool
	prevStep    time.Time
}

func NewRunner(newSkills func(Template) ([]skill.Skill, error), a skill.Actuator, self func() belief.Snapshot, timeout time.Duration, log skill.Logger) *Runner {
	return &Runner{newSkills: newSkills, act: a, self: self, timeout: timeout, log: log, now: time.Now}
}

// SetActuator hands the runner a new session's actuator.
func (r *Runner) SetActuator(a skill.Actuator) {
	r.mu.Lock()
	r.act = a
	r.mu.Unlock()
}

// SetReflex gives the runner the unit's own behaviour. Without one a unit with
// no task only sends an input-less tick.
func (r *Runner) SetReflex(x Reflex) {
	r.mu.Lock()
	r.reflex = x
	r.mu.Unlock()
}

func (r *Runner) Submit(t Template) (Task, error) {
	skills, err := r.newSkills(t)
	if err != nil {
		return Task{}, err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Task{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	queued := 0
	for _, e := range r.entries {
		if e.State == Queued {
			queued++
		}
	}
	if queued >= MaxQueued {
		return Task{}, ErrQueueFull
	}
	now := r.now()
	e := &entry{Task: Task{ID: hex.EncodeToString(b[:]), Template: t.Template, State: Queued, Item: t.Item, Qty: t.Qty, Created: now, Updated: now}, item: t.Item, skills: skills}
	r.entries = append(r.entries, e)
	r.logEvent("task_submitted", e)
	return e.Task, nil
}

func (r *Runner) Cancel(id string) (Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.find(id)
	if e == nil {
		return Task{}, ErrNotFound
	}
	if e.State != Queued && e.State != Running {
		return Task{}, ErrFinished
	}
	r.abandon(e, Cancelled, "")
	return e.Task, nil
}

func (r *Runner) List() []Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Task, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.Task
	}
	return out
}

// SessionEnded fails the running task: the new session starts from a world
// the old task never saw, so resuming it would act on stale assumptions.
func (r *Runner) SessionEnded() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.act = nil
	r.pending = nil // the old actuator is gone
	r.resetReflex = true
	r.failCurrent("session ended")
}

func (r *Runner) Died() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = nil // a dead unit has no chest open and no break running
	r.resetReflex = true
	r.failCurrent("unit died")
}

func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.step(ctx)
		}
	}
}

// preempt takes the running task's skill off the unit for the reflex, and
// returns it to stop when it has started. The time away is not the task's. It
// must be called with mu held.
func (r *Runner) preempt(dt time.Duration) skill.Stopper {
	e := r.cur
	if e == nil {
		return nil
	}
	e.fought += dt
	if !e.ready {
		return nil
	}
	e.ready, e.resume = false, true
	stop, _ := e.skills[e.idx].(skill.Stopper)
	return stop
}

// step does one tick of work and makes sure the unit sent exactly one input
// for it. Bedrock only advances a player on client input, so a unit that sent
// nothing would hang in mid-air; one that sent two would run ahead of the
// server.
func (r *Runner) step(ctx context.Context) {
	a, before := r.work(ctx)
	if a == nil || a.Ticks() != before {
		return
	}
	p := a.Pose()
	// A failed write means the session is ending, which the read loop reports.
	_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch})
}

// work does the task bookkeeping and the skill's step. It returns the
// actuator it may have ticked and the tick count before it did, or nil when
// the unit is not in a state to send inputs. The skill's Step runs outside
// the lock because it talks to the server; a cancel that lands meanwhile is
// honoured by dropping the result.
func (r *Runner) work(ctx context.Context) (skill.Actuator, uint64) {
	r.mu.Lock()
	if r.resetReflex {
		r.resetReflex = false
		if r.reflex != nil {
			r.reflex.Reset()
		}
	}
	snap := r.self()
	if !snap.Spawned || snap.Dead || r.act == nil {
		r.pending = nil
		r.mu.Unlock()
		return nil, 0
	}
	a := r.act
	before := a.Ticks()
	now := r.now()
	dt := now.Sub(r.prevStep)
	r.prevStep = now
	if dt <= 0 || dt > time.Second {
		dt = tickEvery
	}
	if st := r.pending; st != nil {
		r.pending = nil
		r.mu.Unlock()
		st.Stop(a)
		return a, before
	}
	if x := r.reflex; x != nil && x.Engaged(a.Pose(), snap) {
		stop := r.preempt(dt)
		r.mu.Unlock()
		if stop != nil {
			// Stop may send the tick's one input; the fight begins next tick.
			stop.Stop(a)
			return a, before
		}
		// A write error surfaces on the read loop, which ends the session.
		_ = x.Fight(ctx, a, snap)
		return a, before
	}
	if x := r.reflex; x != nil && x.Tending(snap, r.cur != nil) {
		stop := r.preempt(dt)
		r.mu.Unlock()
		if stop != nil {
			stop.Stop(a)
			return a, before
		}
		_ = x.Tend(ctx, a, snap)
		return a, before
	}
	if r.cur == nil {
		r.cur = r.nextQueued()
		if r.cur == nil {
			x := r.reflex
			r.mu.Unlock()
			if x != nil {
				_ = x.Idle(ctx, a)
			}
			return a, before
		}
		if r.reflex != nil {
			r.reflex.Cancel()
		}
		r.cur.State = Running
		r.cur.started = r.now()
		r.cur.Updated = r.cur.started
		r.cur.Skill = r.cur.skills[0].Name()
		r.logEvent("task_started", r.cur)
	}
	e := r.cur
	if r.now().Sub(e.started)-e.fought > r.timeout {
		r.abandon(e, Failed, "timed out")
		r.mu.Unlock()
		return a, before
	}
	sk := e.skills[e.idx]
	needReady := !e.ready
	resume := e.resume
	r.mu.Unlock()

	var (
		st  skill.Status
		err error
	)
	if rs, ok := sk.(skill.Resumer); needReady && resume && ok {
		rs.Resume()
		st, err = sk.Step(ctx, a)
	} else if needReady {
		err = sk.Ready(snap)
		st = skill.Failed
		if err == nil {
			st = skill.Running
			st, err = sk.Step(ctx, a)
		}
	} else {
		st, err = sk.Step(ctx, a)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != e {
		return a, before
	}
	e.ready, e.resume = true, false
	if p, ok := sk.(skill.Progresser); ok {
		e.Progress = p.Progress()
	} else {
		e.Progress = have(r.self(), e.item)
	}
	e.Updated = r.now()
	switch st {
	case skill.Failed:
		reason := "skill failed"
		if err != nil {
			reason = err.Error()
		}
		r.finish(e, Failed, reason)
	case skill.Done:
		e.idx++
		e.ready = false
		if e.idx == len(e.skills) {
			r.finish(e, Done, "")
			return a, before
		}
		e.Skill = e.skills[e.idx].Name()
	}
	return a, before
}

func have(s belief.Snapshot, item string) int {
	n := 0
	for _, it := range s.Inventory {
		if it.Name == item {
			n += it.Count
		}
	}
	return n
}

func (r *Runner) find(id string) *entry {
	for _, e := range r.entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (r *Runner) nextQueued() *entry {
	for _, e := range r.entries {
		if e.State == Queued {
			return e
		}
	}
	return nil
}

// abandon ends a task that may be mid-skill by the caller's choice, leaving
// its cleanup to the stepping goroutine.
func (r *Runner) abandon(e *entry, state, reason string) {
	if r.cur == e && e.idx < len(e.skills) {
		if st, ok := e.skills[e.idx].(skill.Stopper); ok {
			r.pending = st
		}
	}
	r.finish(e, state, reason)
}

func (r *Runner) failCurrent(reason string) {
	if r.cur != nil {
		r.finish(r.cur, Failed, reason)
	}
}

// finish must be called with mu held.
func (r *Runner) finish(e *entry, state, reason string) {
	e.State, e.Reason, e.Updated = state, reason, r.now()
	e.skills = nil
	if r.cur == e {
		r.cur = nil
	}
	r.logEvent("task_finished", e)
	finished := 0
	for _, x := range r.entries {
		if x.State != Queued && x.State != Running {
			finished++
		}
	}
	for i := 0; finished > MaxFinished && i < len(r.entries); {
		if s := r.entries[i].State; s != Queued && s != Running {
			r.entries = append(r.entries[:i], r.entries[i+1:]...)
			finished--
			continue
		}
		i++
	}
}

// logEvent writes only identifiers and state: the request holds coordinates.
func (r *Runner) logEvent(event string, e *entry) {
	f := map[string]any{"id": e.ID, "template": e.Template, "state": e.State}
	if e.Reason != "" {
		f["reason"] = e.Reason
	}
	r.log.Info(event, f)
}
