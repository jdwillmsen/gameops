package tactics

import (
	"math"
	"testing"
)

func TestDefaultsInsideBounds(t *testing.T) {
	d := Defaults()
	for _, n := range Names() {
		v, _ := d.Get(n)
		b := Bounds()[n]
		if v < b.Min || v > b.Max {
			t.Errorf("%s default %v outside %v", n, v, b)
		}
	}
}

func TestSetClampsAndRejectsUnknown(t *testing.T) {
	p := Defaults()
	p.Set("shotEvery", 1)
	if p.ShotEvery != 25 {
		t.Errorf("shotEvery = %v, want clamped to 25", p.ShotEvery)
	}
	if p.Set("nope", 1) {
		t.Error("unknown parameter accepted")
	}
}

func TestParseRejectsOtherVersionAndClamps(t *testing.T) {
	if _, err := Parse([]byte(`{"version":99,"name":"x"}`)); err == nil {
		t.Error("version 99 accepted")
	}
	pr, err := Parse([]byte(`{"version":1,"name":"x","default":{"healSafe":500}}`))
	if err != nil || pr.Default.HealSafe != 8 {
		t.Errorf("healSafe = %v, err %v, want 8", pr.Default.HealSafe, err)
	}
}

func TestForFallsBackToDefault(t *testing.T) {
	pr := NewProfile("p")
	o := Defaults()
	o.KiteBackoff = 8
	pr.Overrides = map[Family]Params{FamilyCreeper: o}
	if pr.For(FamilyCreeper).KiteBackoff != 8 || pr.For(FamilyZombie).KiteBackoff != 5 {
		t.Error("override or fallback wrong")
	}
}

func TestScoreOrdersOutcomes(t *testing.T) {
	win := Scorecard{Outcome: OutcomeWin, Seconds: 30, Kills: 4}
	hurt := Scorecard{Outcome: OutcomeWin, Seconds: 30, Kills: 4, Deaths: map[string]int{"a": 3}}
	loss := Scorecard{Outcome: OutcomeLoss, Kills: 4}
	if !(win.Score() > hurt.Score() && hurt.Score() > loss.Score()) {
		t.Errorf("scores %v %v %v not ordered", win.Score(), hurt.Score(), loss.Score())
	}
	if (Scorecard{Outcome: OutcomeAborted, Kills: 9}).Score() != 0 {
		t.Error("aborted fight scored")
	}
}

func TestTacticForDefaultsToNone(t *testing.T) {
	pr := NewProfile("p")
	if pr.TacticFor(FamilyCreeper) != "" {
		t.Error("a new profile selects a tactic")
	}
	pr.Tactics = map[Family]string{FamilyCreeper: "creeper/backoff"}
	if pr.TacticFor(FamilyCreeper) != "creeper/backoff" || pr.TacticFor(FamilyZombie) != "" {
		t.Error("tactic lookup wrong")
	}
}

func TestSetRefusesNaNAndKeepsTheField(t *testing.T) {
	p := Defaults()
	if p.Set("shotEvery", math.NaN()) {
		t.Error("NaN accepted")
	}
	if p.ShotEvery != Defaults().ShotEvery {
		t.Errorf("shotEvery = %v after a refused NaN, want it unchanged", p.ShotEvery)
	}
}

func TestParseKeepsDefaultsForOmittedFields(t *testing.T) {
	d := Defaults()
	pr, err := Parse([]byte(`{"version":1,"name":"x","default":{"healSafe":5},"overrides":{"creeper":{"kiteBackoff":7}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Default.HealSafe != 5 || pr.Default.FocusRange != d.FocusRange || pr.Default.ShotEvery != d.ShotEvery {
		t.Errorf("default = %+v, want only healSafe changed", pr.Default)
	}
	if o := pr.Overrides[FamilyCreeper]; o.KiteBackoff != 7 || o.ShotEvery != d.ShotEvery {
		t.Errorf("override = %+v, want only kiteBackoff changed", o)
	}
	pr, err = Parse([]byte(`{"version":1,"name":"bare"}`))
	if err != nil || pr.Default != d {
		t.Errorf("profile without a default = %+v, err %v, want the shipped defaults", pr.Default, err)
	}
}
