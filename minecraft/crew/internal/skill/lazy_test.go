package skill

import (
	"context"
	"errors"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// stepper is a skill that runs for n steps and logs what is done to it.
type stepper struct {
	name     string
	n        int
	readyErr error
	log      *[]string
	steps    int
}

func (s *stepper) Name() string { return s.name }
func (s *stepper) Ready(belief.Snapshot) error {
	*s.log = append(*s.log, s.name+".ready")
	return s.readyErr
}
func (s *stepper) Step(context.Context, Actuator) (Status, error) {
	*s.log = append(*s.log, s.name+".step")
	s.steps++
	if s.steps > s.n {
		return Done, nil
	}
	return Running, nil
}
func (s *stepper) Stop(Actuator) { *s.log = append(*s.log, s.name+".stop") }
func (s *stepper) Resume()       { *s.log = append(*s.log, s.name+".resume") }

func TestLazyBuildsWhenItStartsFromTheSnapshotGiven(t *testing.T) {
	var log []string
	built := 0
	var seen belief.Snapshot
	l := NewLazy("chain", func() belief.Snapshot { return belief.Snapshot{} }, func(s belief.Snapshot) ([]Skill, error) {
		built++
		seen = s
		return []Skill{&stepper{name: "a", log: &log}}, nil
	})
	if built != 0 {
		t.Fatal("built before the task started")
	}
	if l.Name() != "chain" {
		t.Errorf("name before start = %s", l.Name())
	}
	start := belief.Snapshot{Inventory: []belief.Item{{Name: "coal", Count: 3}}}
	if err := l.Ready(start); err != nil || built != 1 || len(seen.Inventory) != 1 {
		t.Fatalf("Ready: err %v built %d seen %+v", err, built, seen)
	}
	if l.Name() != "a" {
		t.Errorf("name once running = %s", l.Name())
	}
}

func TestLazyRunsStepsInOrderAndSendsOneInputPerRunningStep(t *testing.T) {
	var log []string
	l := NewLazy("chain", func() belief.Snapshot { return belief.Snapshot{} }, func(belief.Snapshot) ([]Skill, error) {
		// b finishes without a Running step, so the hand-over to c must not
		// end the tick without an input.
		return []Skill{&stepper{name: "a", n: 1, log: &log}, &stepper{name: "b", n: 0, log: &log}, &stepper{name: "c", n: 1, log: &log}}, nil
	})
	if err := l.Ready(belief.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	var statuses []Status
	for i := 0; i < 6 && (len(statuses) == 0 || statuses[len(statuses)-1] == Running); i++ {
		st, err := l.Step(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, st)
	}
	want := []Status{Running, Running, Done}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %v", statuses)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("statuses = %v, want %v", statuses, want)
		}
	}
	got := ""
	for _, e := range log {
		got += e + " "
	}
	if want := "a.ready a.step a.step b.ready b.step c.ready c.step c.step "; got != want {
		t.Errorf("log = %s\nwant  %s", got, want)
	}
}

func TestLazyFailsWhenAStepIsNotReady(t *testing.T) {
	var log []string
	boom := errors.New("no coal in inventory")
	l := NewLazy("chain", func() belief.Snapshot { return belief.Snapshot{} }, func(belief.Snapshot) ([]Skill, error) {
		return []Skill{&stepper{name: "a", log: &log, readyErr: boom}}, nil
	})
	if err := l.Ready(belief.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if st, err := l.Step(context.Background(), nil); st != Failed || !errors.Is(err, boom) {
		t.Errorf("Step = %v, %v", st, err)
	}
}

func TestLazyReportsWhyThereIsNothingToDo(t *testing.T) {
	why := errors.New("the unit already holds the iron tools")
	l := NewLazy("chain", func() belief.Snapshot { return belief.Snapshot{} }, func(belief.Snapshot) ([]Skill, error) { return nil, why })
	if err := l.Ready(belief.Snapshot{}); !errors.Is(err, why) {
		t.Errorf("Ready = %v", err)
	}
	l = NewLazy("chain", func() belief.Snapshot { return belief.Snapshot{} }, func(belief.Snapshot) ([]Skill, error) { return nil, nil })
	if err := l.Ready(belief.Snapshot{}); err == nil {
		t.Error("an empty chain was accepted")
	}
}

func TestLazyStopsAndResumesTheRunningStep(t *testing.T) {
	var log []string
	l := NewLazy("chain", func() belief.Snapshot { return belief.Snapshot{} }, func(belief.Snapshot) ([]Skill, error) {
		return []Skill{&stepper{name: "a", n: 5, log: &log}}, nil
	})
	if err := l.Ready(belief.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	_, _ = l.Step(context.Background(), nil)
	l.Stop(nil)
	l.Resume()
	// Resumed, so the step is not asked to be Ready again.
	_, _ = l.Step(context.Background(), nil)
	want := []string{"a.ready", "a.step", "a.stop", "a.resume", "a.step"}
	if len(log) != len(want) {
		t.Fatalf("log = %v", log)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("log = %v, want %v", log, want)
		}
	}
}
