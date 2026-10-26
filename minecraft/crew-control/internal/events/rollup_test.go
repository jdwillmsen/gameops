package events

import (
	"encoding/json"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func start(unit string, at time.Time, task, template string) (Event, Hint) {
	return Event{Unit: unit, At: at, Kind: "task_started", Severity: SevInfo}, Hint{Task: task, Template: template}
}

func finish(unit string, at time.Time, kind, task, template string, took time.Duration) (Event, Hint) {
	return Event{Unit: unit, At: at, Kind: kind, Severity: SevOK}, Hint{Task: task, Template: template, Took: took}
}

func kinds(ev []Event) []string {
	out := make([]string, len(ev))
	for i, e := range ev {
		out[i] = e.Kind
	}
	return out
}

func rollupOf(t *testing.T, e Event) (count int, last time.Time, template string) {
	t.Helper()
	var p struct {
		Template string    `json:"template"`
		Count    int       `json:"count"`
		Last     time.Time `json:"last"`
	}
	if e.Kind != KindIdleRollup {
		t.Fatalf("not a roll-up: %+v", e)
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Count, p.Last, p.Template
}

func TestIdleHaulPollsCollapseToOneRow(t *testing.T) {
	c := NewCollapser()
	var out []Event
	for i := range 30 {
		at := t0.Add(time.Duration(i) * 2 * time.Second)
		e, h := start("crew-03", at, "t"+string(rune('A'+i)), "haul")
		out = append(out, c.Push(e, h)...)
		e, h = finish("crew-03", at.Add(time.Second), "task_done", "t"+string(rune('A'+i)), "haul", 200*time.Millisecond)
		out = append(out, c.Push(e, h)...)
	}
	if len(out) != 0 {
		t.Fatalf("idle polls stored one by one: %v", kinds(out))
	}
	out = c.Flush()
	if len(out) != 1 {
		t.Fatalf("flush gave %d rows, want 1", len(out))
	}
	count, last, tpl := rollupOf(t, out[0])
	if count != 30 || tpl != "haul" || out[0].Unit != "crew-03" {
		t.Fatalf("roll-up: count %d template %q unit %q", count, tpl, out[0].Unit)
	}
	if !out[0].At.Equal(t0.Add(time.Second)) || !last.Equal(t0.Add(29*2*time.Second+time.Second)) {
		t.Fatalf("first %v last %v", out[0].At, last)
	}
	if out[0].Severity != SevInfo {
		t.Fatalf("severity %q", out[0].Severity)
	}
}

// What is never idle: each of these is information and must keep its row.
func TestOnlyQuickSuccessfulPollingTemplatesAreIdle(t *testing.T) {
	cases := map[string]struct {
		kind, template string
		took           time.Duration
	}{
		"slow haul":            {"task_done", "haul", IdleMax + time.Millisecond},
		"unknown duration":     {"task_done", "haul", 0},
		"failed haul":          {"task_failed", "haul", 100 * time.Millisecond},
		"cancelled haul":       {"task_cancelled", "haul", 100 * time.Millisecond},
		"outcome unknown haul": {"task_ended", "haul", 100 * time.Millisecond},
		"quick gather":         {"task_done", "gather_and_deposit", 100 * time.Millisecond},
		"quick goto":           {"task_done", "goto", 100 * time.Millisecond},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewCollapser()
			e, h := start("crew-03", t0, "a", tc.template)
			out := c.Push(e, h)
			e, h = finish("crew-03", t0.Add(time.Second), tc.kind, "a", tc.template, tc.took)
			out = append(out, c.Push(e, h)...)
			out = append(out, c.Flush()...)
			want := []string{"task_started", tc.kind}
			if got := kinds(out); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("stored %v, want %v", got, want)
			}
		})
	}
}

func TestExactlyIdleMaxIsStillIdle(t *testing.T) {
	c := NewCollapser()
	e, h := finish("crew-03", t0, "task_done", "a", "haul", IdleMax)
	if out := c.Push(e, h); len(out) != 0 {
		t.Fatalf("a haul at the limit was stored: %v", kinds(out))
	}
}

// A real event ends the streak, in order: the roll-up first, because it
// began earlier, then the event.
func TestRealEventClosesTheRollupInOrder(t *testing.T) {
	c := NewCollapser()
	for i := range 3 {
		task := string(rune('a' + i))
		e, h := start("crew-03", t0.Add(time.Duration(i)*time.Second), task, "haul")
		c.Push(e, h)
		e, h = finish("crew-03", t0.Add(time.Duration(i)*time.Second), "task_done", task, "haul", time.Millisecond)
		c.Push(e, h)
	}
	out := c.Push(Event{Unit: "crew-03", At: t0.Add(10 * time.Second), Kind: "died", Severity: SevCrit}, Hint{})
	if got := kinds(out); len(got) != 2 || got[0] != KindIdleRollup || got[1] != "died" {
		t.Fatalf("got %v", got)
	}
	if n, _, _ := rollupOf(t, out[0]); n != 3 {
		t.Fatalf("count %d, want 3", n)
	}
	// The streak is over: the next idle poll opens a new one.
	e, h := finish("crew-03", t0.Add(12*time.Second), "task_done", "z", "haul", time.Millisecond)
	c.Push(e, h)
	out = c.Flush()
	if n, _, _ := rollupOf(t, out[0]); len(out) != 1 || n != 1 {
		t.Fatalf("second streak: %+v", out)
	}
}

func TestHeldStartIsReleasedWhenTheTaskTurnsOutReal(t *testing.T) {
	c := NewCollapser()
	e, h := start("crew-03", t0, "a", "haul")
	if out := c.Push(e, h); len(out) != 0 {
		t.Fatalf("a start was stored before its verdict: %v", kinds(out))
	}
	// A different task's start arrives: the first never finished in view.
	e, h = start("crew-03", t0.Add(time.Second), "b", "haul")
	out := c.Push(e, h)
	if got := kinds(out); len(got) != 1 || got[0] != "task_started" || !out[0].At.Equal(t0) {
		t.Fatalf("got %v", got)
	}
	// And one that is never answered is released by the tick.
	if out := c.Expire(t0.Add(time.Second + holdMax - time.Millisecond)); len(out) != 0 {
		t.Fatalf("released early: %v", kinds(out))
	}
	if out := c.Expire(t0.Add(time.Second + holdMax)); len(out) != 1 || out[0].Kind != "task_started" {
		t.Fatalf("not released on time: %v", kinds(out))
	}
	if out := c.Flush(); len(out) != 0 {
		t.Fatalf("released twice: %v", kinds(out))
	}
}

func TestRollupHasABoundedWindow(t *testing.T) {
	c := NewCollapser()
	var out []Event
	for i := range 3 {
		e, h := finish("crew-03", t0.Add(time.Duration(i)*40*time.Second), "task_done", "t", "haul", time.Millisecond)
		out = append(out, c.Push(e, h)...)
	}
	// 0 s and 40 s share a window; 80 s opens the next.
	if len(out) != 1 {
		t.Fatalf("got %d closed rows, want 1", len(out))
	}
	if n, _, _ := rollupOf(t, out[0]); n != 2 {
		t.Fatalf("first window held %d, want 2", n)
	}
	// A quiet unit's open window is closed by the tick, not stranded.
	if got := c.Expire(t0.Add(80*time.Second + rollupWindow)); len(got) != 1 {
		t.Fatalf("expire closed %d rows, want 1", len(got))
	}
}

func TestUnitsCollapseIndependently(t *testing.T) {
	c := NewCollapser()
	e, h := finish("crew-03", t0, "task_done", "a", "haul", time.Millisecond)
	c.Push(e, h)
	// Another unit's real event does not close crew-03's streak.
	if out := c.Push(Event{Unit: "crew-04", At: t0, Kind: "died", Severity: SevCrit}, Hint{}); len(out) != 1 || out[0].Unit != "crew-04" {
		t.Fatalf("got %+v", out)
	}
	e, h = finish("crew-03", t0.Add(time.Second), "task_done", "b", "haul", time.Millisecond)
	c.Push(e, h)
	out := c.Flush()
	if n, _, _ := rollupOf(t, out[0]); len(out) != 1 || n != 2 {
		t.Fatalf("got %+v", out)
	}
}

// The rule's headline number: a hauler's hour of polls is a few dozen rows,
// not thousands, while one real failure in the middle keeps its row.
func TestAnHourOfIdlePollingStaysSmall(t *testing.T) {
	c := NewCollapser()
	var out []Event
	for i := range 1800 {
		at := t0.Add(time.Duration(i) * 2 * time.Second)
		task := "t" + time.Duration(i).String()
		e, h := start("crew-03", at, task, "haul")
		out = append(out, c.Push(e, h)...)
		kind, took := "task_done", 200*time.Millisecond
		if i == 900 {
			kind = "task_failed"
		}
		e, h = finish("crew-03", at.Add(time.Second), kind, task, "haul", took)
		out = append(out, c.Push(e, h)...)
	}
	out = append(out, c.Flush()...)
	if len(out) > 70 {
		t.Fatalf("an hour of idle polling became %d rows", len(out))
	}
	var failed, polls int
	for _, e := range out {
		switch e.Kind {
		case "task_failed":
			failed++
		case KindIdleRollup:
			n, _, _ := rollupOf(t, e)
			polls += n
		}
	}
	// 1799 idle polls are all accounted for, nothing lost to the folding.
	if failed != 1 || polls != 1799 {
		t.Fatalf("failed %d, polls counted %d", failed, polls)
	}
}

func TestIsIdlePollFollowsTheRollupRule(t *testing.T) {
	for name, c := range map[string]struct {
		kind, template string
		took           time.Duration
		want           bool
	}{
		"quick empty haul":      {"task_done", "haul", 300 * time.Millisecond, true},
		"at the limit":          {"task_done", "haul", IdleMax, true},
		"a haul that moved":     {"task_done", "haul", IdleMax + time.Millisecond, false},
		"unknown duration":      {"task_done", "haul", 0, false},
		"quick goto did work":   {"task_done", "goto", 100 * time.Millisecond, false},
		"a failed haul is real": {"task_failed", "haul", 100 * time.Millisecond, false},
		"a cancelled haul":      {"task_cancelled", "haul", 100 * time.Millisecond, false},
	} {
		if got := IsIdlePoll(c.kind, c.template, c.took); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestInventoryChangeLeavesAHeldIdleStartAlone(t *testing.T) {
	c := NewCollapser()
	e, h := start("crew-01", t0, "t1", "haul")
	if out := c.Push(e, h); len(out) != 0 {
		t.Fatalf("start released: %v", kinds(out))
	}
	out := c.Push(Event{Unit: "crew-01", At: t0.Add(time.Second), Kind: "inventory_changed", Severity: SevInfo}, Hint{})
	if got := kinds(out); len(got) != 1 || got[0] != "inventory_changed" {
		t.Fatalf("out = %v, want only the inventory change", got)
	}
	e, h = finish("crew-01", t0.Add(2*time.Second), "task_done", "t1", "haul", time.Second)
	if out := c.Push(e, h); len(out) != 0 {
		t.Errorf("the idle pair no longer folds: %v", kinds(out))
	}
}
