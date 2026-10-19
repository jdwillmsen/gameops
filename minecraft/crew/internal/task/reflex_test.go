package task

import (
	"context"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// fakeReflex engages for the ticks the test says and counts what it is asked.
type fakeReflex struct {
	fightFrom, fightTo int // engaged for ticks in [from, to)
	tick               int
	fights, idles      int
	cancels, resets    int
	// The reflex tends the unit for ticks in [tendFrom, tendTo) of the ticks
	// it was not engaged on.
	tendFrom, tendTo int
	asked            int
	tends            int
	workingSeen      []bool
	timeline         *[]string
}

func (f *fakeReflex) note(s string) {
	if f.timeline != nil {
		*f.timeline = append(*f.timeline, s)
	}
}

func (f *fakeReflex) Engaged(act.Pose, belief.Snapshot) bool {
	f.tick++
	return f.tick >= f.fightFrom && f.tick < f.fightTo
}
func (f *fakeReflex) Fight(context.Context, skill.Actuator, belief.Snapshot) error {
	f.fights++
	f.note("fight")
	return nil
}
func (f *fakeReflex) Tending(_ belief.Snapshot, working bool) bool {
	f.asked++
	f.workingSeen = append(f.workingSeen, working)
	return f.asked >= f.tendFrom && f.asked < f.tendTo
}
func (f *fakeReflex) Tend(context.Context, skill.Actuator, belief.Snapshot) error {
	f.tends++
	f.note("tend")
	return nil
}
func (f *fakeReflex) Idle(context.Context, skill.Actuator) error {
	f.idles++
	f.note("idle")
	return nil
}
func (f *fakeReflex) Cancel() { f.cancels++; f.note("cancel") }
func (f *fakeReflex) Reset()  { f.resets++ }

// resumable records how the runner drove it.
type resumable struct {
	fakeSkill
	timeline *[]string
	stops    int
	resumes  int
	readies  int
}

func (s *resumable) Ready(belief.Snapshot) error {
	s.readies++
	*s.timeline = append(*s.timeline, "ready")
	return nil
}

func (s *resumable) Step(ctx context.Context, a skill.Actuator) (skill.Status, error) {
	*s.timeline = append(*s.timeline, "step")
	return s.fakeSkill.Step(ctx, a)
}

func (s *resumable) Stop(skill.Actuator) {
	s.stops++
	*s.timeline = append(*s.timeline, "stop")
}

func (s *resumable) Resume() {
	s.resumes++
	*s.timeline = append(*s.timeline, "resume")
}

func reflexHarness(t *testing.T, timeout time.Duration, steps int, rf *fakeReflex) (*harness, *resumable, *[]string) {
	t.Helper()
	h := newHarness(t, timeout, steps)
	timeline := &[]string{}
	rf.timeline = timeline
	sk := &resumable{fakeSkill: fakeSkill{name: "mine_block", steps: steps}, timeline: timeline}
	h.r.newSkills = func(Template) ([]skill.Skill, error) { return []skill.Skill{sk}, nil }
	h.r.SetActuator(&fakeAct{})
	h.r.SetReflex(rf)
	return h, sk, timeline
}

func TestIdleUnitIsTickedEveryStepAndATaskCancelsTheWalk(t *testing.T) {
	rf := &fakeReflex{}
	h, sk, timeline := reflexHarness(t, time.Minute, 3, rf)
	h.tick(5)
	if rf.idles != 5 || rf.cancels != 0 {
		t.Fatalf("idles %d cancels %d, want 5 idle ticks", rf.idles, rf.cancels)
	}
	h.r.Submit(tpl())
	h.tick(1)
	if rf.idles != 5 || rf.cancels != 1 {
		t.Errorf("after submit: idles %d cancels %d, want the idle walk cancelled on the same tick", rf.idles, rf.cancels)
	}
	if got := (*timeline)[len(*timeline)-3:]; got[0] != "cancel" || got[1] != "ready" || got[2] != "step" {
		t.Errorf("timeline tail = %v", got)
	}
	h.tick(10)
	if sk.ran == 0 {
		t.Error("the task never ran")
	}
}

func TestAFightStopsTheSkillThenResumesItInsteadOfStartingOver(t *testing.T) {
	rf := &fakeReflex{fightFrom: 4, fightTo: 9}
	h, sk, _ := reflexHarness(t, time.Minute, 6, rf)
	a, _ := h.r.Submit(tpl())
	h.tick(25)
	if got := stateOf(t, h.r, a.ID); got.State != "done" {
		t.Fatalf("task = %+v", got)
	}
	if sk.stops != 1 {
		t.Errorf("stops = %d, want 1", sk.stops)
	}
	if sk.resumes != 1 || sk.readies != 1 {
		t.Errorf("resumes %d readies %d: a resumed skill must not be judged as a fresh start", sk.resumes, sk.readies)
	}
	// The tick that stopped the skill carried the stop's input, so only four
	// of the five engaged ticks are fights.
	if rf.fights != 4 {
		t.Errorf("fights = %d, want 4", rf.fights)
	}
}

func TestAFightBeforeTheFirstStepNeedsNoStop(t *testing.T) {
	rf := &fakeReflex{fightFrom: 1, fightTo: 4}
	h, sk, timeline := reflexHarness(t, time.Minute, 2, rf)
	a, _ := h.r.Submit(tpl())
	h.tick(10)
	if sk.stops != 0 || sk.resumes != 0 {
		t.Errorf("stops %d resumes %d, want none: the skill had not started", sk.stops, sk.resumes)
	}
	if stateOf(t, h.r, a.ID).State != "done" {
		t.Errorf("timeline = %v", *timeline)
	}
}

func TestFightTimeDoesNotCountTowardsTheTaskTimeout(t *testing.T) {
	rf := &fakeReflex{fightFrom: 3, fightTo: 100}
	h, _, _ := reflexHarness(t, time.Second, 100000, rf)
	a, _ := h.r.Submit(tpl())
	h.tick(90) // 4.5 s, mostly fighting, against a 1 s timeout
	if got := stateOf(t, h.r, a.ID); got.State != "running" {
		t.Fatalf("task = %+v, want it still running", got)
	}
	rf.fightTo = 0
	h.tick(40) // 2 s of work
	if got := stateOf(t, h.r, a.ID); got.State != "failed" || got.Reason != "timed out" {
		t.Errorf("task = %+v, want a timeout once the work itself ran long", got)
	}
}

func TestFightingDoesNotStartAQueuedTask(t *testing.T) {
	rf := &fakeReflex{fightFrom: 1, fightTo: 6}
	h, sk, _ := reflexHarness(t, time.Minute, 2, rf)
	a, _ := h.r.Submit(tpl())
	h.tick(5)
	if got := stateOf(t, h.r, a.ID); got.State != "queued" || sk.readies != 0 {
		t.Errorf("task = %+v, readies %d: nothing should start mid-fight", got, sk.readies)
	}
}

func TestDeathAndSessionEndResetTheReflexOnTheSteppingGoroutine(t *testing.T) {
	rf := &fakeReflex{}
	h, _, _ := reflexHarness(t, time.Minute, 3, rf)
	h.r.Died()
	if rf.resets != 0 {
		t.Fatal("Died reset the reflex from the read loop's goroutine")
	}
	h.tick(1)
	if rf.resets != 1 {
		t.Errorf("resets = %d after a step, want 1", rf.resets)
	}
	h.tick(1)
	h.r.SessionEnded()
	h.r.SetActuator(&fakeAct{})
	h.tick(1)
	if rf.resets != 2 {
		t.Errorf("resets = %d after the session ended, want 2", rf.resets)
	}
}

func TestTendingStopsTheSkillThenResumesItAndTellsTheReflexATaskIsRunning(t *testing.T) {
	rf := &fakeReflex{tendFrom: 4, tendTo: 9}
	h, sk, _ := reflexHarness(t, time.Minute, 6, rf)
	h.r.Submit(tpl())
	h.tick(25)
	if sk.stops != 1 || sk.resumes != 1 || sk.readies != 1 {
		t.Errorf("stops %d resumes %d readies %d, want one of each", sk.stops, sk.resumes, sk.readies)
	}
	if rf.tends != 4 {
		t.Errorf("tends = %d, want 4: the tick that stopped the skill carried the stop's input", rf.tends)
	}
	if rf.workingSeen[0] || !rf.workingSeen[4] {
		t.Errorf("working = %v: false before the task started, true once it ran", rf.workingSeen[:6])
	}
}

func TestTendingTimeDoesNotCountTowardsTheTaskTimeout(t *testing.T) {
	rf := &fakeReflex{tendFrom: 3, tendTo: 100}
	h, _, _ := reflexHarness(t, time.Second, 100000, rf)
	a, _ := h.r.Submit(tpl())
	h.tick(90)
	if got := stateOf(t, h.r, a.ID); got.State != "running" {
		t.Fatalf("task = %+v, want it still running", got)
	}
}

func TestTendingKeepsAQueuedTaskWaiting(t *testing.T) {
	rf := &fakeReflex{tendFrom: 1, tendTo: 6}
	h, sk, _ := reflexHarness(t, time.Minute, 2, rf)
	a, _ := h.r.Submit(tpl())
	h.tick(5)
	if got := stateOf(t, h.r, a.ID); got.State != "queued" || sk.readies != 0 || rf.tends != 5 {
		t.Errorf("task = %+v, readies %d, tends %d", got, sk.readies, rf.tends)
	}
}

func TestAFightWinsOverTending(t *testing.T) {
	rf := &fakeReflex{fightFrom: 1, fightTo: 4, tendFrom: 1, tendTo: 100}
	h, _, _ := reflexHarness(t, time.Minute, 2, rf)
	h.r.Submit(tpl())
	h.tick(3)
	if rf.fights != 3 || rf.tends != 0 {
		t.Errorf("fights %d tends %d, want the fight to take every tick", rf.fights, rf.tends)
	}
}
