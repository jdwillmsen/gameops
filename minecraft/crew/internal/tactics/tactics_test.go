package tactics

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
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

func TestSetIgnoresNonFiniteValues(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		p := Defaults()
		p.ShotEvery = 41
		if p.Set("shotEvery", v) {
			t.Errorf("Set accepted %v", v)
		}
		if p.ShotEvery != 41 {
			t.Errorf("shotEvery = %v after Set(%v), want the previous 41", p.ShotEvery, v)
		}
	}
	p := Defaults()
	if !p.Set("shotEvery", 40) || p.ShotEvery != 40 {
		t.Errorf("a finite value was refused: %v", p.ShotEvery)
	}
	nan := Defaults()
	nan.HealSafe = math.NaN()
	if got := nan.Clamp().HealSafe; got != Defaults().HealSafe {
		t.Errorf("Clamp left %v for a NaN, want the default", got)
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

func TestScoreFastWinWithLittleDamageBeatsASlowOne(t *testing.T) {
	fast := Scorecard{Outcome: OutcomeWin, Seconds: 20, Kills: 4, DamageTaken: 2, DamageDealt: 80}
	slow := Scorecard{Outcome: OutcomeWin, Seconds: 200, Kills: 4, DamageTaken: 2, DamageDealt: 80}
	bled := Scorecard{Outcome: OutcomeWin, Seconds: 20, Kills: 4, DamageTaken: 20, DamageDealt: 80}
	slowBled := Scorecard{Outcome: OutcomeWin, Seconds: 200, Kills: 4, DamageTaken: 18, DamageDealt: 80}
	if !(fast.Score() > slow.Score() && fast.Score() > bled.Score() && fast.Score() > slowBled.Score()) {
		t.Errorf("fast %v, slow %v, bled %v, slow and bled %v: the fast clean win must lead",
			fast.Score(), slow.Score(), bled.Score(), slowBled.Score())
	}
	// A whole health bar lost is worth more than a minute of speed.
	oneBar := Scorecard{Outcome: OutcomeWin, Seconds: 30, DamageTaken: 20}
	minuteSlower := Scorecard{Outcome: OutcomeWin, Seconds: 90}
	if oneBar.Score() >= minuteSlower.Score() {
		t.Errorf("a bar lost scores %v, a minute slower %v", oneBar.Score(), minuteSlower.Score())
	}
}

func TestScoreCreditsDamageDealtOnlyToFightsNotWon(t *testing.T) {
	won := Scorecard{Outcome: OutcomeWin, Seconds: 30, DamageDealt: 400}
	if won.Score() != (Scorecard{Outcome: OutcomeWin, Seconds: 30}).Score() {
		t.Error("damage dealt moved the score of a won fight")
	}
	far := Scorecard{Outcome: OutcomeTimeout, DamageDealt: 18}
	none := Scorecard{Outcome: OutcomeTimeout}
	if far.Score() <= none.Score() {
		t.Errorf("a timeout that got far scores %v, one that did nothing %v", far.Score(), none.Score())
	}
	// However much a loss dealt, it stays below a timeout that dealt nothing,
	// and a timeout stays below a win.
	bigLoss := Scorecard{Outcome: OutcomeLoss, DamageDealt: 5000}
	if bigLoss.Score() >= none.Score() {
		t.Errorf("a loss dealing 5000 scores %v, above a timeout dealing none (%v)", bigLoss.Score(), none.Score())
	}
	if (Scorecard{Outcome: OutcomeTimeout, DamageDealt: 5000}).Score() >= (Scorecard{Outcome: OutcomeWin, Seconds: 600, DamageTaken: 40}).Score() {
		t.Error("a timeout outscored a slow, bloody win")
	}
	if (Scorecard{Outcome: OutcomeLoss, DamageDealt: -9}).Score() != (Scorecard{Outcome: OutcomeLoss}).Score() {
		t.Error("negative damage dealt counted against a loss")
	}
}

func TestTacticsForDefaultsToNoneAndKeepsOrder(t *testing.T) {
	pr := NewProfile("p")
	if len(pr.TacticsFor(FamilyCreeper)) != 0 {
		t.Error("a new profile selects a tactic")
	}
	pr.Tactics = map[Family][]string{FamilySpider: {"spider/ground", "spider/swarm"}}
	if got := pr.TacticsFor(FamilySpider); !reflect.DeepEqual(got, []string{"spider/ground", "spider/swarm"}) {
		t.Errorf("spider tactics = %v, want the listed order", got)
	}
	if len(pr.TacticsFor(FamilyZombie)) != 0 {
		t.Error("a family with no list got tactics")
	}
}

func TestABossOverrideAndTacticLoadClampedAndStayWithTheBoss(t *testing.T) {
	pr, err := Parse([]byte(`{"version":1,"name":"boss","default":{"focusRange":12,"healBelow":14,"healSafe":3,
		"pressRange":1.6,"regroupWithin":3,"kiteBackoff":5,"shotEvery":30,"arrowNear":3,"lineWidth":1.2,
		"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12},
		"overrides":{"boss":{"focusRange":12,"healBelow":99,"healSafe":3,"pressRange":1.6,"regroupWithin":3,
		"kiteBackoff":5,"shotEvery":60,"arrowNear":3,"lineWidth":1.2,"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12}},
		"tactics":{"boss":["boss/kiteburst"]}}`))
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
	if !reflect.DeepEqual(pr.TacticsFor(FamilyBoss), []string{"boss/kiteburst"}) || len(pr.TacticsFor(FamilyZombie)) != 0 {
		t.Error("boss tactic lookup wrong")
	}
}

func TestParseKeepsShippedValuesForAbsentFields(t *testing.T) {
	pr, err := Parse([]byte(`{"version":1,"name":"x","tactics":{"creeper":["creeper/backoff"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Default != Defaults() {
		t.Errorf("default = %+v, want the shipped values", pr.Default)
	}
	pr, err = Parse([]byte(`{"version":1,"name":"x","default":{"shotEvery":40}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.ShotEvery = 40
	if pr.Default != want {
		t.Errorf("default = %+v, want only shotEvery changed", pr.Default)
	}
}

func TestParseOverrideChangesOnlyTheNamedKnob(t *testing.T) {
	pr, err := Parse([]byte(`{"version":1,"name":"x","default":{"focusRange":9},"overrides":{"creeper":{"kiteBackoff":8}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := pr.Default
	want.KiteBackoff = 8
	if got := pr.For(FamilyCreeper); got != want {
		t.Errorf("creeper = %+v, want the profile default with kiteBackoff 8", got)
	}
	if pr.For(FamilyCreeper).FocusRange != 9 {
		t.Error("override lost the profile's own default")
	}
}

func TestParseAcceptsTheOldStringTacticAndTheList(t *testing.T) {
	pr, err := Parse([]byte(`{"version":1,"name":"x","tactics":{"creeper":"creeper/backoff","spider":["spider/ground","spider/swarm"],"zombie":""}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := pr.TacticsFor(FamilyCreeper); !reflect.DeepEqual(got, []string{"creeper/backoff"}) {
		t.Errorf("creeper = %v", got)
	}
	if got := pr.TacticsFor(FamilySpider); !reflect.DeepEqual(got, []string{"spider/ground", "spider/swarm"}) {
		t.Errorf("spider = %v", got)
	}
	if len(pr.TacticsFor(FamilyZombie)) != 0 {
		t.Error("an empty name became a tactic")
	}
	if _, err := Parse([]byte(`{"version":1,"name":"x","tactics":{"creeper":7}}`)); err == nil {
		t.Error("a number accepted as a tactic")
	}
	out, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"creeper":["creeper/backoff"]`) {
		t.Errorf("marshalled %s, want arrays", out)
	}
}
