package telemetry

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClosedEnvKeepsOnlyTheClosedVocabulary(t *testing.T) {
	for _, tc := range []struct{ in, want LiveEnv }{
		{LiveEnv{DayPhase: "night", Biome: "swampland", Raining: true}, LiveEnv{DayPhase: "night", Biome: "swampland", Raining: true}},
		{LiveEnv{DayPhase: "midnight", Biome: "plains"}, LiveEnv{Biome: "plains"}},
		{LiveEnv{DayPhase: "<b>", Biome: "<img src=x onerror=alert(1)>"}, LiveEnv{Biome: "other"}},
		{LiveEnv{Biome: "Plains"}, LiveEnv{Biome: "other"}},
		{LiveEnv{Biome: strings.Repeat("a", 500)}, LiveEnv{Biome: "other"}},
		{LiveEnv{}, LiveEnv{}},
	} {
		if got := closedEnv(tc.in); got != tc.want {
			t.Errorf("closedEnv(%+v) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestDiedCarriesTheDayPhaseWhenKnown(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true}, now)
	got := tr.observe(observation{online: true, dead: true, dayPhase: "dusk"}, now.Add(time.Second))
	if len(got) != 1 || got[0].Kind != "died" || got[0].Attrs["day_phase"] != "dusk" {
		t.Fatalf("died = %+v", got)
	}

	tr.observe(observation{online: true}, now.Add(2*time.Second))
	got = tr.observe(observation{online: true, dead: true}, now.Add(3*time.Second))
	if len(got) != 1 || got[0].Kind != "died" {
		t.Fatalf("died = %+v", got)
	}
	if _, set := got[0].Attrs["day_phase"]; set {
		t.Errorf("a death with no clock got a phase: %v", got[0].Attrs)
	}
}

func stateWithEnv(env string) string {
	return strings.Replace(stateKnownWithTask, `"unique_id":"-4294967290"},`, `"unique_id":"-4294967290","env":`+env+`},`, 1)
}

func TestPublicStatusCarriesPhaseAndRainButNeverTheBiome(t *testing.T) {
	unit := newFakeUnit(t, stateWithEnv(`{"day_phase":"night","biome":"swampland","raining":true}`))
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })

	s := p.Snapshot()[0]
	if s.DayPhase != "night" || !s.Raining {
		t.Fatalf("status = %+v", s)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "swampland") || strings.Contains(string(raw), "biome") || !strings.Contains(string(raw), `"day_phase":"night"`) {
		t.Errorf("public status = %s", raw)
	}
	lv, _ := p.Live("crew-01")
	if lv.Env != (LiveEnv{DayPhase: "night", Biome: "swampland", Raining: true}) {
		t.Errorf("live env = %+v", lv.Env)
	}

	unit.set(http.StatusOK, stateWithEnv(`{"day_phase":"sometime","biome":"<x>","raining":false}`))
	waitFor(t, "the phase to clear", func() bool { return p.Snapshot()[0].DayPhase == "" })
	if lv, _ := p.Live("crew-01"); lv.Env.Biome != "other" {
		t.Errorf("live biome = %q, want other", lv.Env.Biome)
	}

	unit.set(500, stateWithEnv(`{"day_phase":"day","raining":true}`))
	waitFor(t, "offline", func() bool { return !p.Snapshot()[0].Online })
	if s := p.Snapshot()[0]; s.DayPhase != "" || s.Raining {
		t.Errorf("an offline unit kept its environment: %+v", s)
	}
}

func TestAnOlderUnitWithoutAnEnvHasNoPhase(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })
	if s := p.Snapshot()[0]; s.DayPhase != "" || s.Raining {
		t.Errorf("status = %+v", s)
	}
}
