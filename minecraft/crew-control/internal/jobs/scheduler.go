package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

const defaultEvery = 2 * time.Second

type Config struct {
	Jobs Store
	Work work.Store
	// Units returns every unit's status, in roster order: the order breaks
	// ties between equally idle units.
	Units  func(ctx context.Context) ([]ledger.UnitStatus, error)
	Checks Checks
	Now    func() time.Time
	// Notify is called with the id of a job that changed.
	Notify func(job uuid.UUID)
	Log    *logging.Logger
	// Every is the tick period; zero means two seconds.
	Every time.Duration
}

// Scheduler keeps nothing between ticks: each decision reads the job and the
// queue afresh, so a restart, a lost wake or a second replica cannot
// disagree with the stores.
type Scheduler struct {
	cfg  Config
	wake chan struct{}
}

func New(cfg Config) *Scheduler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Every <= 0 {
		cfg.Every = defaultEvery
	}
	if cfg.Notify == nil {
		cfg.Notify = func(uuid.UUID) {}
	}
	return &Scheduler{cfg: cfg, wake: make(chan struct{}, 1)}
}

// Wake asks for a tick soon. Wakes that arrive while one is pending merge.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Every)
	defer t.Stop()
	failing := false
	for {
		// Logged on the transition only: an outage would otherwise write a
		// line every tick for as long as it lasts.
		switch err := s.Tick(ctx); {
		case err != nil && ctx.Err() == nil:
			if !failing {
				s.warn("jobs_tick_failed", nil, err)
			}
			failing = true
		case err == nil && failing:
			failing = false
			if s.cfg.Log != nil {
				s.cfg.Log.Info("jobs_tick_recovered", logging.Fields{})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

func (s *Scheduler) warn(event string, fields logging.Fields, err error) {
	if s.cfg.Log == nil {
		return
	}
	if fields == nil {
		fields = logging.Fields{}
	}
	fields["error"] = err.Error()
	if errors.Is(err, work.ErrUnavailable) || work.IsUnavailable(err) {
		fields["effect"] = "will retry"
	}
	s.cfg.Log.Warn(event, fields)
}

func (s *Scheduler) info(event string, job uuid.UUID, step string) {
	if s.cfg.Log == nil {
		return
	}
	f := logging.Fields{"job": job.String()}
	if step != "" {
		f["step"] = step
	}
	s.cfg.Log.Info(event, f)
}

// Tick runs one pass over every running job. A job that fails to advance does
// not hold up the others; the errors are joined.
func (s *Scheduler) Tick(ctx context.Context) error {
	ids, err := s.cfg.Jobs.Running(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if err := s.advance(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Scheduler) advance(ctx context.Context, id uuid.UUID) error {
	var (
		changed bool
		passErr error
	)
	ok, err := s.cfg.Jobs.Lock(ctx, id, func(j *Job) error {
		if j.State != JobRunning {
			return nil
		}
		p := pass{s: s, ctx: ctx, j: j}
		err := p.run()
		changed, passErr = p.changed, p.err
		// A failure to cancel the job's work must roll the whole decision
		// back, or the job would read as failed with work still running.
		return err
	})
	if err != nil {
		return err
	}
	if ok && changed {
		s.cfg.Notify(id)
	}
	return passErr
}

// pass is one visit to one locked job.
type pass struct {
	s       *Scheduler
	ctx     context.Context
	j       *Job
	changed bool
	// err is a problem that stops the pass but keeps what it already did
	// (a queue that is down after some steps were queued).
	err error

	status []ledger.UnitStatus
	loaded bool
}

// units reads the unit statuses once per pass; assign bumps the copy as it
// queues so later steps see the load.
func (p *pass) units() ([]ledger.UnitStatus, error) {
	if !p.loaded {
		st, err := p.s.cfg.Units(p.ctx)
		if err != nil {
			return nil, err
		}
		p.status, p.loaded = st, true
	}
	return p.status, nil
}

// release takes back work still queued on a unit that has gone away so the
// step can go to another one. It is not a failed attempt. If the unit took
// the item in the meantime, nothing moves.
func (p *pass) release(st *Step, it work.Item) (bool, error) {
	if st.Unit != "" {
		return false, nil
	}
	units, err := p.units()
	if err != nil {
		return false, err
	}
	for _, u := range units {
		if u.ID != it.Unit || (u.Online && !u.Dead) {
			continue
		}
		_, cancelled, err := p.s.cfg.Work.CancelQueued(p.ctx, it.Unit, it.ID, "unit offline, reassigned")
		if err != nil || !cancelled {
			return false, err
		}
		// WorkID stays so the next pick steers away from this unit.
		st.State, st.Attempts, p.changed = StepWaiting, st.Attempts-1, true
		p.s.info("job_step_released", p.j.ID, st.Key)
		return true, nil
	}
	return false, nil
}

func (p *pass) step(key string) *Step {
	for i := range p.j.Steps {
		if p.j.Steps[i].Key == key {
			return &p.j.Steps[i]
		}
	}
	return nil
}

func (p *pass) run() error {
	j := p.j
	if !p.s.cfg.Now().Before(j.Deadline) {
		return p.fail("timed out", "")
	}
	for i := range j.Steps {
		st := &j.Steps[i]
		if st.WorkID == nil || (st.State != StepQueued && st.State != StepRunning) {
			continue
		}
		it, err := p.s.cfg.Work.Get(p.ctx, *st.WorkID)
		switch {
		case errors.Is(err, work.ErrNotFound):
			it = work.Item{State: work.Failed, Reason: "work item is gone"}
		case err != nil:
			p.err = err
			return nil
		}
		// Work the job did not cancel but somebody else did is a failure of
		// the attempt: a cancelled job is never visited here.
		if it.State == work.Cancelled {
			it.State, it.Reason = work.Failed, "work cancelled"
		}
		switch it.State {
		case work.Queued:
			moved, err := p.release(st, it)
			if err != nil {
				p.err = err
				return nil
			}
			if !moved {
				p.set(st, StepQueued)
			}
		case work.Delivered:
			p.set(st, StepQueued)
		case work.Running:
			p.set(st, StepRunning)
		case work.Done:
			p.set(st, StepDone)
			st.Reason = ""
			p.s.info("job_step_done", j.ID, st.Key)
		case work.Failed:
			if st.Attempts < MaxAttempts {
				// WorkID stays so the retry can avoid the unit that failed.
				p.set(st, StepWaiting)
				st.Reason = it.Reason
				p.s.info("job_step_retry", j.ID, st.Key)
				continue
			}
			st.Reason = it.Reason
			p.set(st, StepFailed)
			return p.fail(fmt.Sprintf("step %s: %s", st.Key, it.Reason), st.Key)
		}
	}

	if err := p.assign(); err != nil {
		return err
	}
	if p.err != nil || j.State != JobRunning {
		return nil
	}

	for _, st := range j.Steps {
		if st.State != StepDone {
			return nil
		}
	}
	return p.finish()
}

func (p *pass) set(st *Step, state string) {
	if st.State != state {
		st.State = state
		p.changed = true
	}
}

// fail ends the job and cancels its remaining work. The caller's step, if
// any, keeps its own state.
func (p *pass) fail(reason, except string) error {
	if _, err := p.s.cfg.Work.CancelJob(p.ctx, p.j.ID, p.j.Operator); err != nil {
		return err
	}
	for i := range p.j.Steps {
		st := &p.j.Steps[i]
		if st.Key != except && st.State != StepDone && st.State != StepFailed {
			st.State = StepCancelled
		}
	}
	p.j.State, p.j.Reason, p.changed = JobFailed, reason, true
	p.s.info("job_failed", p.j.ID, except)
	return nil
}

func (p *pass) finish() error {
	j := p.j
	check, ok := p.s.cfg.Checks[j.Kind]
	if !ok {
		p.done()
		return nil
	}
	met, short, topUp, err := check(p.ctx, *j)
	if err != nil {
		p.err = err
		return nil
	}
	switch {
	case met:
		p.done()
	case j.Round == 0 && len(topUp) > 0:
		all := slices.Concat(j.Steps, topUp)
		if err := ValidateSteps(all); err != nil {
			return p.fail("goal not met: "+short, "")
		}
		for _, st := range topUp {
			st.ID, st.State, st.Attempts, st.WorkID, st.Reason = uuid.New(), StepWaiting, 0, nil, ""
			if st.DependsOn == nil {
				st.DependsOn = []string{}
			}
			j.Steps = append(j.Steps, st)
		}
		j.Round, p.changed = 1, true
		p.s.info("job_topped_up", j.ID, "")
	default:
		return p.fail("goal not met: "+short, "")
	}
	return nil
}

func (p *pass) done() {
	p.j.State, p.changed = JobDone, true
	p.s.info("job_done", p.j.ID, "")
}

// assign queues each waiting step whose dependencies are done.
func (p *pass) assign() error {
	for i := range p.j.Steps {
		st := &p.j.Steps[i]
		if st.State != StepWaiting || !p.ready(st) {
			continue
		}
		unit := st.Unit
		if unit == "" {
			units, err := p.units()
			if err != nil {
				p.err = err
				return nil
			}
			unit = p.pick(units, st)
			if unit == "" {
				continue
			}
		}
		it, err := p.s.cfg.Work.EnqueueStep(p.ctx, unit, st.Template, p.j.Operator, p.j.ID, st.ID)
		switch {
		case errors.Is(err, work.ErrInvalid):
			st.State, st.Reason = StepFailed, "template rejected"
			return p.fail("step "+st.Key+": template rejected", st.Key)
		case err != nil:
			p.err = err
			return nil
		}
		st.State, st.WorkID, st.Reason = StepQueued, &it.ID, ""
		st.Attempts++
		p.changed = true
		for k := range p.status {
			if p.status[k].ID == unit {
				p.status[k].Queued++
			}
		}
		p.s.info("job_step_queued", p.j.ID, st.Key)
	}
	return nil
}

func (p *pass) ready(st *Step) bool {
	for _, d := range st.DependsOn {
		if dep := p.step(d); dep == nil || dep.State != StepDone {
			return false
		}
	}
	return true
}

// pick chooses the free unit with the fewest unfinished items, the first in
// roster order on a tie. The unit that last failed the step is a last resort,
// not a veto: a crew of one must still be able to retry.
func (p *pass) pick(units []ledger.UnitStatus, st *Step) string {
	avoid := p.lastUnit(st)
	best, bestLoad, fallback := "", 0, ""
	for _, u := range units {
		if !u.Free() {
			continue
		}
		if u.ID == avoid {
			fallback = u.ID
			continue
		}
		if load := u.Queued + u.Delivered + u.Running; best == "" || load < bestLoad {
			best, bestLoad = u.ID, load
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

// lastUnit is the unit that held the step's previous attempt.
func (p *pass) lastUnit(st *Step) string {
	if st.WorkID == nil {
		return ""
	}
	it, err := p.s.cfg.Work.Get(p.ctx, *st.WorkID)
	if err != nil {
		return ""
	}
	return it.Unit
}

// Cancel ends a running job: its work is cancelled and its unfinished steps
// with it. A job that already ended is returned as it is.
func (s *Scheduler) Cancel(ctx context.Context, id uuid.UUID, operator string) (Job, error) {
	var cancelled bool
	ok, err := s.cfg.Jobs.Lock(ctx, id, func(j *Job) error {
		if j.State != JobRunning {
			return nil
		}
		if _, err := s.cfg.Work.CancelJob(ctx, id, operator); err != nil {
			return err
		}
		for i := range j.Steps {
			if st := &j.Steps[i]; st.State != StepDone && st.State != StepFailed {
				st.State = StepCancelled
			}
		}
		j.State, j.Reason = JobCancelled, "cancelled by "+operator
		cancelled = true
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if !ok {
		return Job{}, ErrBusy
	}
	if cancelled {
		s.cfg.Notify(id)
	}
	return s.cfg.Jobs.Get(ctx, id)
}

// Store is the job store the scheduler works on, for the routes that list and
// read jobs.
func (s *Scheduler) Store() Store { return s.cfg.Jobs }

// FreeUnits lists the units a planner may give more work. A roster that
// cannot be read yields none: planning only uses the count to size a split.
func (s *Scheduler) FreeUnits(ctx context.Context) []ledger.UnitStatus {
	all, err := s.cfg.Units(ctx)
	if err != nil {
		return nil
	}
	var free []ledger.UnitStatus
	for _, u := range all {
		if u.Free() {
			free = append(free, u)
		}
	}
	return free
}
