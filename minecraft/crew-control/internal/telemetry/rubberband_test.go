package telemetry

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func corr(n uint64) observation { return observation{online: true, corrections: &n} }

// feed steps a fresh track once a second through running totals and returns
// the events of every step.
func feedCorrections(totals ...uint64) [][]Event {
	var tr track
	now := time.Unix(1000, 0)
	out := make([][]Event, len(totals))
	for i, n := range totals {
		now = now.Add(time.Second)
		out[i] = tr.observe(corr(n), now)
	}
	return out
}

func TestRubberbandingFiresOnceWhenCorrectionsPassTheRateAndNotBefore(t *testing.T) {
	// 100 is the baseline; nine more in the window is one short.
	for i, got := range feedCorrections(100, 103, 106, 109) {
		if len(got) != 0 {
			t.Fatalf("step %d announced %v below the rate", i, kinds(got))
		}
	}
	steps := feedCorrections(100, 104, 108, 110)
	if len(steps[3]) != 1 {
		t.Fatalf("ten corrections in the window: %v", kinds(steps[3]))
	}
	ev := steps[3][0]
	if ev.Kind != "rubberbanding" || ev.Level != "warn" || ev.ReasonCode != ReasonMovementCorrected {
		t.Errorf("event = %+v", ev)
	}
	if ev.Attrs["corrections"] != uint64(10) || ev.Attrs["window_s"] != 30 {
		t.Errorf("attrs = %v, want the count and window as operator-only detail", ev.Attrs)
	}
}

func TestRubberbandingTextCarriesNoCountSoThePublicFeedCannotLeakOne(t *testing.T) {
	ev := feedCorrections(0, 40)[1][0]
	raw, _ := json.Marshal(ev)
	if regexp.MustCompile(`[0-9]`).MatchString(ev.Text) || strings.Contains(string(raw), "corrections") || strings.Contains(string(raw), "window") {
		t.Errorf("public event = %s (text %q)", raw, ev.Text)
	}
}

func TestRubberbandingIsOneEpisodeUntilTheServerLetsGo(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	total := uint64(0)
	step := func(add uint64, after time.Duration) []Event {
		total += add
		now = now.Add(after)
		return tr.observe(corr(total), now)
	}
	step(0, time.Second)
	if got := step(12, time.Second); len(got) != 1 {
		t.Fatalf("first burst: %v", kinds(got))
	}
	// More corrections while it is still going on are the same episode.
	if got := step(30, 5*time.Second); len(got) != 0 {
		t.Fatalf("a continuing episode re-announced: %v", kinds(got))
	}
	// Quiet for longer than the window clears it...
	if got := step(0, 2*rubberbandWindow); len(got) != 0 || tr.rubberbanding {
		t.Fatalf("still rubberbanding after a quiet window: %v", kinds(got))
	}
	// ...so the next burst is a new episode.
	if got := step(11, time.Second); len(got) != 1 {
		t.Fatalf("second episode: %v", kinds(got))
	}
}

func TestSlowCorrectionsNeverAddUpToRubberbanding(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	total := uint64(0)
	tr.observe(corr(total), now)
	// One correction every 10 s is a busy server, not a unit being pulled back.
	for range 60 {
		total++
		now = now.Add(10 * time.Second)
		if got := tr.observe(corr(total), now); len(got) != 0 {
			t.Fatalf("a slow trickle was announced: %v", kinds(got))
		}
	}
}

func TestACorrectionCountThatWentDownOrIsMissingOnlySetsABaseline(t *testing.T) {
	// A restart: 500 then 3 is not a negative burst, and 3 then 14 is eleven.
	steps := feedCorrections(500, 3, 14)
	if len(steps[1]) != 0 || len(steps[2]) != 1 {
		t.Fatalf("restart handling: %v then %v", kinds(steps[1]), kinds(steps[2]))
	}

	var tr track
	now := time.Unix(1000, 0)
	tr.observe(corr(0), now)
	// An older unit sends no count: nothing is raised and nothing is kept.
	if got := tr.observe(observation{online: true}, now.Add(time.Second)); len(got) != 0 || tr.corrections != nil {
		t.Fatalf("a missing count: %v, kept %v", kinds(got), tr.corrections)
	}
	// Its first count back is a baseline, not 5000 corrections at once.
	if got := tr.observe(corr(5000), now.Add(2*time.Second)); len(got) != 0 {
		t.Fatalf("a count after a gap was read as growth: %v", kinds(got))
	}
}

func TestGoingOfflineForgetsTheCorrectionBaseline(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(corr(0), now)
	tr.observe(corr(8), now.Add(time.Second))
	tr.observe(observation{online: false}, now.Add(2*time.Second))
	if tr.corrections != nil || tr.correctionWindow != nil || tr.rubberbanding {
		t.Fatalf("offline kept %+v", tr)
	}
	tr.observe(corr(900), now.Add(3*time.Second))
	if got := tr.observe(corr(905), now.Add(4*time.Second)); len(got) != 0 {
		t.Fatalf("growth from before the gap leaked in: %v", kinds(got))
	}
}

func TestStateReportsTheRunningCorrectionCountFromTheUnitsMovementField(t *testing.T) {
	body := func(extra string) string {
		return strings.Replace(stateKnownWithTask, `"tasks":`, extra+`"tasks":`, 1)
	}
	unit := newFakeUnit(t, body(`"movement":{"corrections":41},`))
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	read := func() *uint64 {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.statesLocked()[0].Corrections
	}
	waitFor(t, "the count", func() bool { c := read(); return c != nil && *c == 41 })

	unit.set(200, body(`"movement":{"corrections":57},`))
	waitFor(t, "the count to follow", func() bool { c := read(); return c != nil && *c == 57 })

	// An older unit has no movement object at all.
	unit.set(200, stateKnownWithTask)
	waitFor(t, "no count from an older unit", func() bool { return read() == nil })
}

func TestARubberbandingUnitRaisesTheEventThroughThePoller(t *testing.T) {
	n := 0
	unit := newFakeUnit(t, stateKnownWithTask)
	set := func() {
		n += 6
		unit.set(200, strings.Replace(stateKnownWithTask, `"tasks":`, `"movement":{"corrections":`+itoa(n)+`},"tasks":`, 1))
	}
	set()
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	waitFor(t, "rubberbanding", func() bool {
		set()
		for _, e := range p.Events() {
			if e.Kind == "rubberbanding" {
				return true
			}
		}
		return false
	})
}
