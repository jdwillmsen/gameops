package task

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// fakeAct counts the inputs a runner sends.
type fakeAct struct {
	skill.Actuator
	ticks uint64
}

func (f *fakeAct) Ticks() uint64        { return f.ticks }
func (f *fakeAct) Pose() act.Pose       { return act.Pose{} }
func (f *fakeAct) Tick(act.Input) error { f.ticks++; return nil }

type fakeSkill struct {
	name     string
	steps    int // Running steps before Done
	failWith error
	readyErr error
	ran      int
	order    *[]string
	// ticks makes Step send its input, as the real skills do.
	ticks bool
}

func (f *fakeSkill) Name() string                { return f.name }
func (f *fakeSkill) Ready(belief.Snapshot) error { return f.readyErr }
func (f *fakeSkill) Step(_ context.Context, a skill.Actuator) (skill.Status, error) {
	if f.ticks {
		_ = a.Tick(act.Input{})
	}
	if f.order != nil && f.ran == 0 {
		*f.order = append(*f.order, f.name)
	}
	f.ran++
	if f.failWith != nil {
		return skill.Failed, f.failWith
	}
	if f.ran > f.steps {
		return skill.Done, nil
	}
	return skill.Running, nil
}

type captureLog struct {
	mu    sync.Mutex
	lines []map[string]any
}

func (c *captureLog) Info(event string, f map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := map[string]any{"event": event}
	for k, v := range f {
		m[k] = v
	}
	c.lines = append(c.lines, m)
}

type harness struct {
	r     *Runner
	act   *fakeAct
	log   *captureLog
	snap  belief.Snapshot
	clock time.Time
	made  []*fakeSkill
	order []string
}

func newHarness(t *testing.T, timeout time.Duration, steps int) *harness {
	t.Helper()
	h := &harness{log: &captureLog{}, clock: time.Unix(1000, 0), snap: belief.Snapshot{Spawned: true}}
	newSkills := func(Template) ([]skill.Skill, error) {
		a := &fakeSkill{name: "mine_block", steps: steps, order: &h.order}
		b := &fakeSkill{name: "deposit_chest", steps: steps, order: &h.order}
		h.made = append(h.made, a, b)
		return []skill.Skill{a, b}, nil
	}
	h.act = &fakeAct{}
	h.r = NewRunner(newSkills, h.act, func() belief.Snapshot { return h.snap }, timeout, h.log)
	h.r.now = func() time.Time { return h.clock }
	return h
}

func (h *harness) tick(n int) {
	for i := 0; i < n; i++ {
		h.r.step(context.Background())
		h.clock = h.clock.Add(50 * time.Millisecond)
	}
}

func tpl() Template {
	return Template{Template: "gather_and_deposit", Item: "cobblestone", Qty: 4, Chest: Chest{X: 701, Y: -3, Z: 4242}}
}

func stateOf(t *testing.T, r *Runner, id string) Task {
	t.Helper()
	for _, tk := range r.List() {
		if tk.ID == id {
			return tk
		}
	}
	t.Fatalf("task %s not listed", id)
	return Task{}
}

func TestRunnerRunsTasksInOrder(t *testing.T) {
	h := newHarness(t, time.Minute, 2)
	a, _ := h.r.Submit(tpl())
	b, _ := h.r.Submit(tpl())
	if len(a.ID) != 16 || a.State != "queued" {
		t.Fatalf("submitted = %+v", a)
	}
	h.tick(1)
	if got := stateOf(t, h.r, a.ID); got.State != "running" || got.Skill != "mine_block" {
		t.Errorf("first = %+v", got)
	}
	if got := stateOf(t, h.r, b.ID); got.State != "queued" {
		t.Errorf("second = %+v, want queued while the first runs", got)
	}
	h.tick(20)
	if stateOf(t, h.r, a.ID).State != "done" || stateOf(t, h.r, b.ID).State != "done" {
		t.Fatalf("tasks = %+v", h.r.List())
	}
	if got := strings.Join(h.order, ","); got != "mine_block,deposit_chest,mine_block,deposit_chest" {
		t.Errorf("order = %s", got)
	}
}

func TestRunnerFailsTheRunningTaskWhenTheSessionEnds(t *testing.T) {
	h := newHarness(t, time.Minute, 100)
	a, _ := h.r.Submit(tpl())
	b, _ := h.r.Submit(tpl())
	h.tick(3)
	h.r.SessionEnded()
	got := stateOf(t, h.r, a.ID)
	if got.State != "failed" || got.Reason != "session ended" {
		t.Errorf("running = %+v", got)
	}
	h.snap.Spawned = false
	h.tick(5)
	if got := stateOf(t, h.r, b.ID); got.State != "queued" {
		t.Errorf("queued = %+v, want it to wait", got)
	}

	h.snap.Spawned = true
	h.r.SetActuator(&fakeAct{})
	h.tick(1)
	c := stateOf(t, h.r, b.ID)
	h.r.Died()
	if got := stateOf(t, h.r, b.ID); c.State != "running" || got.State != "failed" || got.Reason != "unit died" {
		t.Errorf("after death = %+v (was %s)", got, c.State)
	}
}

func TestRunnerWaitsForSpawnBeforeStartingQueuedTasks(t *testing.T) {
	h := newHarness(t, time.Minute, 1)
	h.snap.Spawned = false
	a, _ := h.r.Submit(tpl())
	h.tick(5)
	if got := stateOf(t, h.r, a.ID); got.State != "queued" {
		t.Fatalf("before spawn = %+v", got)
	}
	h.snap.Spawned = true
	h.tick(1)
	if got := stateOf(t, h.r, a.ID); got.State != "running" {
		t.Errorf("after spawn = %+v", got)
	}
	h.snap.Dead = true
	h.tick(1)
	if stateOf(t, h.r, a.ID).State != "running" {
		t.Error("dead unit must not fail a task on its own; Died does that")
	}
	if h.made[0].ran != 1 {
		t.Errorf("steps = %d, a dead unit must not be stepped", h.made[0].ran)
	}
}

func TestRunnerTimesOutATask(t *testing.T) {
	h := newHarness(t, time.Second, 10000)
	a, _ := h.r.Submit(tpl())
	h.tick(10)
	if got := stateOf(t, h.r, a.ID); got.State != "running" {
		t.Fatalf("early = %+v", got)
	}
	h.tick(20)
	if got := stateOf(t, h.r, a.ID); got.State != "failed" || got.Reason != "timed out" {
		t.Errorf("late = %+v", got)
	}
}

func TestRunnerFailsWhenASkillIsNotReadyOrFails(t *testing.T) {
	h := newHarness(t, time.Minute, 1)
	h.r.newSkills = func(Template) ([]skill.Skill, error) {
		return []skill.Skill{&fakeSkill{name: "mine_block", readyErr: errors.New("no pickaxe in inventory")}}, nil
	}
	a, _ := h.r.Submit(tpl())
	h.tick(2)
	if got := stateOf(t, h.r, a.ID); got.State != "failed" || got.Reason != "no pickaxe in inventory" {
		t.Errorf("task = %+v", got)
	}
}

func TestCancelStopsTheRunningSkill(t *testing.T) {
	h := newHarness(t, time.Minute, 100)
	a, _ := h.r.Submit(tpl())
	b, _ := h.r.Submit(tpl())
	h.tick(3)
	ran := h.made[0].ran
	got, err := h.r.Cancel(a.ID)
	if err != nil || got.State != "cancelled" {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	h.tick(1)
	if h.made[0].ran != ran {
		t.Error("cancelled skill was stepped again")
	}
	if got := stateOf(t, h.r, b.ID); got.State != "running" {
		t.Errorf("next = %+v, want it to start", got)
	}
	if got, err := h.r.Cancel(b.ID); err != nil || got.State != "cancelled" {
		t.Errorf("cancel running = %+v, %v", got, err)
	}
	if _, err := h.r.Cancel("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}
	if _, err := h.r.Cancel(a.ID); !errors.Is(err, ErrFinished) {
		t.Errorf("finished = %v", err)
	}
}

func TestSubmitBoundsTheQueueAndKeepsFinishedHistory(t *testing.T) {
	h := newHarness(t, time.Minute, 0)
	for i := 0; i < MaxQueued; i++ {
		if _, err := h.r.Submit(tpl()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.r.Submit(tpl()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("overflow = %v", err)
	}
	for i := 0; i < MaxQueued*3+10 && len(h.r.List()) > 0; i++ {
		h.tick(1)
	}
	for i := 0; i < 60; i++ {
		h.r.Submit(tpl())
		h.tick(4)
	}
	if n := len(h.r.List()); n > MaxFinished+MaxQueued {
		t.Errorf("%d tasks kept", n)
	}
}

func TestTaskLogsCarryNoBody(t *testing.T) {
	h := newHarness(t, time.Minute, 0)
	h.r.Submit(tpl())
	h.tick(10)
	if len(h.log.lines) < 3 {
		t.Fatalf("lines = %v", h.log.lines)
	}
	for _, l := range h.log.lines {
		for k := range l {
			switch k {
			case "event", "id", "template", "state", "reason":
			default:
				t.Errorf("log field %q beyond id/template/state/reason", k)
			}
		}
		for _, v := range l {
			s, _ := v.(string)
			for _, bad := range []string{"701", "4242", "chest", "cobblestone"} {
				if strings.Contains(s, bad) {
					t.Errorf("log value %q leaks %q", s, bad)
				}
			}
		}
	}
}

type stopSkill struct {
	fakeSkill
	mu      sync.Mutex
	stops   int
	entered chan struct{}
	release chan struct{}
}

func (s *stopSkill) Step(ctx context.Context, a skill.Actuator) (skill.Status, error) {
	if s.entered != nil {
		s.entered <- struct{}{}
		<-s.release
	}
	return skill.Running, nil
}

func (s *stopSkill) Stop(skill.Actuator) {
	s.mu.Lock()
	s.stops++
	s.mu.Unlock()
}

func (s *stopSkill) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

func stopHarness(t *testing.T, timeout time.Duration, sk *stopSkill) *harness {
	h := newHarness(t, timeout, 0)
	h.r.newSkills = func(Template) ([]skill.Skill, error) { return []skill.Skill{sk}, nil }
	return h
}

func TestCancelAndTimeoutCleanUpTheRunningSkillOnce(t *testing.T) {
	sk := &stopSkill{fakeSkill: fakeSkill{name: "deposit_chest"}}
	h := stopHarness(t, time.Minute, sk)
	a, _ := h.r.Submit(tpl())
	h.tick(3)
	h.r.Cancel(a.ID)
	h.tick(5)
	if sk.count() != 1 {
		t.Errorf("stops after cancel = %d, want 1", sk.count())
	}

	sk = &stopSkill{fakeSkill: fakeSkill{name: "mine_block"}}
	h = stopHarness(t, time.Second, sk)
	h.r.Submit(tpl())
	h.tick(40)
	if sk.count() != 1 {
		t.Errorf("stops after timeout = %d, want 1", sk.count())
	}
}

func TestSessionEndAndDeathSkipCleanup(t *testing.T) {
	sk := &stopSkill{fakeSkill: fakeSkill{name: "mine_block"}}
	h := stopHarness(t, time.Minute, sk)
	h.r.Submit(tpl())
	h.tick(2)
	h.r.SessionEnded()
	h.tick(3)
	h.r.SetActuator(&fakeAct{})
	h.tick(3)
	if sk.count() != 0 {
		t.Errorf("stops = %d, want none", sk.count())
	}
}

func TestCancelDuringAStepStillCleansUpOnce(t *testing.T) {
	sk := &stopSkill{fakeSkill: fakeSkill{name: "mine_block"}, entered: make(chan struct{}), release: make(chan struct{})}
	h := stopHarness(t, time.Minute, sk)
	a, _ := h.r.Submit(tpl())
	done := make(chan struct{})
	go func() { h.r.step(context.Background()); close(done) }()
	<-sk.entered
	h.r.Cancel(a.ID)
	close(sk.release)
	<-done
	sk.entered = nil
	h.tick(4)
	if sk.count() != 1 {
		t.Errorf("stops = %d, want 1", sk.count())
	}
}
