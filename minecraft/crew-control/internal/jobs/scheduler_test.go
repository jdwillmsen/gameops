package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	t       *testing.T
	clock   *clock
	jobs    Store
	work    *work.Memory
	offline map[string]bool
	roster  []string
	checks  Checks
	notes   []uuid.UUID
	s       *Scheduler
}

func newRig(t *testing.T, units ...string) *rig {
	r := &rig{t: t, clock: &clock{t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}, jobs: NewMemory(), work: work.NewMemory(),
		offline: map[string]bool{}, roster: units, checks: DefaultChecks()}
	r.s = r.scheduler()
	return r
}

// scheduler builds a fresh scheduler over the rig's stores, as a restart would.
func (r *rig) scheduler() *Scheduler {
	return New(Config{
		Jobs: r.jobs, Work: r.work, Checks: r.checks, Now: r.clock.Now,
		Units:  r.units,
		Notify: func(id uuid.UUID) { r.notes = append(r.notes, id) },
	})
}

func (r *rig) units(ctx context.Context) ([]ledger.UnitStatus, error) {
	var st []telemetry.Status
	open := map[string][]work.Item{}
	for _, id := range r.roster {
		st = append(st, telemetry.Status{ID: id, Name: id, Online: !r.offline[id]})
		items, err := r.work.Open(ctx, id)
		if err != nil {
			return nil, err
		}
		open[id] = items
	}
	return ledger.Statuses(st, open), nil
}

func (r *rig) job(steps ...Step) Job {
	j := newJob(steps...)
	j.Deadline = r.clock.Now().Add(30 * time.Minute)
	j, err := r.jobs.Create(ctx, j)
	if err != nil {
		r.t.Fatal(err)
	}
	return j
}

func (r *rig) tick() {
	r.t.Helper()
	if err := r.s.Tick(ctx); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) get(id uuid.UUID) Job {
	r.t.Helper()
	j, err := r.jobs.Get(ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return j
}

func (r *rig) item(j Job, key string) work.Item {
	r.t.Helper()
	s := byKey(j, key)
	if s.WorkID == nil {
		r.t.Fatalf("step %s has no work (state %s)", key, s.State)
	}
	it, err := r.work.Get(ctx, *s.WorkID)
	if err != nil {
		r.t.Fatal(err)
	}
	return it
}

// run plays the unit: it takes the item, then reports the state.
func (r *rig) run(it work.Item, state, reason string) {
	r.t.Helper()
	got, ok, _, err := r.work.Next(ctx, it.Unit, "", r.clock.Now())
	if err != nil || !ok || got.ID != it.ID {
		r.t.Fatalf("unit %s did not get %s: got %v ok=%v err=%v", it.Unit, it.ID, got.ID, ok, err)
	}
	if _, err := r.work.Apply(ctx, it.Unit, it.ID, state, 100, reason); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) finish(j Job, key string) {
	r.t.Helper()
	r.run(r.item(j, key), work.Done, "")
}

func (r *rig) states(j Job) string {
	var out []string
	for _, s := range j.Steps {
		out = append(out, fmt.Sprintf("%s=%s", s.Key, s.State))
	}
	return strings.Join(out, " ")
}

func TestPlanWithDependencies(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	j := r.job(step("a"), step("b"), step("c", "a", "b"))
	r.tick()
	got := r.get(j.ID)
	if r.states(got) != "a=queued b=queued c=waiting" {
		t.Fatalf("states = %s", r.states(got))
	}
	if ua, ub := r.item(got, "a").Unit, r.item(got, "b").Unit; ua == ub {
		t.Fatalf("a and b share %s", ua)
	}
	if it := r.item(got, "a"); it.JobID == nil || *it.JobID != j.ID || it.Operator != "ada" || *it.StepID != byKey(got, "a").ID {
		t.Fatalf("item = %+v", it)
	}
	r.finish(got, "a")
	r.tick()
	if got = r.get(j.ID); r.states(got) != "a=done b=queued c=waiting" {
		t.Fatalf("states = %s", r.states(got))
	}
	r.finish(got, "b")
	r.tick()
	got = r.get(j.ID)
	if r.states(got) != "a=done b=done c=queued" || got.State != JobRunning {
		t.Fatalf("states = %s job %s", r.states(got), got.State)
	}
	r.finish(got, "c")
	r.tick()
	if got = r.get(j.ID); got.State != JobDone || got.Reason != "" {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
	if len(r.notes) == 0 {
		t.Fatal("no notifications")
	}
	// A finished job is not visited again.
	n := len(r.notes)
	r.tick()
	if len(r.notes) != n {
		t.Fatal("notified for a finished job")
	}
}

func TestRunningWorkReadsAsRunning(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	r.tick()
	it := r.item(r.get(j.ID), "a")
	if _, ok, _, _ := r.work.Next(ctx, it.Unit, "", r.clock.Now()); !ok {
		t.Fatal("not delivered")
	}
	r.tick()
	if got := r.get(j.ID); byKey(got, "a").State != StepQueued {
		t.Fatalf("delivered reads %s", byKey(got, "a").State)
	}
	if _, err := r.work.Apply(ctx, it.Unit, it.ID, work.Running, 10, ""); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if got := r.get(j.ID); byKey(got, "a").State != StepRunning {
		t.Fatalf("running reads %s", byKey(got, "a").State)
	}
}

func TestStepRetriesThenSucceeds(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	for i := 1; i <= 2; i++ {
		r.tick()
		got := r.get(j.ID)
		if s := byKey(got, "a"); s.State != StepQueued || s.Attempts != i {
			t.Fatalf("round %d: %+v", i, s)
		}
		r.run(r.item(got, "a"), work.Failed, "no path")
		r.tick()
		got = r.get(j.ID)
		if got.State != JobRunning {
			t.Fatalf("job %s after %d failures", got.State, i)
		}
	}
	got := r.get(j.ID)
	// The retry was queued in the same pass that saw the failure.
	if s := byKey(got, "a"); s.State != StepQueued || s.Attempts != 3 {
		t.Fatalf("step = %+v", s)
	}
	r.finish(got, "a")
	r.tick()
	got = r.get(j.ID)
	if got.State != JobDone || byKey(got, "a").Attempts != 3 {
		t.Fatalf("job %s attempts %d", got.State, byKey(got, "a").Attempts)
	}
}

func TestThirdFailureFailsTheJobAndCancelsTheRest(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	pinned := step("b")
	pinned.Unit = "crew-02"
	j := r.job(step("a"), pinned, step("c", "a"))
	r.tick()
	got := r.get(j.ID)
	other := r.item(got, "b")
	// With crew-02 gone, a's retries fall back to the only unit left.
	r.offline["crew-02"] = true
	for i := 0; i < 3; i++ {
		r.run(r.item(got, "a"), work.Failed, "stuck")
		r.tick()
		got = r.get(j.ID)
	}
	if got.State != JobFailed || got.Reason != "step a: stuck" {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
	if s := byKey(got, "a"); s.State != StepFailed || s.Attempts != 3 {
		t.Fatalf("a = %+v", s)
	}
	if byKey(got, "b").State != StepCancelled || byKey(got, "c").State != StepCancelled {
		t.Fatalf("states = %s", r.states(got))
	}
	if it, _ := r.work.Get(ctx, other.ID); it.State != work.Cancelled {
		t.Fatalf("b's work = %s", it.State)
	}
}

func TestRetryPrefersAnotherUnit(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	j := r.job(step("a"))
	r.tick()
	got := r.get(j.ID)
	first := r.item(got, "a").Unit
	if first != "crew-01" {
		t.Fatalf("first on %s, want roster order", first)
	}
	r.run(r.item(got, "a"), work.Failed, "x")
	r.tick()
	if second := r.item(r.get(j.ID), "a").Unit; second != "crew-02" {
		t.Fatalf("retry on %s", second)
	}
}

func TestRetryUsesTheOnlyFreeUnitEvenIfItFailed(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	r.offline["crew-02"] = true
	j := r.job(step("a"))
	r.tick()
	got := r.get(j.ID)
	r.run(r.item(got, "a"), work.Failed, "x")
	r.tick()
	if u := r.item(r.get(j.ID), "a").Unit; u != "crew-01" {
		t.Fatalf("retry on %s", u)
	}
}

func TestFixedUnitIsKeptOnRetry(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	s := step("a")
	s.Unit = "crew-01"
	j := r.job(s)
	r.tick()
	r.run(r.item(r.get(j.ID), "a"), work.Failed, "x")
	r.tick()
	if u := r.item(r.get(j.ID), "a").Unit; u != "crew-01" {
		t.Fatalf("retry on %s", u)
	}
}

func TestFewestOpenItemsWinsThenRosterOrder(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02", "crew-03")
	// crew-01 holds one queued item (still Free) so crew-02 is less loaded.
	if _, err := r.work.Enqueue(ctx, "crew-01", work.KindTask, []byte(`{"t":1}`), "ada"); err != nil {
		t.Fatal(err)
	}
	j := r.job(step("a"))
	r.tick()
	if u := r.item(r.get(j.ID), "a").Unit; u != "crew-02" {
		t.Fatalf("picked %s", u)
	}
}

func TestNoFreeUnitLeavesTheStepWaiting(t *testing.T) {
	r := newRig(t, "crew-01")
	r.offline["crew-01"] = true
	j := r.job(step("a"))
	r.tick()
	if got := r.get(j.ID); byKey(got, "a").State != StepWaiting || byKey(got, "a").Attempts != 0 {
		t.Fatalf("step = %+v", byKey(got, "a"))
	}
	if items, _ := r.work.Recent(ctx, "crew-01", 10); len(items) != 0 {
		t.Fatalf("%d items enqueued", len(items))
	}
	r.offline["crew-01"] = false
	r.tick()
	if got := r.get(j.ID); byKey(got, "a").State != StepQueued {
		t.Fatalf("step = %+v", byKey(got, "a"))
	}
}

func TestTimeout(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"), step("b", "a"))
	r.tick()
	r.clock.Add(31 * time.Minute)
	r.tick()
	got := r.get(j.ID)
	if got.State != JobFailed || got.Reason != "timed out" {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
	if it, _ := r.work.Get(ctx, *byKey(got, "a").WorkID); it.State != work.Cancelled {
		t.Fatalf("work = %s", it.State)
	}
	if byKey(got, "a").State != StepCancelled || byKey(got, "b").State != StepCancelled {
		t.Fatalf("states = %s", r.states(got))
	}
}

// A unit that goes offline holding the work is redelivered every 30 s and
// every redelivery that ends failed spends an attempt; the job must not stay
// running forever.
func TestUnitGoingQuietEndsInFailureNotLimbo(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	for i := 0; i < MaxAttempts; i++ {
		r.tick()
		got := r.get(j.ID)
		if got.State != JobRunning {
			t.Fatalf("early end: %s", got.State)
		}
		it := r.item(got, "a")
		r.run(it, work.Running, "")
		r.clock.Add(work.RedeliverAfter + time.Second)
		// The unit comes back, finds nothing to resume and reports the lost task.
		r.run(it, work.Failed, "unit restarted")
		r.tick()
	}
	if got := r.get(j.ID); got.State != JobFailed || !strings.HasPrefix(got.Reason, "step a: ") {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
}

func TestCancelledWorkUnderARunningJobIsRetried(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	r.tick()
	it := r.item(r.get(j.ID), "a")
	if _, ok, err := r.work.CancelQueued(ctx, it.Unit, it.ID); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	r.tick()
	got := r.get(j.ID)
	if s := byKey(got, "a"); s.State != StepQueued || s.Attempts != 2 || r.item(got, "a").ID == it.ID {
		t.Fatalf("step = %+v", s)
	}
}

func TestCancelJobLeavesHandQueuedWork(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	hand, err := r.work.Enqueue(ctx, "crew-01", work.KindTask, []byte(`{"t":"hand"}`), "bob")
	if err != nil {
		t.Fatal(err)
	}
	j := r.job(step("a"), step("b"), step("c", "a"))
	r.tick()
	got := r.get(j.ID)
	// crew-02 has the job's step in hand; crew-01 still has it queued.
	a := r.item(got, "a")
	var running work.Item
	for _, k := range []string{"a", "b"} {
		if it := r.item(got, k); it.Unit == "crew-02" {
			running = it
		}
	}
	if running.ID == uuid.Nil {
		t.Fatalf("no step on crew-02; a on %s", a.Unit)
	}
	r.run(running, work.Running, "")

	out, err := r.s.Cancel(ctx, j.ID, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != JobCancelled {
		t.Fatalf("state = %s", out.State)
	}
	if s := out.Steps; byKey(out, "a").State != StepCancelled || byKey(out, "c").State != StepCancelled || len(s) != 3 {
		t.Fatalf("states = %s", r.states(out))
	}
	if it, _ := r.work.Get(ctx, hand.ID); it.State != work.Queued {
		t.Fatalf("hand-queued item is %s", it.State)
	}
	// The unit still holds the running task until its cancel_task is applied.
	if it, _ := r.work.Get(ctx, running.ID); it.State != work.Running {
		t.Fatalf("running item is %s", it.State)
	}
	for _, k := range []string{"a", "b"} {
		if it := r.item(got, k); it.ID != running.ID && it.State != work.Cancelled {
			t.Fatalf("queued job item %s is %s", k, it.State)
		}
	}
	cmds := 0
	for _, it := range openCmds(t, r, "crew-02") {
		if strings.Contains(string(it.Body), running.ID.String()) {
			cmds++
		}
	}
	if cmds != 1 {
		t.Fatalf("cancel_task commands for the running item: %d", cmds)
	}
	// The scheduler must not revive a cancelled job.
	r.tick()
	if got = r.get(j.ID); got.State != JobCancelled {
		t.Fatalf("state = %s", got.State)
	}
}

func openCmds(t *testing.T, r *rig, unit string) []work.Item {
	items, err := r.work.Open(ctx, unit)
	if err != nil {
		t.Fatal(err)
	}
	var out []work.Item
	for _, it := range items {
		if it.Kind == work.KindCommand {
			out = append(out, it)
		}
	}
	return out
}

func TestCancelOfAnEndedJobChangesNothing(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job()
	r.tick()
	out, err := r.s.Cancel(ctx, j.ID, "ada")
	if err != nil || out.State != JobDone {
		t.Fatalf("state = %s err = %v", out.State, err)
	}
	if _, err := r.s.Cancel(ctx, uuid.New(), "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestANewSchedulerFinishesAHalfRunJob(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	j := r.job(step("a"), step("b", "a"))
	r.tick()
	r.finish(r.get(j.ID), "a")
	r.tick()
	r.s = r.scheduler()
	got := r.get(j.ID)
	r.finish(got, "b")
	r.tick()
	if got = r.get(j.ID); got.State != JobDone {
		t.Fatalf("state = %s (%s)", got.State, r.states(got))
	}
}

func (r *rig) itemCount() int {
	n := 0
	for _, u := range r.roster {
		items, _ := r.work.Recent(ctx, u, 10)
		n += len(items)
	}
	return n
}

func TestRestartDoesNotQueueAStepTwice(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	r.job(step("a"))
	r.tick()
	r.s = r.scheduler()
	r.tick()
	if n := r.itemCount(); n != 1 {
		t.Fatalf("%d items for one step", n)
	}
}

// failingCommit runs the pass but loses its commit, as a timeout would.
type failingCommit struct{ Store }

func (f failingCommit) Lock(ctx context.Context, id uuid.UUID, fn func(*Job) error) (bool, error) {
	return f.Store.Lock(ctx, id, func(j *Job) error {
		if err := fn(j); err != nil {
			return err
		}
		return errors.New("commit failed")
	})
}

func TestRolledBackPassDoesNotQueueTheStepTwice(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	j := r.job(step("a"))
	r.s.cfg.Jobs = failingCommit{r.jobs}
	if err := r.s.Tick(ctx); err == nil {
		t.Fatal("tick did not fail")
	}
	if got := r.get(j.ID); byKey(got, "a").State != StepWaiting {
		t.Fatalf("step = %s", byKey(got, "a").State)
	}
	r.s.cfg.Jobs = r.jobs
	r.tick()
	if n := r.itemCount(); n != 1 {
		t.Fatalf("%d items for one step", n)
	}
	if got := r.get(j.ID); byKey(got, "a").State != StepQueued || byKey(got, "a").Attempts != 1 {
		t.Fatalf("step = %+v", byKey(got, "a"))
	}
}

func TestStepQueuedOnAUnitThatWentAwayMovesWithoutSpendingAnAttempt(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	j := r.job(step("a"))
	r.tick()
	got := r.get(j.ID)
	old := r.item(got, "a")
	if old.Unit != "crew-01" {
		t.Fatalf("first on %s", old.Unit)
	}
	r.offline["crew-01"] = true
	r.tick()
	got = r.get(j.ID)
	now := r.item(got, "a")
	if now.Unit != "crew-02" || now.ID == old.ID || byKey(got, "a").State != StepQueued || byKey(got, "a").Attempts != 1 {
		t.Fatalf("step = %+v on %s", byKey(got, "a"), now.Unit)
	}
	if it, _ := r.work.Get(ctx, old.ID); it.State != work.Cancelled {
		t.Fatalf("old item is %s", it.State)
	}
}

func TestDeliveredStepOnAnOfflineUnitIsLeftAlone(t *testing.T) {
	r := newRig(t, "crew-01", "crew-02")
	j := r.job(step("a"))
	r.tick()
	it := r.item(r.get(j.ID), "a")
	if _, ok, _, _ := r.work.Next(ctx, it.Unit, "", r.clock.Now()); !ok {
		t.Fatal("not delivered")
	}
	r.offline[it.Unit] = true
	r.tick()
	if got := r.item(r.get(j.ID), "a"); got.ID != it.ID || got.State != work.Delivered {
		t.Fatalf("item = %+v", got)
	}
}

func TestWorkThatVanishedCountsAsAFailure(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	r.tick()
	if _, err := r.work.Prune(ctx, r.clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if s := byKey(r.get(j.ID), "a"); s.State != StepQueued || s.Attempts != 2 {
		t.Fatalf("step = %+v", s)
	}
}

func TestAnInvalidTemplateFailsTheJobAtOnce(t *testing.T) {
	r := newRig(t, "crew-01")
	bad := step("a")
	bad.Template = []byte(`[1]`)
	j := r.job(bad)
	r.tick()
	got := r.get(j.ID)
	if got.State != JobFailed || !strings.HasPrefix(got.Reason, "step a: ") {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
}

func TestChecks(t *testing.T) {
	topUp := func(Job) []Step { return []Step{step("extra")} }
	cases := []struct {
		name      string
		kind      string
		check     Check
		wantState string
		wantWhy   string
		wantExtra bool
		round     int
	}{
		{"plan needs nothing more", KindPlan, nil, JobDone, "", false, 0},
		{"met", KindStock, func(context.Context, Job) (bool, string, []Step, error) { return true, "", nil, nil }, JobDone, "", false, 0},
		{"top-up once", KindStock, func(_ context.Context, j Job) (bool, string, []Step, error) {
			return false, "have 3 of 5", topUp(j), nil
		}, JobRunning, "", true, 1},
		{"no top-up fails", KindStock, func(context.Context, Job) (bool, string, []Step, error) { return false, "have 3 of 5", nil, nil }, JobFailed, "goal not met: have 3 of 5", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, "crew-01")
			if tc.check != nil {
				r.checks[tc.kind] = tc.check
			}
			r.s = r.scheduler()
			j := newJob(step("a"))
			j.Kind, j.Deadline = tc.kind, r.clock.Now().Add(time.Hour)
			j, _ = r.jobs.Create(ctx, j)
			r.tick()
			r.finish(r.get(j.ID), "a")
			r.tick()
			got := r.get(j.ID)
			if got.State != tc.wantState || got.Reason != tc.wantWhy || got.Round != tc.round || (len(got.Steps) == 2) != tc.wantExtra {
				t.Fatalf("job = %s %q round %d steps %d", got.State, got.Reason, got.Round, len(got.Steps))
			}
		})
	}
}

func TestSecondFailedCheckFailsTheJob(t *testing.T) {
	r := newRig(t, "crew-01")
	r.checks[KindStock] = func(context.Context, Job) (bool, string, []Step, error) {
		return false, "have 3 of 5", []Step{step("extra", "a")}, nil
	}
	r.s = r.scheduler()
	j := newJob(step("a"))
	j.Kind, j.Deadline = KindStock, r.clock.Now().Add(time.Hour)
	j, _ = r.jobs.Create(ctx, j)
	r.tick()
	r.finish(r.get(j.ID), "a")
	r.tick() // all done, check fails, top-up appended
	r.tick() // top-up step queued
	got := r.get(j.ID)
	if s := byKey(got, "extra"); s.State != StepQueued || s.ID == uuid.Nil {
		t.Fatalf("extra = %+v", s)
	}
	r.finish(got, "extra")
	r.tick()
	if got = r.get(j.ID); got.State != JobFailed || got.Reason != "goal not met: have 3 of 5" {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
}

func TestACheckErrorKeepsTheJobRunning(t *testing.T) {
	r := newRig(t, "crew-01")
	r.checks[KindStock] = func(context.Context, Job) (bool, string, []Step, error) {
		return false, "", nil, ledger.ErrUnavailable
	}
	r.s = r.scheduler()
	j := newJob(step("a"))
	j.Kind, j.Deadline = KindStock, r.clock.Now().Add(time.Hour)
	j, _ = r.jobs.Create(ctx, j)
	r.tick()
	r.finish(r.get(j.ID), "a")
	if err := r.s.Tick(ctx); !errors.Is(err, ledger.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if got := r.get(j.ID); got.State != JobRunning || byKey(got, "a").State != StepDone {
		t.Fatalf("job = %s, a = %s", got.State, byKey(got, "a").State)
	}
}

func TestQueueDownKeepsEarlierProgress(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"), step("b", "a"))
	r.tick()
	r.finish(r.get(j.ID), "a")
	r.s.cfg.Work = failingEnqueue{r.work}
	if err := r.s.Tick(ctx); !errors.Is(err, work.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	got := r.get(j.ID)
	if got.State != JobRunning || byKey(got, "a").State != StepDone || byKey(got, "b").State != StepWaiting {
		t.Fatalf("states = %s", r.states(got))
	}
}

type failingEnqueue struct{ work.Store }

func (failingEnqueue) EnqueueStep(context.Context, string, json.RawMessage, string, uuid.UUID, uuid.UUID) (work.Item, error) {
	return work.Item{}, work.ErrUnavailable
}

// failingWork reads fine but cannot cancel, like a database that drops out
// between the two calls.
type failingWork struct{ work.Store }

func (failingWork) CancelJob(context.Context, uuid.UUID, string) ([]work.Item, error) {
	return nil, work.ErrUnavailable
}

func TestFailedCancelRollsTheDecisionBack(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	r.tick()
	r.clock.Add(time.Hour)
	r.s.cfg.Work = failingWork{r.work}
	if err := r.s.Tick(ctx); !errors.Is(err, work.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if got := r.get(j.ID); got.State != JobRunning {
		t.Fatalf("state = %s", got.State)
	}
	r.s.cfg.Work = r.work
	r.tick()
	if got := r.get(j.ID); got.State != JobFailed || got.Reason != "timed out" {
		t.Fatalf("job = %s %q", got.State, got.Reason)
	}
}

func TestWakeMergesAndRunTicks(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	r.s.cfg.Every = time.Hour
	r.s.Wake()
	r.s.Wake()
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.s.Run(cctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for byKey(r.get(j.ID), "a").State != StepQueued {
		select {
		case <-deadline:
			t.Fatal("Run never ticked")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// flakyJobs fails Running while down is set, as a database outage would.
type flakyJobs struct {
	Store
	down  atomic.Bool
	calls atomic.Int32
}

func (f *flakyJobs) Running(ctx context.Context) ([]uuid.UUID, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return nil, work.ErrUnavailable
	}
	return f.Store.Running(ctx)
}

func TestRunLogsAnOutageOnceAndItsRecoveryOnce(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, out
	log := logging.New("info")
	os.Stdout, os.Stderr = realOut, realErr

	f := &flakyJobs{Store: NewMemory()}
	f.down.Store(true)
	s := New(Config{Jobs: f, Work: work.NewMemory(), Checks: DefaultChecks(), Log: log, Every: time.Millisecond,
		Units: func(context.Context) ([]ledger.UnitStatus, error) { return nil, nil }})
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Run(cctx); close(done) }()
	waitCalls := func(n int32) {
		for f.calls.Load() < n {
			time.Sleep(time.Millisecond)
		}
	}
	waitCalls(20)
	f.down.Store(false)
	waitCalls(f.calls.Load() + 20)
	cancel()
	<-done

	b, _ := os.ReadFile(out.Name())
	if n := strings.Count(string(b), "jobs_tick_failed"); n != 1 {
		t.Errorf("failure logged %d times over 20+ failing ticks:\n%s", n, b)
	}
	if n := strings.Count(string(b), "jobs_tick_recovered"); n != 1 {
		t.Errorf("recovery logged %d times:\n%s", n, b)
	}
}

func TestAnIdleTickLeavesTheJobUntouched(t *testing.T) {
	r := newRig(t, "crew-01")
	j := r.job(step("a"))
	r.tick()
	before := r.get(j.ID)
	r.clock.t = r.clock.t.Add(time.Minute)
	r.tick()
	if after := r.get(j.ID); !after.Updated.Equal(before.Updated) {
		t.Fatalf("an idle tick moved updated from %v to %v", before.Updated, after.Updated)
	}
}

// The same dependency plan over the real stores, so the row locks, the
// appended-step insert and EnqueueStep's job columns are exercised together.
func TestPlanOverPostgres(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	pg, err := work.OpenPostgres(ctx, dsn, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	for _, q := range []string{`DELETE FROM minecraft.crew_job`, `DELETE FROM minecraft.crew_work`} {
		if _, err := pg.Pool().Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	r := newRig(t, "crew-01", "crew-02")
	r.jobs, r.work = NewPostgres(pg.Pool()), nil
	store := work.Store(pg)
	r.s = New(Config{Jobs: r.jobs, Work: store, Checks: r.checks, Now: time.Now,
		Units: func(ctx context.Context) ([]ledger.UnitStatus, error) {
			var st []telemetry.Status
			open := map[string][]work.Item{}
			for _, id := range r.roster {
				st = append(st, telemetry.Status{ID: id, Online: true})
				open[id], _ = store.Open(ctx, id)
			}
			return ledger.Statuses(st, open), nil
		}})
	j := newJob(step("a"), step("b", "a"))
	j, err = r.jobs.Create(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		if err := r.s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		got := r.get(j.ID)
		it, err := store.Get(ctx, *byKey(got, key).WorkID)
		if err != nil || it.JobID == nil || *it.JobID != j.ID {
			t.Fatalf("item = %+v err = %v", it, err)
		}
		if _, ok, _, err := store.Next(ctx, it.Unit, "", time.Now()); err != nil || !ok {
			t.Fatalf("next: %v %v", ok, err)
		}
		if _, err := store.Apply(ctx, it.Unit, it.ID, work.Done, 100, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.get(j.ID); got.State != JobDone {
		t.Fatalf("state = %s (%s)", got.State, r.states(got))
	}
}
