package telemetry

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func threatObs(hostiles int) observation {
	return observation{online: true, threat: &Threat{Hostiles: hostiles, Players: 2}}
}

func TestThreatHighFiresOnReachingThreeAndRearmsBelowTwo(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	step := func(o observation) []Event {
		now = now.Add(time.Second)
		return tr.observe(o, now)
	}
	tr.observe(threatObs(0), now)

	for _, n := range []int{1, 2} {
		if got := step(threatObs(n)); len(got) != 0 {
			t.Fatalf("%d hostiles announced %v", n, kinds(got))
		}
	}
	got := step(threatObs(3))
	if len(got) != 1 || got[0].Kind != "threat_high" || got[0].Level != "warn" || got[0].Text != "several hostile mobs nearby" ||
		got[0].Attrs["hostiles"] != 3 || got[0].Attrs["players"] != 2 {
		t.Fatalf("threat_high = %+v", got)
	}
	if !tr.underAttack {
		t.Fatal("not marked under attack")
	}
	// Staying high, dipping to two, and rising again is one episode.
	for _, n := range []int{5, 2, 3, 9} {
		if got := step(threatObs(n)); len(got) != 0 {
			t.Fatalf("%d hostiles re-announced %v", n, kinds(got))
		}
	}
	step(threatObs(1))
	if tr.underAttack {
		t.Fatal("still under attack at one hostile")
	}
	if got := step(threatObs(4)); len(got) != 1 || got[0].Kind != "threat_high" {
		t.Fatalf("a new episode was not announced: %v", kinds(got))
	}
}

func TestAnUnreportedThreatChangesNothing(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(threatObs(0), now)
	tr.observe(threatObs(5), now.Add(time.Second))
	if got := tr.observe(observation{online: true}, now.Add(2*time.Second)); len(got) != 0 || !tr.underAttack {
		t.Fatalf("a poll without counts changed the state: %v under attack %v", kinds(got), tr.underAttack)
	}
}

func TestThreatAtStartIsABaselineAndOfflineClearsIt(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	if got := tr.observe(threatObs(6), now); len(got) != 0 {
		t.Fatalf("baseline announced %v", kinds(got))
	}
	if !tr.underAttack {
		t.Fatal("a unit already surrounded at start is under attack")
	}
	tr.observe(observation{online: false}, now.Add(time.Second))
	if tr.underAttack {
		t.Fatal("an offline unit is still under attack")
	}
}

func stateWithThreat(h, p int) string {
	return strings.Replace(stateKnownWithTask, `"unique_id":"-4294967290"},`,
		`"unique_id":"-4294967290"},"threat":{"hostiles":`+itoa(h)+`,"players":`+itoa(p)+`},`, 1)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestPollerRaisesThreatHighAndOnlyTheBitIsPublic(t *testing.T) {
	unit := newFakeUnit(t, stateWithThreat(0, 0))
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	got := make(chan []Born, 64)
	p.OnEvents = func(b []Born) { got <- b }
	run(t, p)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	if p.Snapshot()[0].UnderAttack {
		t.Fatal("under attack with nothing near")
	}

	unit.set(http.StatusOK, stateWithThreat(7, 3))
	var ev Born
	for end := time.Now().Add(3 * time.Second); ev.Kind == "" && time.Now().Before(end); {
		select {
		case b := <-got:
			for _, e := range b {
				if e.Kind == "threat_high" {
					ev = e
				}
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if ev.Unit != "crew-01" || ev.Attrs["hostiles"] != 7 || ev.Attrs["players"] != 3 {
		t.Fatalf("threat_high = %+v", ev)
	}
	waitFor(t, "the public flag", func() bool { return p.Snapshot()[0].UnderAttack })

	raw, _ := json.Marshal(p.Snapshot()[0])
	if !strings.Contains(string(raw), `"under_attack":true`) || strings.Contains(string(raw), "hostiles") || strings.Contains(string(raw), "players") {
		t.Errorf("public status = %s", raw)
	}
	for _, e := range p.Events() {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "players") {
			t.Errorf("the public feed carries the player count: %s", raw)
		}
		if e.Kind == "threat_high" && strings.ContainsAny(e.Text, "0123456789") {
			t.Errorf("the public feed text carries a count: %q", e.Text)
		}
	}

	unit.set(http.StatusOK, stateWithThreat(0, 0))
	waitFor(t, "the flag to clear", func() bool { return !p.Snapshot()[0].UnderAttack })
	unit.set(500, stateWithThreat(5, 0))
	waitFor(t, "offline", func() bool { return !p.Snapshot()[0].Online })
	if p.Snapshot()[0].UnderAttack {
		t.Error("an offline unit is under attack")
	}
}

func TestACountOutOfRangeIsBoundedAndAnOlderUnitHasNone(t *testing.T) {
	unit := newFakeUnit(t, stateWithThreat(500, -4))
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	got := make(chan []Born, 64)
	p.OnEvents = func(b []Born) { got <- b }
	run(t, p)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	if !p.Snapshot()[0].UnderAttack {
		t.Error("a huge count is still a threat at its cap")
	}

	old := newFakeUnit(t, stateKnownWithTask)
	q := startPoller(t, rosterFor(t, old.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return q.Snapshot()[0].Online })
	if q.Snapshot()[0].UnderAttack {
		t.Error("a unit that reports no threat is under attack")
	}
}
