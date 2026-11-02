package telemetry

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func totals(swings, hits, deaths uint64, damage float64) *FightTotals {
	return &FightTotals{Swings: swings, Hits: hits, Deaths: deaths, Damage: damage}
}

func fightObs(f *FightTotals) observation { return observation{online: true, fight: f} }

func TestFightTotalsGrowthAndRestart(t *testing.T) {
	d, ok := totals(10, 6, 1, 7.5).Since(*totals(4, 2, 1, 2))
	if !ok || *totals(6, 4, 0, 5.5) != d {
		t.Errorf("growth = %+v ok=%v", d, ok)
	}
	for name, prev := range map[string]*FightTotals{
		"swings": totals(11, 0, 0, 0),
		"hits":   totals(0, 7, 0, 0),
		"deaths": totals(0, 0, 2, 0),
		"damage": totals(0, 0, 0, 7.6),
	} {
		if _, ok := totals(10, 6, 1, 7.5).Since(*prev); ok {
			t.Errorf("a decrease in %s was taken as growth", name)
		}
	}
}

func TestFightWireRefusesWhatCannotBeACount(t *testing.T) {
	var w *fightWire
	if w.totals() != nil {
		t.Error("an absent fight produced totals")
	}
	bad := &fightWire{}
	bad.Total.DamageTaken = -1
	if bad.totals() != nil {
		t.Error("negative damage produced totals")
	}
	good := &fightWire{}
	good.Total.Swings, good.Total.HitsLanded, good.Total.DamageTaken, good.Total.Deaths = 3, 2, 1.5, 1
	if got := good.totals(); got == nil || *got != *totals(3, 2, 1, 1.5) {
		t.Errorf("totals = %+v", got)
	}
}

type fightRun struct {
	t   *testing.T
	tr  track
	now time.Time
}

func newFightRun(t *testing.T, first *FightTotals) *fightRun {
	r := &fightRun{t: t, now: time.Unix(1000, 0)}
	if got := r.tr.observe(fightObs(first), r.now); len(got) != 0 {
		t.Fatalf("baseline announced %v", kinds(got))
	}
	return r
}

func (r *fightRun) poll(after time.Duration, f *FightTotals) []Event {
	r.now = r.now.Add(after)
	return r.tr.observe(fightObs(f), r.now)
}

func fightEnded(t *testing.T, es []Event) Event {
	t.Helper()
	var found []Event
	for _, e := range es {
		if e.Kind == "fight_ended" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("fight_ended events = %d in %v", len(found), kinds(es))
	}
	return found[0]
}

func TestAFightThatLandsBlowsAndSurvivesIsAWin(t *testing.T) {
	r := newFightRun(t, totals(100, 60, 2, 40))
	if got := r.poll(time.Second, totals(103, 61, 2, 41.5)); len(got) != 0 {
		t.Fatalf("a fight under way announced %v", kinds(got))
	}
	if got := r.poll(time.Second, totals(106, 63, 2, 41.5)); len(got) != 0 {
		t.Fatalf("announced mid-fight: %v", kinds(got))
	}
	if got := r.poll(9*time.Second, totals(106, 63, 2, 41.5)); len(got) != 0 {
		t.Fatalf("announced before the quiet period: %v", kinds(got))
	}
	e := fightEnded(t, r.poll(2*time.Second, totals(106, 63, 2, 41.5)))
	if e.Level != "ok" || e.Text != "fight ended: win" || e.Attrs["swings"] != uint64(6) || e.Attrs["hits"] != uint64(3) ||
		e.Attrs["damage"] != 1.5 || e.Attrs["outcome"] != "win" {
		t.Fatalf("fight_ended = %+v", e)
	}
	// Announced once.
	if got := r.poll(time.Second, totals(106, 63, 2, 41.5)); len(got) != 0 {
		t.Fatalf("announced twice: %v", kinds(got))
	}
}

func TestADeathEndsTheFightAtOnceAsALoss(t *testing.T) {
	r := newFightRun(t, totals(0, 0, 0, 0))
	r.poll(time.Second, totals(4, 1, 0, 8))
	e := fightEnded(t, r.poll(time.Second, totals(5, 1, 1, 20)))
	if e.Attrs["outcome"] != "loss" || e.Level != "warn" || e.Attrs["swings"] != uint64(5) || e.Attrs["damage"] != 20.0 {
		t.Fatalf("fight_ended = %+v", e)
	}
}

func TestADeathWithNoFightIsNotAFight(t *testing.T) {
	r := newFightRun(t, totals(5, 5, 0, 10))
	if got := r.poll(time.Second, totals(5, 5, 1, 10)); len(got) != 0 {
		t.Fatalf("a fall death announced %v", kinds(got))
	}
}

func TestAFightWithNoLandedBlowIsADisengagement(t *testing.T) {
	r := newFightRun(t, totals(0, 0, 0, 0))
	r.poll(time.Second, totals(3, 0, 0, 0))
	e := fightEnded(t, r.poll(11*time.Second, totals(3, 0, 0, 0)))
	if e.Attrs["outcome"] != "disengaged" || e.Level != "info" || e.Attrs["hits"] != uint64(0) {
		t.Fatalf("fight_ended = %+v", e)
	}

	r = newFightRun(t, totals(0, 0, 0, 0))
	r.poll(time.Second, totals(0, 0, 0, 4))
	if e := fightEnded(t, r.poll(11*time.Second, totals(0, 0, 0, 4))); e.Attrs["outcome"] != "disengaged" || e.Attrs["swings"] != uint64(0) {
		t.Fatalf("hurt without swinging: %+v", e)
	}
}

func TestARestartMidFightEndsItAsOtherAndRebaselines(t *testing.T) {
	r := newFightRun(t, totals(50, 30, 1, 10))
	r.poll(time.Second, totals(53, 31, 1, 12))
	e := fightEnded(t, r.poll(time.Second, totals(1, 0, 0, 0)))
	if e.Attrs["outcome"] != "other" {
		t.Fatalf("fight_ended = %+v", e)
	}
	// The new process's totals are the new baseline: growth from them is a
	// fresh fight, not a negative one.
	r.poll(time.Second, totals(4, 1, 0, 2))
	if e := fightEnded(t, r.poll(11*time.Second, totals(4, 1, 0, 2))); e.Attrs["swings"] != uint64(3) || e.Attrs["outcome"] != "win" {
		t.Fatalf("the fight after the restart: %+v", e)
	}
}

func TestARestartWithNoFightOpenSaysNothing(t *testing.T) {
	r := newFightRun(t, totals(50, 30, 1, 10))
	if got := r.poll(time.Second, totals(0, 0, 0, 0)); len(got) != 0 {
		t.Fatalf("a restart announced %v", kinds(got))
	}
}

func TestAUnitThatReportsNoFightHasNoFightEvents(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		if got := tr.observe(fightObs(nil), now.Add(time.Duration(i)*time.Second)); len(got) != 0 {
			t.Fatalf("an older unit announced %v", kinds(got))
		}
	}
}

func TestGoingOfflineMidFightEndsItAsOther(t *testing.T) {
	r := newFightRun(t, totals(0, 0, 0, 0))
	r.poll(time.Second, totals(4, 2, 0, 3))
	r.now = r.now.Add(time.Second)
	got := r.tr.observe(observation{online: false}, r.now)
	if e := fightEnded(t, got); e.Attrs["outcome"] != "other" {
		t.Fatalf("fight_ended = %+v", e)
	}
	// Back online, the totals are a baseline again.
	if got := r.poll(time.Second, totals(90, 50, 0, 3)); len(got) > 1 {
		t.Fatalf("history after a gap announced %v", kinds(got))
	}
	if r.tr.fight != nil {
		t.Fatal("a fight was opened from history")
	}
}

func stateWithFight(swings, hits, deaths int, damage string) string {
	return strings.Replace(stateKnownWithTask, `"recent":[]}}`,
		`"recent":[],"fight":{"total":{"swings":`+itoa(swings)+`,"hits_landed":`+itoa(hits)+`,"damage_taken":`+damage+`,"deaths":`+itoa(deaths)+`},"current":{}}}}`, 1)
}

func TestPollerEmitsFightEndedAndExposesTheTotals(t *testing.T) {
	unit := newFakeUnit(t, stateWithFight(10, 5, 0, "3.5"))
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	got := make(chan []Born, 64)
	p.OnEvents = func(b []Born) { got <- b }
	var ls lastStates
	p.OnPoll = ls.set
	run(t, p)
	waitFor(t, "the baseline", func() bool {
		s := ls.get()
		return len(s) == 1 && s[0].Fight != nil && *s[0].Fight == *totals(10, 5, 0, 3.5)
	})

	unit.set(http.StatusOK, stateWithFight(16, 9, 1, "9"))
	var ev Born
	for end := time.Now().Add(3 * time.Second); ev.Kind == "" && time.Now().Before(end); {
		select {
		case b := <-got:
			for _, e := range b {
				if e.Kind == "fight_ended" {
					ev = e
				}
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if ev.Unit != "crew-01" || ev.Attrs["outcome"] != "loss" || ev.Attrs["swings"] != uint64(6) || ev.Attrs["hits"] != uint64(4) || ev.Attrs["damage"] != 5.5 {
		t.Fatalf("fight_ended = %+v", ev)
	}

	unit.set(500, stateWithFight(16, 9, 1, "9"))
	waitFor(t, "offline", func() bool { return !p.Snapshot()[0].Online })
	waitFor(t, "the totals to clear", func() bool { s := ls.get(); return len(s) == 1 && s[0].Fight == nil })
}

func TestAnOlderUnitWithoutTheFightFieldReportsNone(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	var ls lastStates
	p.OnPoll = ls.set
	run(t, p)
	waitFor(t, "a poll", func() bool { return len(ls.get()) == 1 && p.Snapshot()[0].Online })
	if s := ls.get(); s[0].Fight != nil {
		t.Fatalf("fight = %+v", s[0].Fight)
	}
}
