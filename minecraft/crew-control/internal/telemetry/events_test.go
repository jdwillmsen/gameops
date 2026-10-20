package telemetry

import (
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
