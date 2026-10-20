package jobs

import "context"

// Check decides whether a job whose steps are all done has met its goal. When
// it has not, short says how far off it is, and topUp is the further work
// that could close the gap; the scheduler plans at most one such round.
type Check func(ctx context.Context, j Job) (ok bool, short string, topUp []Step, err error)

// Checks maps a job kind to its check. A kind without one is done when every
// step is.
type Checks map[string]Check

// PlanCheck is a plan job's check: its steps are the whole goal.
func PlanCheck(context.Context, Job) (bool, string, []Step, error) {
	return true, "", nil, nil
}

// DefaultChecks registers what needs nothing outside the job.
func DefaultChecks() Checks { return Checks{KindPlan: PlanCheck} }
