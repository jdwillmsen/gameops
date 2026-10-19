package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

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
	ID       string    `json:"id"`
	Template string    `json:"template"`
	State    string    `json:"state"`
	Reason   string    `json:"reason,omitempty"`
	Skill    string    `json:"skill,omitempty"`
	Progress int       `json:"progress"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

type entry struct {
	Task
	item    string
	skills  []skill.Skill
	idx     int
	started time.Time
	ready   bool
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
}

func NewRunner(newSkills func(Template) ([]skill.Skill, error), act skill.Actuator, self func() belief.Snapshot, timeout time.Duration, log skill.Logger) *Runner {
	return &Runner{newSkills: newSkills, act: act, self: self, timeout: timeout, log: log, now: time.Now}
}

// SetActuator hands the runner a new session's actuator.
func (r *Runner) SetActuator(a skill.Actuator) {
	r.mu.Lock()
	r.act = a
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
	e := &entry{Task: Task{ID: hex.EncodeToString(b[:]), Template: t.Template, State: Queued, Created: now, Updated: now}, item: t.Item, skills: skills}
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
	r.failCurrent("session ended")
}

func (r *Runner) Died() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = nil // a dead unit has no chest open and no break running
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

// step does one tick of work. The skill's Step runs outside the lock because
// it talks to the server; a cancel that lands meanwhile is honoured by
// dropping the result.
func (r *Runner) step(ctx context.Context) {
	r.mu.Lock()
	snap := r.self()
	if !snap.Spawned || snap.Dead || r.act == nil {
		r.pending = nil
		r.mu.Unlock()
		return
	}
	if st := r.pending; st != nil {
		r.pending = nil
		act := r.act
		r.mu.Unlock()
		st.Stop(act)
		return
	}
	if r.cur == nil {
		r.cur = r.nextQueued()
		if r.cur == nil {
			r.mu.Unlock()
			return
		}
		r.cur.State = Running
		r.cur.started = r.now()
		r.cur.Updated = r.cur.started
		r.cur.Skill = r.cur.skills[0].Name()
		r.logEvent("task_started", r.cur)
	}
	e, act := r.cur, r.act
	if r.now().Sub(e.started) > r.timeout {
		r.abandon(e, Failed, "timed out")
		r.mu.Unlock()
		return
	}
	sk := e.skills[e.idx]
	needReady := !e.ready
	r.mu.Unlock()

	var (
		st  skill.Status
		err error
	)
	if needReady {
		err = sk.Ready(snap)
		st = skill.Failed
		if err == nil {
			st = skill.Running
			st, err = sk.Step(ctx, act)
		}
	} else {
		st, err = sk.Step(ctx, act)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != e {
		return
	}
	e.ready = true
	e.Progress = have(r.self(), e.item)
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
			return
		}
		e.Skill = e.skills[e.idx].Name()
	}
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
