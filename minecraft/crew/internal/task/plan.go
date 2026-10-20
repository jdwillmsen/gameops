package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxPlanSteps bounds WORK_PLAN; a standing rotation is a handful of tasks.
	MaxPlanSteps = 8

	planPoll = 2 * time.Second
	// planRetry spaces out a plan whose yard is spent: a failed step is skipped
	// to the next, but never retried in a tight loop.
	planRetry = 30 * time.Second
)

// ParsePlan reads WORK_PLAN, a JSON array of task bodies exactly as the task
// API takes them. An empty string is no plan.
func ParsePlan(raw string) ([]Template, error) {
	if raw == "" {
		return nil, nil
	}
	var bodies []json.RawMessage
	if err := json.NewDecoder(bytes.NewReader([]byte(raw))).Decode(&bodies); err != nil {
		return nil, fmt.Errorf("want a JSON array of tasks: %w", err)
	}
	if len(bodies) == 0 || len(bodies) > MaxPlanSteps {
		return nil, fmt.Errorf("want 1 to %d tasks, got %d", MaxPlanSteps, len(bodies))
	}
	plan := make([]Template, len(bodies))
	for i, b := range bodies {
		t, err := ParseTemplate(b)
		if err != nil {
			return nil, fmt.Errorf("task %d: %w", i+1, err)
		}
		plan[i] = t
	}
	return plan, nil
}

// Planner gives a unit standing work: whenever the runner has nothing queued
// or running it submits the plan's next task, round and round. Work that
// arrives from elsewhere (the task API, crew-control) preempts it, so a plan
// only ever fills idle time.
type Planner struct {
	r    *Runner
	plan []Template
	now  func() time.Time

	next  int
	ours  string
	retry time.Time
}

func NewPlanner(r *Runner, plan []Template) *Planner {
	return &Planner{r: r, plan: plan, now: time.Now}
}

// Run polls until ctx ends.
func (p *Planner) Run(ctx context.Context) {
	t := time.NewTicker(planPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Step()
		}
	}
}

// Step looks at the runner once and submits or yields as needed.
func (p *Planner) Step() {
	var busy, foreign, ourBusy bool
	var ourState string
	for _, t := range p.r.List() {
		if t.ID == p.ours {
			ourState = t.State
		}
		switch t.State {
		case Queued, Running:
			busy = true
			if t.ID != p.ours {
				foreign = true
			} else {
				ourBusy = true
			}
		}
	}
	if foreign && ourBusy {
		// Somebody's task is waiting behind ours; theirs wins.
		_, _ = p.r.Cancel(p.ours)
		return
	}
	if ourState == Failed {
		p.retry = p.now().Add(planRetry)
		p.ours = ""
	}
	if busy || p.now().Before(p.retry) {
		return
	}
	t, err := p.r.Submit(p.plan[p.next])
	if errors.Is(err, ErrQueueFull) {
		return
	}
	p.next = (p.next + 1) % len(p.plan)
	if err != nil {
		// A template the world rejects now will not pass in a moment either.
		p.retry = p.now().Add(planRetry)
		return
	}
	p.ours = t.ID
}
