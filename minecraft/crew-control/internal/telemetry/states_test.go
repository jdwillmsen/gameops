package telemetry

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestStateOfOrdersWhatNeedsActingOnFirst(t *testing.T) {
	task := &TaskSummary{ID: "t"}
	for name, c := range map[string]struct {
		s     Status
		stuck bool
		want  string
	}{
		"offline wins over a stale task": {Status{Online: false, Task: task, Dead: true}, true, StateOffline},
		"dead wins over a listed task":   {Status{Online: true, Dead: true, Task: task}, true, StateDead},
		"stuck on a task":                {Status{Online: true, Task: task}, true, StateStuck},
		"stuck flag without a task":      {Status{Online: true}, true, StateIdle},
		"working":                        {Status{Online: true, Task: task}, false, StateWorking},
		"idle":                           {Status{Online: true}, false, StateIdle},
	} {
		if got := stateOf(c.s, c.stuck); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestOnPollGetsEveryUnitsStateEachTick(t *testing.T) {
	working := newFakeUnit(t, stateKnownWithTask)
	idle := newFakeUnit(t, stateUnknownNoTask)
	gone := newFakeUnit(t, stateUnknownNoTask)
	gone.set(500, "")
	r := rosterFor(t, working.server.URL, idle.server.URL, gone.server.URL)

	var mu sync.Mutex
	var last []UnitState
	p := NewPoller(r, &http.Client{}, 20*time.Millisecond)
	p.OnPoll = func(_ time.Time, s []UnitState) {
		mu.Lock()
		last = s
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	want := []UnitState{{"crew-01", StateWorking}, {"crew-02", StateDead}, {"crew-03", StateOffline}}
	waitFor(t, "every unit's state", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(last) != len(want) {
			return false
		}
		for i := range want {
			if last[i] != want[i] {
				return false
			}
		}
		return true
	})
}
