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

func TestABossOverrideAndTacticLoadClampedAndStayWithTheBoss(t *testing.T) {
	pr, err := Parse([]byte(`{"version":1,"name":"boss","default":{"focusRange":12,"healBelow":14,"healSafe":3,
		"pressRange":1.6,"regroupWithin":3,"kiteBackoff":5,"shotEvery":30,"arrowNear":3,"lineWidth":1.2,
		"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12},
		"overrides":{"boss":{"focusRange":12,"healBelow":99,"healSafe":3,"pressRange":1.6,"regroupWithin":3,
		"kiteBackoff":5,"shotEvery":60,"arrowNear":3,"lineWidth":1.2,"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12}},
		"tactics":{"boss":"boss/kiteburst"}}`))
	if err != nil {
		t.Fatal(err)
	}
	boss := pr.For(FamilyBoss)
	if boss.ShotEvery != 60 || boss.HealBelow != Bounds()["healBelow"].Max {
		t.Errorf("boss params = %+v, want shotEvery 60 and healBelow clamped to its bound", boss)
	}
	if pr.For(FamilyZombie) != pr.Default {
		t.Error("a boss override reached the zombie family")
	}
	if pr.TacticFor(FamilyBoss) != "boss/kiteburst" || pr.TacticFor(FamilyZombie) != "" {
		t.Error("boss tactic lookup wrong")
	}
}
