package insight

import (
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

var t0 = time.Date(2026, 10, 23, 6, 21, 0, 0, time.UTC)

type gen struct{ id int64 }

func (g *gen) ev(unit string, at time.Time, kind, reason string) unitlog.Event {
	g.id++
	return unitlog.Event{ID: g.id, Unit: unit, At: at, Kind: kind, Reason: reason}
}

// steady emits perMin completions every minute from..to.
func (g *gen) steady(unit string, from, to time.Time, perMin int) []unitlog.Event {
	var out []unitlog.Event
	for m := from; m.Before(to); m = m.Add(time.Minute) {
		for i := 0; i < perMin; i++ {
			out = append(out, g.ev(unit, m.Add(time.Duration(i)*time.Second), unitlog.KindTaskDone, ""))
		}
	}
	return out
}

func (g *gen) fails(unit string, from time.Time, n int, reason string) []unitlog.Event {
	var out []unitlog.Event
	for i := 0; i < n; i++ {
		out = append(out, g.ev(unit, from.Add(time.Duration(i)*20*time.Second), unitlog.KindTaskFailed, reason))
	}
	return out
}

func TestZeroSuccess(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		name string
		mk   func(g *gen) []unitlog.Event
		now  time.Time
		want int
	}{
		{"three same-reason failures, quiet five minutes", func(g *gen) []unitlog.Event {
			return g.fails("u", t0, 6, "no axe")
		}, t0.Add(5 * time.Minute), 6},
		{"too early to call", func(g *gen) []unitlog.Event {
			return g.fails("u", t0, 6, "no axe")
		}, t0.Add(4 * time.Minute), 0},
		{"only two failures", func(g *gen) []unitlog.Event {
			return g.fails("u", t0, 2, "no axe")
		}, t0.Add(6 * time.Minute), 0},
		{"mixed reasons break the run", func(g *gen) []unitlog.Event {
			return append(g.fails("u", t0, 3, "no axe"), g.fails("u", t0.Add(time.Minute), 2, "no path")...)
		}, t0.Add(6 * time.Minute), 0},
		{"a recent success clears it", func(g *gen) []unitlog.Event {
			es := g.fails("u", t0, 5, "no axe")
			return append(es, g.ev("u", t0.Add(4*time.Minute), unitlog.KindTaskDone, ""))
		}, t0.Add(6 * time.Minute), 0},
		{"old success, fresh failures", func(g *gen) []unitlog.Event {
			es := []unitlog.Event{g.ev("u", t0, unitlog.KindTaskDone, "")}
			return append(es, g.fails("u", t0.Add(8*time.Minute), 3, "no axe")...)
		}, t0.Add(9 * time.Minute), 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := ZeroSuccess(cfg, c.now, "u", c.mk(&gen{}))
			got := 0
			if f != nil {
				got = len(f.Evidence)
			}
			if got != c.want {
				t.Fatalf("evidence = %d, want %d", got, c.want)
			}
		})
	}
}

func TestThroughput(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		name string
		mk   func(g *gen) []unitlog.Event
		now  time.Time
		want bool
	}{
		{"steady unit is quiet", func(g *gen) []unitlog.Event {
			return g.steady("u", t0, t0.Add(30*time.Minute), 4)
		}, t0.Add(30 * time.Minute), false},
		{"drop to nothing alarms", func(g *gen) []unitlog.Event {
			return g.steady("u", t0, t0.Add(25*time.Minute), 4)
		}, t0.Add(30 * time.Minute), true},
		{"a one-completion dip in one minute is not an alarm", func(g *gen) []unitlog.Event {
			es := g.steady("u", t0, t0.Add(29*time.Minute), 4)
			return append(es, g.steady("u", t0.Add(29*time.Minute), t0.Add(30*time.Minute), 3)...)
		}, t0.Add(30 * time.Minute), false},
		{"too little history gives no verdict", func(g *gen) []unitlog.Event {
			return g.steady("u", t0, t0.Add(5*time.Minute), 4)
		}, t0.Add(12 * time.Minute), false},
		{"a unit that never worked has no baseline to drop from", func(g *gen) []unitlog.Event {
			return g.fails("u", t0, 3, "no axe")
		}, t0.Add(30 * time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Throughput(cfg, c.now, "u", c.mk(&gen{}))
			if (f != nil) != c.want {
				t.Fatalf("alarm = %v, want %v", f != nil, c.want)
			}
		})
	}
}

func TestLongTasks(t *testing.T) {
	cfg := DefaultConfig()
	mk := func(d time.Duration, n int) []time.Duration {
		out := make([]time.Duration, n)
		for i := range out {
			out[i] = d
		}
		return out
	}
	cases := []struct {
		name    string
		hist    []time.Duration
		elapsed time.Duration
		want    bool
	}{
		{"zero MAD haul: slightly slower is not flagged", mk(200*time.Millisecond, 20), 900 * time.Millisecond, false},
		{"zero MAD haul: far slower is flagged", mk(200*time.Millisecond, 20), 5 * time.Second, true},
		{"gather within range", mk(17*time.Second, 20), 20 * time.Second, false},
		{"gather far over range", mk(17*time.Second, 20), 60 * time.Second, true},
		{"too few samples", mk(17*time.Second, 5), 600 * time.Second, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			open := []OpenTask{{StartID: 1, Unit: "u", Template: "tpl", Since: t0}}
			got := LongTasks(cfg, t0.Add(c.elapsed), open, map[string][]time.Duration{"tpl": c.hist})
			if (len(got) == 1) != c.want {
				t.Fatalf("flagged = %v, want %v", len(got) == 1, c.want)
			}
		})
	}
}

func TestEvaluateCommonModeSuppressesUnits(t *testing.T) {
	cfg := DefaultConfig()
	g := &gen{}
	var es []unitlog.Event
	units := []string{"a", "b", "c", "d"}
	for _, u := range units {
		es = append(es, g.steady(u, t0, t0.Add(25*time.Minute), 4)...)
	}
	now := t0.Add(30 * time.Minute)
	for i := 0; i < 6; i++ {
		es = append(es, g.ev("a", now.Add(-time.Duration(i)*10*time.Second), unitlog.KindSession, ""))
	}
	got := Evaluate(cfg, Input{Now: now, Events: es}, nil)
	rules := map[string]int{}
	for _, f := range got {
		rules[f.Rule]++
	}
	if rules[RuleCommonMode] != 1 || rules[RuleThroughput] != 0 {
		t.Fatalf("rules = %v, want one R4 and no per-unit R2", rules)
	}
}

func TestEvaluateWithoutSessionErrorsKeepsUnitInsights(t *testing.T) {
	cfg := DefaultConfig()
	g := &gen{}
	var es []unitlog.Event
	for _, u := range []string{"a", "b", "c", "d"} {
		es = append(es, g.steady(u, t0, t0.Add(25*time.Minute), 4)...)
	}
	got := Evaluate(cfg, Input{Now: t0.Add(30 * time.Minute), Events: es}, nil)
	n := 0
	for _, f := range got {
		if f.Rule == RuleCommonMode {
			t.Fatal("R4 without session errors")
		}
		if f.Rule == RuleThroughput {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("R2 count = %d, want 4", n)
	}
}

func TestEvaluateHaulersStayQuiet(t *testing.T) {
	// Hundreds of completions a minute are normal for a hauler, and a
	// fleet-wide baseline would have flagged them; per-unit ones do not.
	g := &gen{}
	es := g.steady("hauler", t0, t0.Add(30*time.Minute), 30)
	if got := Evaluate(DefaultConfig(), Input{Now: t0.Add(30 * time.Minute), Events: es}, nil); len(got) != 0 {
		t.Fatalf("findings = %v", got)
	}
}
