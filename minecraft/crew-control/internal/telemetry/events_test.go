package telemetry

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func f32(v float32) *float32 { return &v }

func kinds(es []Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Kind)
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFirstObservationIsABaseline(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	got := tr.observe(observation{online: true, health: f32(3), hunger: f32(2), curID: "a", curTemplate: "haul"}, now)
	if len(got) != 0 {
		t.Fatalf("baseline announced %v", kinds(got))
	}
	// Already low at the baseline: not announced again while it stays low.
	if got := tr.observe(observation{online: true, health: f32(3), hunger: f32(2), curID: "a", curTemplate: "haul"}, now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("steady state announced %v", kinds(got))
	}
}

func TestTransitions(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true, health: f32(20), hunger: f32(20)}, now)

	step := func(o observation) []string {
		now = now.Add(time.Second)
		return kinds(tr.observe(o, now))
	}
	if got := step(observation{online: true, health: f32(5), hunger: f32(20)}); !same(got, []string{"low_health"}) {
		t.Errorf("low health: %v", got)
	}
	if got := step(observation{online: true, health: f32(4), hunger: f32(20)}); len(got) != 0 {
		t.Errorf("still low: %v", got)
	}
	if got := step(observation{online: true, dead: true, health: f32(0), hunger: f32(20)}); !same(got, []string{"died"}) {
		t.Errorf("death: %v", got)
	}
	if got := step(observation{online: true, health: f32(20), hunger: f32(3)}); !same(got, []string{"respawned", "low_hunger"}) {
		t.Errorf("respawn: %v", got)
	}
	if got := step(observation{online: true, health: f32(20), hunger: f32(3), curID: "t1", curTemplate: "haul"}); !same(got, []string{"task_started"}) {
		t.Errorf("start: %v", got)
	}
	done := []recentTask{{ID: "t1", State: "done"}}
	if got := step(observation{online: true, health: f32(20), hunger: f32(3), recent: done}); !same(got, []string{"task_done"}) {
		t.Errorf("done: %v", got)
	}
	step(observation{online: true, health: f32(20), hunger: f32(3), curID: "t2", curTemplate: "gather_and_deposit"})
	failed := []recentTask{{ID: "t2", State: "failed", Reason: "kit_empty"}}
	es := tr.observe(observation{online: true, health: f32(20), hunger: f32(3), recent: failed}, now.Add(time.Second))
	if len(es) != 1 || es[0].Kind != "task_failed" || es[0].Level != "crit" || es[0].Text != "failed gather_and_deposit: kit_empty" {
		t.Errorf("failed: %+v", es)
	}
	if got := step(observation{}); !same(got, []string{"offline"}) {
		t.Errorf("offline: %v", got)
	}
	if got := step(observation{}); len(got) != 0 {
		t.Errorf("still offline: %v", got)
	}
	if got := step(observation{online: true}); !same(got, []string{"online"}) {
		t.Errorf("online: %v", got)
	}
}

func TestStuckIsReportedOnceAndResetsOnProgress(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	run := func(progress int) []string {
		return kinds(tr.observe(observation{online: true, curID: "t", curTemplate: "gather_and_deposit", curProgress: progress}, now))
	}
	run(1)
	now = now.Add(stuckAfter - time.Second)
	if got := run(1); len(got) != 0 {
		t.Fatalf("early: %v", got)
	}
	now = now.Add(2 * time.Second)
	if got := run(1); !same(got, []string{"stuck"}) {
		t.Fatalf("stuck: %v", got)
	}
	now = now.Add(time.Minute)
	if got := run(1); len(got) != 0 {
		t.Fatalf("repeat: %v", got)
	}
	run(2)
	now = now.Add(stuckAfter + time.Second)
	if got := run(2); !same(got, []string{"stuck"}) {
		t.Fatalf("stuck again after progress: %v", got)
	}
}

func TestWalkingIsProgressButShufflingIsNot(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	at := func(x float32) []string {
		return kinds(tr.observe(observation{online: true, curID: "t", curTemplate: "goto", pos: [3]float32{x, 64, 0}, posKnown: true}, now))
	}
	at(0)
	for x := float32(4); x <= 40; x += 4 {
		now = now.Add(stuckAfter / 2)
		if got := at(x); len(got) != 0 {
			t.Fatalf("walking at x=%v reported %v", x, got)
		}
	}
	now = now.Add(stuckAfter / 2)
	at(41)
	now = now.Add(stuckAfter / 2)
	if got := at(42); !same(got, []string{"stuck"}) {
		t.Fatalf("shuffling within a few blocks: %v", got)
	}
}

func TestFinishedTaskMissingFromRecentHasUnknownOutcome(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true, curID: "t1", curTemplate: "haul"}, now)
	others := []recentTask{{ID: "q1", State: "queued"}, {ID: "q2", State: "queued"}}
	es := tr.observe(observation{online: true, curID: "q1", curTemplate: "haul", recent: others}, now.Add(time.Second))
	if len(es) != 2 || es[0].Kind != "task_ended" || es[0].Level != "info" || es[0].Text != "haul ended, outcome unknown" {
		t.Fatalf("got %+v", es)
	}
}

func TestFeedIsBoundedAndSequenced(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), nil, time.Second)
	ch, cancel := p.SubscribeEvents()
	defer cancel()
	p.mu.Lock()
	for i := 0; i < feedSize+30; i++ {
		p.appendEventLocked(0, Event{Kind: "online", Level: "ok", Text: "connected"})
	}
	p.mu.Unlock()
	got := p.Events()
	if len(got) != feedSize {
		t.Fatalf("feed holds %d, want %d", len(got), feedSize)
	}
	if got[0].Seq != 31 || got[len(got)-1].Seq != feedSize+30 {
		t.Errorf("seq range %d..%d", got[0].Seq, got[len(got)-1].Seq)
	}
	if got[0].Unit != "crew-01" || got[0].Name != "Unit1" {
		t.Errorf("identity %q %q", got[0].Unit, got[0].Name)
	}
	if len(ch) != cap(ch) {
		t.Errorf("a slow reader holds %d, want a full buffer of %d", len(ch), cap(ch))
	}
}

func TestTaskEventsCarryTheWorkItem(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true, curID: "t1", curTemplate: "haul"}, now)
	es := tr.observe(observation{online: true, curID: "t2", curTemplate: "goto"}, now.Add(time.Second))
	if len(es) != 2 || es[0].Kind != "task_ended" || es[0].Task != "t1" ||
		es[1].Kind != "task_started" || es[1].Task != "t2" {
		t.Fatalf("change: %+v", es)
	}
	// t2 leaves with no verdict in the recent list: the outcome-unknown
	// event still names it.
	es = tr.observe(observation{online: true, recent: []recentTask{{ID: "t1", State: "done"}}}, now.Add(2*time.Second))
	if len(es) != 1 || es[0].Kind != "task_ended" || es[0].Task != "t2" {
		t.Fatalf("unknown outcome: %+v", es)
	}
	es = tr.observe(observation{online: true, curID: "t3", curTemplate: "haul"}, now.Add(3*time.Second))
	if len(es) != 1 || es[0].Kind != "task_started" || es[0].Task != "t3" {
		t.Fatalf("start t3: %+v", es)
	}
	es = tr.observe(observation{online: true, recent: []recentTask{{ID: "t3", State: "done"}}}, now.Add(4*time.Second))
	if len(es) != 1 || es[0].Kind != "task_done" || es[0].Task != "t3" {
		t.Fatalf("done: %+v", es)
	}
}

func TestOnEventsReceivesBornEventsWithPositions(t *testing.T) {
	// The same known position as the state fixture, but a different task, so
	// one poll produces a task change and with it events.
	next := strings.Replace(stateKnownWithTask, `"id":"run"`, `"id":"run2"`, 1)
	unit := newFakeUnit(t, stateKnownWithTask)
	// OnEvents is set before Run, as main sets it: the poll loop reads the
	// hook unsynchronised.
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	// Buffered, and never read from the poll loop: the sink must not block.
	got := make(chan []Born, 8)
	p.OnEvents = func(b []Born) { got <- b }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })

	unit.set(http.StatusOK, next)
	born := readBorn(t, got)
	if len(born) != 2 {
		t.Fatalf("born %d, want the ended run and the started run2: %+v", len(born), born)
	}
	kinds := born[0].Kind + "," + born[1].Kind
	if kinds != "task_ended,task_started" {
		t.Fatalf("kinds %s", kinds)
	}
	for _, b := range born {
		if b.Unit != "crew-01" || b.Name != "Unit1" || b.Task != "run" && b.Task != "run2" {
			t.Fatalf("identity or task wrong: %+v", b)
		}
		if b.Seq == 0 || !b.Pos.Known || b.Pos.X != 12.5 || b.Pos.Y != 64.62 || b.Pos.Z != -3 {
			t.Fatalf("context wrong: %+v", b)
		}
	}
}

func readBorn(t *testing.T, ch chan []Born) []Born {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(3 * time.Second):
		t.Fatal("no born events arrived")
		return nil
	}
}

func TestFinishedTaskEventsCarryTemplateAndDuration(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	made := time.Unix(990, 0)
	tr.observe(observation{online: true, curID: "t1", curTemplate: "haul"}, now)
	done := recentTask{ID: "t1", State: "done", Created: made, Updated: made.Add(200 * time.Millisecond)}
	es := tr.observe(observation{online: true, recent: []recentTask{done}}, now.Add(time.Second))
	if len(es) != 1 || es[0].Template != "haul" || es[0].Took != 200*time.Millisecond {
		t.Fatalf("done: %+v", es)
	}

	// A unit that sent no usable times leaves the duration unknown rather
	// than inventing one, so nothing downstream can mistake it for instant.
	for name, r := range map[string]recentTask{
		"no times":       {ID: "t2", State: "done"},
		"clock reversed": {ID: "t2", State: "done", Created: made, Updated: made.Add(-time.Second)},
	} {
		tr.observe(observation{online: true, curID: "t2", curTemplate: "goto"}, now.Add(2*time.Second))
		es = tr.observe(observation{online: true, recent: []recentTask{r}}, now.Add(3*time.Second))
		if len(es) != 1 || es[0].Took != 0 {
			t.Fatalf("%s: took %v, want unknown", name, es[0].Took)
		}
	}
}
