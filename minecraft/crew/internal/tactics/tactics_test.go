package tactics

import "testing"

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
