package insight

import (
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

// worldGen builds an hour of completions: per unit-hour done events of one
// template, spread evenly, for each unit.
type worldGen struct {
	g   gen
	end time.Time
}

func (w *worldGen) done(unit, template string, n int) []unitlog.Event {
	var out []unitlog.Event
	start := w.end.Add(-time.Hour)
	for i := 0; i < n; i++ {
		e := w.g.ev(unit, start.Add(time.Duration(i)*time.Hour/time.Duration(n+1)+time.Second), unitlog.KindTaskDone, "")
		e.Template = template
		out = append(out, e)
	}
	return out
}

// fourWorlds: three alike (30 gathers per unit-hour) and one that is not.
func fourWorlds(oddRate int) (Input, *worldGen) {
	wg := &worldGen{end: t0.Add(2 * time.Hour)}
	worlds := map[string]string{"a1": "alpha", "a2": "alpha", "b1": "beta", "b2": "beta", "c1": "gamma", "c2": "gamma", "d1": "delta", "d2": "delta"}
	var es []unitlog.Event
	for u, w := range worlds {
		rate := 30
		if w == "delta" {
			rate = oddRate
		}
		es = append(es, wg.done(u, "gather_and_deposit", rate)...)
	}
	return Input{Now: wg.end, Events: es, Worlds: worlds}, wg
}

func TestWorldDivergenceFlagsTheWorldOutsideTheBandOfTheOthers(t *testing.T) {
	cfg := DefaultConfig()
	in, _ := fourWorlds(8)
	fs := WorldDivergence(cfg, in)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
	f := fs[0]
	if f.Rule != RuleWorldDiverges || f.World != "delta" || f.Unit != "" || f.Severity != SevWarn {
		t.Errorf("finding = %+v", f)
	}
	if f.Detail != "gather_and_deposit completions per unit-hour is well below the other worlds for the last hour" {
		t.Errorf("detail = %q", f.Detail)
	}
	if len(f.Evidence) == 0 || len(f.Evidence) > maxEvidence {
		t.Errorf("evidence = %v", f.Evidence)
	}
	// The numbers are in Attrs, never in the sentence the public reads.
	for _, r := range f.Detail {
		if r >= '0' && r <= '9' || r == '%' {
			t.Fatalf("detail carries a number: %q", f.Detail)
		}
	}
	ts, ok := f.Attrs["templates"].([]map[string]any)
	if !ok || len(ts) != 1 || ts[0]["median_of_others"] != 30.0 || ts[0]["other_worlds"] != 3 || ts[0]["rate"] != 8.0 {
		t.Errorf("attrs = %+v", f.Attrs)
	}
}

func TestWorldDivergenceNamesAWorldRunningAheadToo(t *testing.T) {
	in, _ := fourWorlds(80)
	fs := WorldDivergence(DefaultConfig(), in)
	if len(fs) != 1 || fs[0].World != "delta" || fs[0].Detail != "gather_and_deposit completions per unit-hour is well above the other worlds for the last hour" {
		t.Fatalf("findings = %+v", fs)
	}
}

func TestWorldsInsideTheBandAreQuiet(t *testing.T) {
	for _, rate := range []int{30, 22, 38} {
		in, _ := fourWorlds(rate)
		if fs := WorldDivergence(DefaultConfig(), in); len(fs) != 0 {
			t.Errorf("rate %d: %+v", rate, fs)
		}
	}
}

func TestWorldDivergenceIgnoresADifferenceTooSmallToMatter(t *testing.T) {
	cfg := DefaultConfig()
	wg := &worldGen{end: t0.Add(2 * time.Hour)}
	worlds := map[string]string{"a": "alpha", "b": "beta", "c": "gamma", "d": "delta"}
	var es []unitlog.Event
	for u, w := range worlds {
		n := 6
		if w == "delta" {
			n = 5 // 5 against 6 is outside no band that means anything
		}
		es = append(es, wg.done(u, "gather_and_deposit", n)...)
	}
	cfg.R6MinUnitHours = 0.5
	if fs := WorldDivergence(cfg, Input{Now: wg.end, Events: es, Worlds: worlds}); len(fs) != 0 {
		t.Errorf("a one-per-hour difference was flagged: %+v", fs)
	}
}

func TestWorldDivergenceNeedsTwoOtherWorlds(t *testing.T) {
	in, wg := fourWorlds(2)
	cfg := DefaultConfig()
	// Drop to three worlds: delta would have exactly two others, which is enough...
	three := Input{Now: in.Now, Worlds: map[string]string{}}
	for u, w := range in.Worlds {
		if w != "gamma" {
			three.Worlds[u] = w
		}
	}
	for _, e := range in.Events {
		if _, ok := three.Worlds[e.Unit]; ok {
			three.Events = append(three.Events, e)
		}
	}
	if fs := WorldDivergence(cfg, three); len(fs) != 1 || fs[0].World != "delta" {
		t.Fatalf("three worlds: %+v", fs)
	}
	// ...but with only two worlds, or one, nobody has "the others".
	two := Input{Now: in.Now, Worlds: map[string]string{"a1": "alpha", "a2": "alpha", "d1": "delta", "d2": "delta"}}
	for _, e := range in.Events {
		if _, ok := two.Worlds[e.Unit]; ok {
			two.Events = append(two.Events, e)
		}
	}
	if fs := WorldDivergence(cfg, two); len(fs) != 0 {
		t.Errorf("two worlds: %+v", fs)
	}
	one := Input{Now: in.Now, Worlds: map[string]string{"a1": "lab", "a2": "lab"}, Events: wg.done("a1", "gather_and_deposit", 1)}
	if fs := WorldDivergence(cfg, one); len(fs) != 0 {
		t.Errorf("one world: %+v", fs)
	}
	// And the other worlds must each have run the template: two that never
	// finished one say nothing about a third that did.
	idle := Input{Now: in.Now, Worlds: in.Worlds}
	for _, e := range in.Events {
		if in.Worlds[e.Unit] == "delta" || in.Worlds[e.Unit] == "alpha" {
			idle.Events = append(idle.Events, e)
		}
	}
	if fs := WorldDivergence(cfg, idle); len(fs) != 0 {
		t.Errorf("only one other world ran it: %+v", fs)
	}
}

func TestWorldDivergenceWaitsForEnoughUnitHours(t *testing.T) {
	in, _ := fourWorlds(2)
	// One unit per world is only one unit-hour: give the rule a two-hour bar.
	cfg := DefaultConfig()
	cfg.R6MinUnitHours = 3
	if fs := WorldDivergence(cfg, in); len(fs) != 0 {
		t.Errorf("a world with too little exposure was judged: %+v", fs)
	}
}

func TestOnlineTimeIsReadFromConnectionEvents(t *testing.T) {
	start, end := t0, t0.Add(time.Hour)
	ev := func(at time.Duration, kind string) unitlog.Event {
		return unitlog.Event{Unit: "u", At: start.Add(at), Kind: kind}
	}
	for name, c := range map[string]struct {
		es   []unitlog.Event
		want float64
	}{
		"no connection events: connected throughout": {nil, 1},
		"offline half way through":                   {[]unitlog.Event{ev(30*time.Minute, unitlog.KindOffline)}, 0.5},
		"came online half way through":               {[]unitlog.Event{ev(30*time.Minute, unitlog.KindOnline)}, 0.5},
		"offline then back":                          {[]unitlog.Event{ev(10*time.Minute, unitlog.KindOffline), ev(40*time.Minute, unitlog.KindOnline)}, 0.5},
		"offline before the window, back in it":      {[]unitlog.Event{ev(-10*time.Minute, unitlog.KindOffline), ev(15*time.Minute, unitlog.KindOnline)}, 0.75},
		"online before the window, offline in it":    {[]unitlog.Event{ev(-10*time.Minute, unitlog.KindOnline), ev(15*time.Minute, unitlog.KindOffline)}, 0.25},
		"offline before the window and still":        {[]unitlog.Event{ev(-10*time.Minute, unitlog.KindOffline)}, 0},
	} {
		if got := onlineHours(c.es, start, end); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s: %v hours, want %v", name, got, c.want)
		}
	}
}

func TestWorldsFallBackToTheWorldAnEventCarries(t *testing.T) {
	in := Input{Worlds: map[string]string{"a": "alpha"}, Events: []unitlog.Event{
		{Unit: "a", World: "ignored"}, {Unit: "b", World: "beta"}, {Unit: "c"},
	}}
	got := worldsOf(in)
	if len(got) != 2 || got["a"] != "alpha" || got["b"] != "beta" {
		t.Errorf("worlds = %v", got)
	}
}

func TestTheTrackerKeepsOneInsightPerWorldAndHidesNumbersFromTheSentence(t *testing.T) {
	tr := NewTracker()
	f := func(world string, attrs map[string]any) Finding {
		return Finding{Rule: RuleWorldDiverges, World: world, Severity: SevWarn, Detail: "x", Attrs: attrs}
	}
	if !tr.Apply(t0, []Finding{f("a", map[string]any{"r": 1.0}), f("b", map[string]any{"r": 2.0})}) {
		t.Fatal("two new insights were not a change")
	}
	// New numbers alone are not a change worth waking the console for.
	if tr.Apply(t0.Add(time.Second), []Finding{f("a", map[string]any{"r": 1.5}), f("b", map[string]any{"r": 2.5})}) {
		t.Error("moving attrs counted as a change")
	}
	list := tr.List()
	if len(list) != 2 || list[0].Attrs["r"] == 1.0 || list[1].Attrs["r"] == 1.0 {
		t.Errorf("list = %+v, want two insights with refreshed attrs", list)
	}
	tr.Apply(t0.Add(2*time.Second), []Finding{f("a", nil)})
	var resolved []string
	for _, in := range tr.List() {
		if in.State == StateResolved {
			resolved = append(resolved, in.World)
		}
	}
	if len(resolved) != 1 || resolved[0] != "b" {
		t.Errorf("resolved = %v, want only b", resolved)
	}
}
