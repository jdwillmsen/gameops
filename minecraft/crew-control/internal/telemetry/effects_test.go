package telemetry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func fx(id int, name string, amp int) LiveEffect {
	return LiveEffect{ID: id, Name: name, Amplifier: amp, Duration: 600}
}

func effectKinds(es []Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, fmt.Sprintf("%s:%v:%v", e.Kind, e.Attrs["effect"], e.Attrs["amplifier"]))
	}
	return out
}

func TestEffectsFirstPollIsABaseline(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	got := tr.observe(observation{online: true, effects: []LiveEffect{fx(19, "poison", 0)}}, now)
	if len(got) != 0 {
		t.Fatalf("baseline announced %v", effectKinds(got))
	}
	if got := tr.observe(observation{online: true, effects: []LiveEffect{fx(19, "poison", 0)}}, now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("steady state announced %v", effectKinds(got))
	}
}

func TestEffectGainedAndLost(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true}, now)

	got := tr.observe(observation{online: true, effects: []LiveEffect{fx(19, "poison", 1), fx(1, "speed", 0)}}, now.Add(time.Second))
	want := []string{"effect_gained:speed:0", "effect_gained:poison:1"}
	if !slices.Equal(effectKinds(got), want) {
		t.Fatalf("gained = %v, want %v", effectKinds(got), want)
	}
	if got[0].Level != "info" || got[1].Level != "warn" || got[1].Text != "gained poison" {
		t.Errorf("levels or text: %+v", got)
	}
	for _, e := range got {
		if strings.ContainsAny(e.Text, "0123456789") {
			t.Errorf("the text carries a number: %q", e.Text)
		}
	}

	// A longer or stronger dose of an effect already held is not news.
	if got := tr.observe(observation{online: true, effects: []LiveEffect{fx(19, "poison", 3), fx(1, "speed", 0)}}, now.Add(2*time.Second)); len(got) != 0 {
		t.Fatalf("a changed amplifier announced %v", effectKinds(got))
	}

	got = tr.observe(observation{online: true, effects: []LiveEffect{fx(1, "speed", 0)}}, now.Add(3*time.Second))
	if !slices.Equal(effectKinds(got), []string{"effect_lost:poison:3"}) || got[0].Level != "info" || got[0].Text != "lost poison" {
		t.Fatalf("lost = %+v", got)
	}
}

func TestAnUnknownEffectIsOtherAndFreeTextNeverReachesAnEvent(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true}, now)
	got := tr.observe(observation{online: true, effects: []LiveEffect{fx(99, "", 0), fx(98, "<script>alert(1)</script>", 999)}}, now.Add(time.Second))
	// In id order, with the amplifier bounded to a byte.
	if want := []string{"effect_gained:other:255", "effect_gained:other:0"}; !slices.Equal(effectKinds(got), want) {
		t.Fatalf("got %v, want %v", effectKinds(got), want)
	}
	for _, e := range got {
		if e.Text != "gained other" {
			t.Errorf("text = %q", e.Text)
		}
	}
	if got := closedEffects([]LiveEffect{fx(1, "speed", 0), fx(2, "speed", 1), fx(3, "<b>", 0), fx(4, "poison", 0)}); !slices.Equal(got, []string{"other", "poison", "speed"}) {
		t.Errorf("closed names = %v", got)
	}
}

func TestEffectsAfterAnOfflineSpellAreANewBaseline(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true, effects: []LiveEffect{fx(19, "poison", 0)}}, now)
	tr.observe(observation{online: false}, now.Add(time.Second))
	got := tr.observe(observation{online: true, effects: []LiveEffect{fx(19, "poison", 0)}}, now.Add(2*time.Second))
	if slices.ContainsFunc(got, func(e Event) bool { return strings.HasPrefix(e.Kind, "effect_") }) {
		t.Fatalf("a reconnect announced its effects: %v", effectKinds(got))
	}
}

func TestAnAbsurdEffectListIsBounded(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true}, now)
	var many []LiveEffect
	for i := 0; i < 5000; i++ {
		many = append(many, fx(i, "speed", 0))
	}
	if got := tr.observe(observation{online: true, effects: many}, now.Add(time.Second)); len(got) != maxEffects {
		t.Fatalf("%d events from 5000 effects, want %d", len(got), maxEffects)
	}
}

func TestHarmfulEffectsAreAClosedSubsetOfTheNames(t *testing.T) {
	for name := range harmfulEffects {
		if !effectNames[name] {
			t.Errorf("%s is harmful but not a known effect", name)
		}
	}
	if len(effectNames) != 36 {
		t.Errorf("%d effect names, want the units' 36", len(effectNames))
	}
	if !IsHarmfulEffect("poison") || IsHarmfulEffect("speed") || IsHarmfulEffect("other") {
		t.Error("harmful classification is wrong")
	}
}

func TestPublicStatusCarriesOnlyClosedEffectNames(t *testing.T) {
	body := strings.Replace(stateKnownWithTask,
		`"effects":[{"id":1,"name":"speed","amplifier":1,"duration":-1}]`,
		`"effects":[{"id":19,"name":"poison","amplifier":1,"duration":50},{"id":77,"name":"<img src=x>","amplifier":0,"duration":5}]`, 1)
	unit := newFakeUnit(t, body)
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })

	if got := p.Snapshot()[0].Effects; !slices.Equal(got, []string{"other", "poison"}) {
		t.Fatalf("effects = %v", got)
	}
	raw, _ := json.Marshal(p.Snapshot()[0])
	if strings.Contains(string(raw), "img") || !strings.Contains(string(raw), `"effects":["other","poison"]`) {
		t.Errorf("public status = %s", raw)
	}

	unit.set(http.StatusOK, strings.Replace(body, `"id":19,"name":"poison"`, `"id":20,"name":"wither"`, 1))
	waitFor(t, "effects to change", func() bool { return slices.Contains(p.Snapshot()[0].Effects, "wither") })

	unit.set(500, body)
	waitFor(t, "offline", func() bool { return !p.Snapshot()[0].Online })
	if got := p.Snapshot()[0].Effects; got != nil {
		t.Errorf("an offline unit kept its effects: %v", got)
	}
}

func TestPollerEmitsEffectEventsFromPolls(t *testing.T) {
	plain := strings.Replace(stateKnownWithTask, `"effects":[{"id":1,"name":"speed","amplifier":1,"duration":-1}]`, `"effects":[]`, 1)
	poisoned := strings.Replace(stateKnownWithTask, `"effects":[{"id":1,"name":"speed","amplifier":1,"duration":-1}]`, `"effects":[{"id":19,"name":"poison","amplifier":0,"duration":80}]`, 1)
	unit := newFakeUnit(t, plain)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	got := make(chan []Born, 64)
	p.OnEvents = func(b []Born) { got <- b }
	run(t, p)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })

	unit.set(http.StatusOK, poisoned)
	var ev Born
	for end := time.Now().Add(3 * time.Second); ev.Kind == "" && time.Now().Before(end); {
		select {
		case b := <-got:
			for _, e := range b {
				if e.Kind == "effect_gained" {
					ev = e
				}
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if ev.Unit != "crew-01" || ev.Attrs["effect"] != "poison" || ev.Attrs["amplifier"] != 0 || ev.Level != "warn" {
		t.Fatalf("effect_gained = %+v", ev)
	}
}
