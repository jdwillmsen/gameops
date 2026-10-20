package task

import (
	"strings"
	"testing"
	"time"
)

const planJSON = `[{"template":"gather_and_deposit","item":"cobblestone","qty":4,"chest":{"x":1,"y":64,"z":2}},
{"template":"haul","items":{"cobblestone":4},"from":[{"x":1,"y":64,"z":2}],"to":{"x":3,"y":64,"z":2}}]`

func TestParsePlan(t *testing.T) {
	if p, err := ParsePlan(""); p != nil || err != nil {
		t.Errorf("empty = %v, %v; want no plan", p, err)
	}
	p, err := ParsePlan(planJSON)
	if err != nil || len(p) != 2 || p[1].Template != "haul" {
		t.Fatalf("plan = %+v, err %v", p, err)
	}
	step := `{"template":"goto","to":{"x":0,"y":0,"z":0}}`
	tooMany := "[" + strings.Repeat(step+",", MaxPlanSteps) + step + "]"
	for _, bad := range []string{`{}`, `[]`, `[{"template":"dig"}]`, `[1]`, `[{"template":"goto","to":{"x":9999,"y":0,"z":0}}]`, tooMany} {
		if _, err := ParsePlan(bad); err == nil {
			t.Errorf("ParsePlan(%.40s) accepted", bad)
		}
	}
}

func TestPlannerRotatesAndYieldsToOtherWork(t *testing.T) {
	h := newHarness(t, time.Minute, 0)
	plan, err := ParsePlan(planJSON)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPlanner(h.r, plan)
	p.now = func() time.Time { return h.clock }

	p.Step()
	tasks := h.r.List()
	if len(tasks) != 1 || tasks[0].Template != "gather_and_deposit" {
		t.Fatalf("after first step: %+v", tasks)
	}
	p.Step()
	if len(h.r.List()) != 1 {
		t.Fatal("submitted again while busy")
	}
	h.tick(10)
	if got := stateOf(t, h.r, tasks[0].ID); got.State != Done {
		t.Fatalf("first = %+v", got)
	}
	p.Step()
	if l := h.r.List(); len(l) != 2 || l[1].Template != "haul" {
		t.Fatalf("second step: %+v", l)
	}

	// A foreign task queued behind the plan's running one cancels the plan's.
	h.tick(1)
	other, _ := h.r.Submit(tpl())
	preempted := p.ours
	p.Step()
	if got := stateOf(t, h.r, preempted); got.State != Cancelled {
		t.Errorf("plan task = %+v, want cancelled for the waiting task", got)
	}
	if got := stateOf(t, h.r, other.ID); got.State != Queued {
		t.Errorf("other = %+v", got)
	}

	// Its own preemption does not pause the plan: once the other work is
	// done, the rotation picks up at the next poll.
	h.tick(10)
	if got := stateOf(t, h.r, other.ID); got.State != Done {
		t.Fatalf("other = %+v", got)
	}
	n := len(h.r.List())
	p.Step()
	if l := h.r.List(); len(l) != n+1 || l[n].Template != "gather_and_deposit" {
		t.Fatalf("after other work: %+v", l)
	}
}

func TestPlannerPausesWhenItsTaskIsStopped(t *testing.T) {
	h := newHarness(t, time.Minute, 0)
	plan, err := ParsePlan(planJSON)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPlanner(h.r, plan)
	p.now = func() time.Time { return h.clock }

	p.Step()
	h.tick(1)
	stopped := h.r.List()[0].ID
	if _, err := h.r.Cancel(stopped); err != nil {
		t.Fatal(err)
	}
	for _, wait := range []time.Duration{0, planPoll, 9 * time.Minute} {
		h.clock = h.clock.Add(wait)
		p.Step()
		if l := h.r.List(); len(l) != 1 {
			t.Fatalf("resubmitted %v after a stop: %+v", wait, l)
		}
	}
	h.clock = h.clock.Add(time.Minute)
	p.Step()
	if l := h.r.List(); len(l) != 2 || l[1].Template != "haul" {
		t.Fatalf("after the pause: %+v", l)
	}
}
