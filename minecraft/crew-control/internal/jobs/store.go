// Package jobs runs a goal as a set of steps with dependencies. Each step is
// one task template; a stateless scheduler turns ready steps into work on the
// queue, retries failures and decides when the job is finished.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

const (
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobCancelled = "cancelled"

	StepWaiting   = "waiting"
	StepQueued    = "queued"
	StepRunning   = "running"
	StepDone      = "done"
	StepFailed    = "failed"
	StepCancelled = "cancelled"

	KindPlan  = "plan"
	KindStock = "stock"

	// MaxAttempts is how many times a step's work may be tried.
	MaxAttempts = 3

	// DefaultTimeout is how long a job without a deadline may run.
	DefaultTimeout = 30 * time.Minute
)

var (
	ErrNotFound = errors.New("jobs: no such job")
	ErrInvalid  = errors.New("jobs: invalid job")
	// ErrBusy: another scheduler pass holds the job.
	ErrBusy = errors.New("jobs: job is being worked on, try again")
)

// Step is one task. Unit, when set, pins it; WorkID is the item of the latest
// attempt. A waiting step that still has a WorkID is awaiting a retry, and
// that item's unit is the one to steer away from.
type Step struct {
	ID        uuid.UUID
	Key, Unit string
	Template  json.RawMessage
	DependsOn []string
	State     string
	Attempts  int
	WorkID    *uuid.UUID
	Reason    string
}

type Job struct {
	ID                         uuid.UUID
	Kind                       string
	Spec                       json.RawMessage
	State, Reason, Operator    string
	Round                      int
	Deadline, Created, Updated time.Time
	Steps                      []Step
}

type Store interface {
	// Create stores a running job and its steps, giving them ids and the
	// waiting state. It rejects duplicate keys, unknown dependencies and cycles.
	Create(ctx context.Context, j Job) (Job, error)
	// Running returns the ids of running jobs, oldest first.
	Running(ctx context.Context) ([]uuid.UUID, error)
	// Lock runs fn with the job locked for update and stores what fn changed
	// when it returns nil; an error discards the changes. A job another
	// caller holds is skipped, with ok false. A missing job is ErrNotFound.
	Lock(ctx context.Context, id uuid.UUID, fn func(*Job) error) (ok bool, err error)
	Get(ctx context.Context, id uuid.UUID) (Job, error)
	// List returns the newest jobs first.
	List(ctx context.Context, n int) ([]Job, error)
}

func (j Job) clone() Job {
	j.Steps = slices.Clone(j.Steps)
	for i, s := range j.Steps {
		j.Steps[i].DependsOn = slices.Clone(s.DependsOn)
		j.Steps[i].Template = slices.Clone(s.Template)
		if s.WorkID != nil {
			id := *s.WorkID
			j.Steps[i].WorkID = &id
		}
	}
	j.Spec = slices.Clone(j.Spec)
	return j
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ValidateSteps checks keys and dependencies; a step that waits on a key that
// never exists, or on itself through a cycle, would hold the job until its
// deadline.
func ValidateSteps(steps []Step) error {
	idx := map[string]int{}
	for i, s := range steps {
		if s.Key == "" {
			return invalid("step has no key")
		}
		if _, dup := idx[s.Key]; dup {
			return invalid("duplicate step key %q", s.Key)
		}
		idx[s.Key] = i
	}
	for _, s := range steps {
		for _, d := range s.DependsOn {
			if _, ok := idx[d]; !ok {
				return invalid("step %q depends on unknown step %q", s.Key, d)
			}
		}
	}
	// Kahn's algorithm: whatever is left over sits on a cycle.
	left := map[string]int{}
	for _, s := range steps {
		left[s.Key] = len(s.DependsOn)
	}
	var queue []string
	for k, n := range left {
		if n == 0 {
			queue = append(queue, k)
		}
	}
	seen := 0
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		seen++
		for _, s := range steps {
			if slices.Contains(s.DependsOn, k) {
				if left[s.Key]--; left[s.Key] == 0 {
					queue = append(queue, s.Key)
				}
			}
		}
	}
	if seen != len(steps) {
		return invalid("steps depend on each other in a cycle")
	}
	return nil
}

// prepare gives a new job its ids and initial states.
func prepare(j Job) (Job, error) {
	if j.Kind != KindPlan && j.Kind != KindStock {
		return Job{}, invalid("unknown kind")
	}
	if err := ValidateSteps(j.Steps); err != nil {
		return Job{}, err
	}
	if j.Operator == "" {
		return Job{}, invalid("job has no operator")
	}
	j = j.clone()
	if j.ID == uuid.Nil {
		j.ID = uuid.New()
	}
	if j.Deadline.IsZero() {
		j.Deadline = time.Now().Add(DefaultTimeout)
	}
	if len(j.Spec) == 0 {
		j.Spec = json.RawMessage(`{}`)
	}
	j.State, j.Reason, j.Round = JobRunning, "", 0
	for i := range j.Steps {
		s := &j.Steps[i]
		s.ID, s.State, s.Attempts, s.WorkID, s.Reason = uuid.New(), StepWaiting, 0, nil, ""
		if s.DependsOn == nil {
			s.DependsOn = []string{}
		}
	}
	return j, nil
}
