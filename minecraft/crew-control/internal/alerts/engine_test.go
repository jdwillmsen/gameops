package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func f32(v float32) *float32 { return &v }

func st(id string, online bool, hunger *float32, seen time.Time) telemetry.Status {
	return telemetry.Status{ID: id, Name: "U-" + id, Online: online, Hunger: hunger, Seen: seen}
}

func states(pairs ...string) []telemetry.UnitState {
	var out []telemetry.UnitState
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, telemetry.UnitState{ID: pairs[i], State: pairs[i+1]})
	}
	return out
}

func find(e *Engine, id string) *Alert {
	for _, a := range e.List() {
		if a.ID == id {
			return &a
		}
	}
	return nil
}

func TestDeathFiresOnceAndResolvesOnRespawn(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	s := []telemetry.Status{st("a", true, f32(20), t0)}
	e.Poll(t0, s, states("a", "idle"))
	if len(e.List()) != 0 {
		t.Fatal("healthy unit alerted")
	}
	e.Poll(t0.Add(time.Second), s, states("a", "dead"))
	a := find(e, "unit_died.a")
	if a == nil || a.State != StateFiring || a.Severity != SevCrit {
		t.Fatalf("want firing crit, got %+v", a)
	}
	since := a.Since
	e.Poll(t0.Add(5*time.Second), s, states("a", "dead"))
	if !find(e, "unit_died.a").Since.Equal(since) {
		t.Fatal("a still-firing alert was re-announced")
	}
	e.Poll(t0.Add(6*time.Second), s, states("a", "idle"))
	if a := find(e, "unit_died.a"); a.State != StateResolved || a.ResolvedAt == nil {
		t.Fatalf("not resolved: %+v", a)
	}
}

func TestStuckHonoursThresholdAndClears(t *testing.T) {
	cfg := Defaults()
	cfg.StuckFor = 30 * time.Second
	e := NewEngine(cfg, t0)
	s := []telemetry.Status{st("a", true, f32(20), t0)}
	e.Poll(t0, s, states("a", "stuck"))
	if find(e, "unit_stuck.a") != nil {
		t.Fatal("fired before the threshold")
	}
	e.Poll(t0.Add(31*time.Second), s, states("a", "stuck"))
	if a := find(e, "unit_stuck.a"); a == nil || a.State != StateFiring {
		t.Fatal("did not fire after the threshold")
	}
	e.Poll(t0.Add(32*time.Second), s, states("a", "working"))
	if find(e, "unit_stuck.a").State != StateResolved {
		t.Fatal("did not resolve when the unit moved on")
	}
}

func TestStuckFiresOnTheTickItIsFlagged(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	e.Poll(t0, []telemetry.Status{st("a", true, f32(20), t0)}, states("a", "stuck"))
	if find(e, "unit_stuck.a") == nil {
		t.Fatal("zero threshold should alert at once")
	}
}

func TestConsecutiveFailuresResetBySuccess(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	s := []telemetry.Status{st("a", true, f32(20), t0)}
	fail := telemetry.Born{Event: telemetry.Event{Unit: "a", Kind: "task_failed"}}
	e.Observe([]telemetry.Born{fail, fail})
	e.Poll(t0, s, states("a", "idle"))
	if find(e, "task_failures.a") != nil {
		t.Fatal("fired below the threshold")
	}
	e.Observe([]telemetry.Born{fail})
	e.Poll(t0.Add(time.Second), s, states("a", "idle"))
	if a := find(e, "task_failures.a"); a == nil || !strings.Contains(a.Text, "3") {
		t.Fatalf("want 3 failures alert, got %+v", a)
	}
	e.Observe([]telemetry.Born{{Event: telemetry.Event{Unit: "a", Kind: "task_done"}}})
	e.Poll(t0.Add(2*time.Second), s, states("a", "idle"))
	if find(e, "task_failures.a").State != StateResolved {
		t.Fatal("success did not resolve")
	}
}

func TestLowFoodThresholdAndOffline(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	e.Poll(t0, []telemetry.Status{st("a", true, f32(7), t0)}, states("a", "idle"))
	if find(e, "low_food.a") != nil {
		t.Fatal("fired above the threshold")
	}
	e.Poll(t0.Add(time.Second), []telemetry.Status{st("a", true, f32(6), t0)}, states("a", "idle"))
	if find(e, "low_food.a") == nil {
		t.Fatal("did not fire at the threshold")
	}
	e.Poll(t0.Add(2*time.Second), []telemetry.Status{st("a", true, f32(18), t0)}, states("a", "idle"))
	if find(e, "low_food.a").State != StateResolved {
		t.Fatal("eating did not resolve")
	}
	e.Poll(t0.Add(3*time.Second), []telemetry.Status{st("a", true, nil, t0)}, states("a", "idle"))
	if find(e, "low_food.a").State != StateResolved {
		t.Fatal("unknown hunger must not fire")
	}
}

func TestSilentAfterThreshold(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	s := []telemetry.Status{st("a", false, nil, t0)}
	e.Poll(t0.Add(time.Minute), s, states("a", "offline"))
	if find(e, "unit_silent.a") != nil {
		t.Fatal("fired too early")
	}
	e.Poll(t0.Add(3*time.Minute), s, states("a", "offline"))
	if find(e, "unit_silent.a") == nil {
		t.Fatal("did not fire")
	}
	e.Poll(t0.Add(4*time.Minute), []telemetry.Status{st("a", true, nil, t0.Add(4*time.Minute))}, states("a", "idle"))
	if find(e, "unit_silent.a").State != StateResolved {
		t.Fatal("reconnect did not resolve")
	}
}

func TestSilentNeverSeenCountsFromStart(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	e.Poll(t0.Add(3*time.Minute), []telemetry.Status{st("a", false, nil, time.Time{})}, states("a", "offline"))
	if find(e, "unit_silent.a") == nil {
		t.Fatal("a never-seen unit should go quiet")
	}
}

func fire(e *Engine, now time.Time) {
	e.Poll(now, []telemetry.Status{st("a", true, f32(20), now)}, states("a", "dead"))
}

func TestAckAndMuteSurviveRefire(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	fire(e, t0)
	if !e.Ack("unit_died.a") || !find(e, "unit_died.a").Acked {
		t.Fatal("ack failed")
	}
	if e.Ack("nope") || e.Mute("nope", time.Minute, t0) {
		t.Fatal("unknown id accepted")
	}
	e.Mute("unit_died.a", time.Hour, t0)
	e.Poll(t0.Add(time.Second), []telemetry.Status{st("a", true, f32(20), t0)}, states("a", "idle"))
	fire(e, t0.Add(2*time.Second))
	a := find(e, "unit_died.a")
	if a.Acked {
		t.Fatal("a new firing must need a new ack")
	}
	if a.MutedUntil == nil || !a.MutedUntil.Equal(t0.Add(time.Hour)) {
		t.Fatalf("mute lost across re-fire: %+v", a.MutedUntil)
	}
	e.Mute("unit_died.a", 0, t0.Add(3*time.Second))
	if find(e, "unit_died.a").MutedUntil != nil {
		t.Fatal("unmute failed")
	}
}

func TestMuteCappedAndExpires(t *testing.T) {
	cfg := Defaults()
	cfg.MuteMax = time.Hour
	e := NewEngine(cfg, t0)
	fire(e, t0)
	e.Mute("unit_died.a", 100*time.Hour, t0)
	if !find(e, "unit_died.a").MutedUntil.Equal(t0.Add(time.Hour)) {
		t.Fatal("mute not capped")
	}
	fire(e, t0.Add(2*time.Hour))
	if find(e, "unit_died.a").MutedUntil != nil {
		t.Fatal("expired mute still shown")
	}
}

func TestResolvedAreDropped(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	fire(e, t0)
	idle := []telemetry.Status{st("a", true, f32(20), t0)}
	e.Poll(t0.Add(time.Second), idle, states("a", "idle"))
	e.Poll(t0.Add(resolvedKeep+2*time.Second), idle, states("a", "idle"))
	if len(e.List()) != 0 {
		t.Fatal("resolved alert kept forever")
	}
}

func TestSubscribeSignalsOnChangeOnly(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	sig, cancel := e.Subscribe()
	defer cancel()
	idle := []telemetry.Status{st("a", true, f32(20), t0)}
	e.Poll(t0, idle, states("a", "idle"))
	select {
	case <-sig:
		t.Fatal("signalled without a change")
	default:
	}
	fire(e, t0.Add(time.Second))
	select {
	case <-sig:
	default:
		t.Fatal("no signal on fire")
	}
}

func TestListOrdersFiringFirst(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	fire(e, t0)
	e.Poll(t0.Add(time.Second), []telemetry.Status{st("a", true, f32(2), t0)}, states("a", "idle"))
	l := e.List()
	if len(l) != 2 || l[0].State != StateFiring || l[1].State != StateResolved {
		t.Fatalf("bad order: %+v", l)
	}
}

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig(`{"stuck_for":"45s","failures":5,"hunger_below":8,"silent_for":"5m"}`)
	if err != nil || c.StuckFor != 45*time.Second || c.Failures != 5 || c.HungerBelow != 8 || c.SilentFor != 5*time.Minute || c.MuteMax != 24*time.Hour {
		t.Fatalf("got %+v %v", c, err)
	}
	if c, err := ParseConfig(" "); err != nil || c != Defaults() {
		t.Fatal("blank must keep defaults")
	}
	for _, bad := range []string{`{"stuck":"1s"}`, `{"failures":0}`, `{"hunger_below":30}`, `{"silent_for":"0s"}`, `{"stuck_for":"x"}`, `[`} {
		if _, err := ParseConfig(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

type gate bool

func (g gate) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g {
			http.Error(w, `{"error":"signed_out"}`, http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func mux(e *Engine, signedIn bool) *http.ServeMux {
	m := http.NewServeMux()
	NewAPI(e).MountOperator(m, gate(signedIn))
	return m
}

func do(m http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestRoutesPublicReadSignedInWrite(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	fire(e, t0)
	out := do(mux(e, false), "GET", "/v1/alerts", "")
	if out.Code != 200 || !strings.Contains(out.Body.String(), "unit_died.a") {
		t.Fatalf("public list: %d %s", out.Code, out.Body)
	}
	if strings.Contains(out.Body.String(), `"x"`) {
		t.Fatal("coordinates leaked")
	}
	for _, p := range []string{"/v1/alerts/unit_died.a/ack", "/v1/alerts/unit_died.a/mute"} {
		if c := do(mux(e, false), "POST", p, "").Code; c != 401 {
			t.Errorf("%s signed out: %d", p, c)
		}
	}
	if find(e, "unit_died.a").Acked {
		t.Fatal("signed-out ack took effect")
	}
	m := mux(e, true)
	if c := do(m, "POST", "/v1/alerts/unit_died.a/ack", "").Code; c != 200 || !find(e, "unit_died.a").Acked {
		t.Fatalf("ack: %d", c)
	}
	if c := do(m, "POST", "/v1/alerts/unit_died.a/mute", `{"for":"30m"}`).Code; c != 200 || find(e, "unit_died.a").MutedUntil == nil {
		t.Fatalf("mute: %d", c)
	}
	if c := do(m, "POST", "/v1/alerts/unit_died.a/mute", `{"for":"soon"}`).Code; c != 400 {
		t.Fatalf("bad duration: %d", c)
	}
	if c := do(m, "POST", "/v1/alerts/nope/ack", "").Code; c != 404 {
		t.Fatalf("unknown: %d", c)
	}
}

func TestStreamSendsSnapshotThenChange(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	srv := httptest.NewServer(mux(e, false))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/alerts/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	next := func() map[string][]Alert {
		buf := make([]byte, 0, 4096)
		b := make([]byte, 1)
		for !strings.HasSuffix(string(buf), "\n\n") {
			if _, err := resp.Body.Read(b); err != nil {
				t.Fatal(err)
			}
			buf = append(buf, b...)
		}
		var v struct {
			Alerts []Alert `json:"alerts"`
		}
		line := strings.SplitN(string(buf), "data: ", 2)[1]
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatal(err)
		}
		return map[string][]Alert{"alerts": v.Alerts}
	}
	if got := next(); len(got["alerts"]) != 0 {
		t.Fatal("expected empty first snapshot")
	}
	fire(e, t0)
	if got := next(); len(got["alerts"]) != 1 {
		t.Fatalf("expected the new alert, got %+v", got)
	}
}

func TestSnoozeRuleMutesCurrentAndFutureAlertsOfThatRuleOnly(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	s := []telemetry.Status{st("a", true, f32(2), t0), st("b", true, f32(20), t0)}
	e.Poll(t0, s, states("a", "idle", "b", "dead"))
	if !e.SnoozeRule(RuleLowFood, time.Hour, t0) {
		t.Fatal("known rule refused")
	}
	if a := find(e, "low_food.a"); a == nil || a.MutedUntil == nil {
		t.Fatalf("existing alert of the rule not muted: %+v", a)
	}
	if b := find(e, "unit_died.b"); b == nil || b.MutedUntil != nil {
		t.Fatalf("another rule was muted: %+v", b)
	}
	// A unit that recovers and then gets hungry again inside the hour stays quiet.
	e.Poll(t0.Add(time.Minute), []telemetry.Status{st("c", true, f32(1), t0)}, states("c", "idle"))
	if c := find(e, "low_food.c"); c == nil || c.MutedUntil == nil {
		t.Fatalf("new alert of a snoozed rule is loud: %+v", c)
	}
	if got := e.Snoozed(t0.Add(time.Minute)); len(got) != 1 || got[RuleLowFood].IsZero() {
		t.Fatalf("snoozed: %v", got)
	}
	e.Poll(t0.Add(2*time.Hour), s, states("a", "idle", "b", "dead"))
	if len(e.Snoozed(t0.Add(2*time.Hour))) != 0 {
		t.Fatal("snooze outlived its period")
	}
}

func TestSnoozeRuleCapsLiftsAndRefusesUnknownRules(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	if e.SnoozeRule("nope", time.Hour, t0) {
		t.Fatal("unknown rule accepted")
	}
	e.SnoozeRule(RuleStuck, 1000*time.Hour, t0)
	if until := e.Snoozed(t0)[RuleStuck]; until.Sub(t0) != Defaults().MuteMax {
		t.Fatalf("not capped: %v", until.Sub(t0))
	}
	e.SnoozeRule(RuleStuck, 0, t0)
	if len(e.Snoozed(t0)) != 0 {
		t.Fatal("not lifted")
	}
}

func TestSnoozeRuleRoute(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	fire(e, t0)
	if c := do(mux(e, false), "POST", "/v1/alerts/rules/unit_died/mute", `{"for":"1h"}`).Code; c != 401 {
		t.Fatalf("signed out: %d", c)
	}
	m := mux(e, true)
	out := do(m, "POST", "/v1/alerts/rules/unit_died/mute", `{"for":"1h"}`)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"snoozed"`) || find(e, "unit_died.a").MutedUntil == nil {
		t.Fatalf("snooze: %d %s", out.Code, out.Body)
	}
	if c := do(m, "POST", "/v1/alerts/rules/nope/mute", `{"for":"1h"}`).Code; c != 404 {
		t.Fatalf("unknown rule: %d", c)
	}
	if c := do(m, "POST", "/v1/alerts/rules/unit_died/mute", `{"for":"x"}`).Code; c != 400 {
		t.Fatalf("bad duration: %d", c)
	}
	if !strings.Contains(do(m, "GET", "/v1/alerts", "").Body.String(), `"snoozed"`) {
		t.Fatal("list does not report the snooze")
	}
}

func toolState(id, state string, pct *int) []telemetry.UnitState {
	return []telemetry.UnitState{{ID: id, State: state, ToolDurability: pct}}
}

func heldTool(id, item string, pct *int) []telemetry.UnitState {
	return []telemetry.UnitState{{ID: id, State: "idle", ToolDurability: pct, ToolItem: item}}
}

func pctOf(n int) *int { return &n }

func TestToolBreakingFiresAtTheThresholdAndClearsOnRepair(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	poll := func(sec int, online bool, pct *int) {
		e.Poll(t0.Add(time.Duration(sec)*time.Second), []telemetry.Status{st("a", online, nil, t0)}, toolState("a", "idle", pct))
	}
	poll(0, true, pctOf(11))
	if find(e, "tool_breaking.a") != nil {
		t.Fatal("fired above the threshold")
	}
	poll(1, true, pctOf(10))
	a := find(e, "tool_breaking.a")
	if a == nil || a.State != StateFiring || a.Severity != SevWarn || a.Rule != RuleToolWorn {
		t.Fatalf("did not fire at the threshold: %+v", a)
	}
	if strings.ContainsAny(a.Text, "0123456789") {
		t.Fatalf("the public alert text carries the exact durability: %q", a.Text)
	}
	poll(2, true, pctOf(100))
	if find(e, "tool_breaking.a").State != StateResolved {
		t.Fatal("a repair did not resolve")
	}
	poll(3, true, pctOf(0))
	poll(5, true, pctOf(0))
	poll(6, false, pctOf(0))
	if find(e, "tool_breaking.a").State != StateResolved {
		t.Fatal("an offline unit must not fire")
	}
}

func TestToolBreakingSurvivesASwapAndOnlyThatToolsRepairResolvesIt(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	n := 0
	poll := func(online bool, item string, pct *int) {
		n++
		e.Poll(t0.Add(time.Duration(n)*time.Second), []telemetry.Status{st("a", online, nil, t0)}, heldTool("a", item, pct))
	}
	firing := func(step string) {
		t.Helper()
		if a := find(e, "tool_breaking.a"); a == nil || a.State != StateFiring {
			t.Fatalf("%s: %+v", step, a)
		}
	}
	poll(true, "iron_pickaxe", pctOf(5))
	firing("worn pickaxe")
	since := find(e, "tool_breaking.a").Since

	poll(true, "iron_sword", pctOf(90))
	firing("swapped to a fine sword")
	poll(true, "", nil)
	firing("empty hand")
	poll(true, "iron_axe", pctOf(3))
	firing("swapped to another worn tool")
	poll(true, "iron_pickaxe", pctOf(5))
	firing("back to the pickaxe")
	if !find(e, "tool_breaking.a").Since.Equal(since) {
		t.Fatal("a swap re-fired the alert")
	}

	poll(true, "iron_pickaxe", pctOf(100))
	if find(e, "tool_breaking.a").State != StateResolved {
		t.Fatal("repairing the same tool did not resolve")
	}
	poll(true, "iron_pickaxe", pctOf(5))
	firing("worn again after repair")
	poll(false, "", nil)
	if find(e, "tool_breaking.a").State != StateResolved {
		t.Fatal("an offline unit must not fire")
	}
}

func TestToolBelowIsConfigurable(t *testing.T) {
	c, err := ParseConfig(`{"tool_below":25}`)
	if err != nil || c.ToolBelow != 25 {
		t.Fatalf("got %+v %v", c, err)
	}
	if Defaults().ToolBelow != 10 {
		t.Fatalf("default = %d", Defaults().ToolBelow)
	}
	for _, bad := range []string{`{"tool_below":-1}`, `{"tool_below":101}`} {
		if _, err := ParseConfig(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestToolBreakingCanBeSnoozedAsAType(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	if !e.SnoozeRule(RuleToolWorn, time.Hour, t0) {
		t.Fatal("tool_breaking is not a known rule")
	}
}

func withEffects(s telemetry.Status, names ...string) telemetry.Status {
	s.Effects = names
	return s
}

func TestHarmfulEffectFiresOnTheClosedHarmfulNamesOnly(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	poll := func(sec int, s telemetry.Status) {
		e.Poll(t0.Add(time.Duration(sec)*time.Second), []telemetry.Status{s}, states("a", "idle"))
	}
	poll(0, withEffects(st("a", true, nil, t0), "speed", "night_vision", "other"))
	if find(e, "harmful_effect.a") != nil {
		t.Fatal("fired on helpful or unknown effects")
	}
	poll(1, withEffects(st("a", true, nil, t0), "speed", "poison", "weakness"))
	a := find(e, "harmful_effect.a")
	if a == nil || a.State != StateFiring || a.Severity != SevWarn || a.Text != "harmful effect: poison, weakness" {
		t.Fatalf("did not fire: %+v", a)
	}
	poll(2, withEffects(st("a", true, nil, t0), "speed", "poison", "weakness", "wither"))
	if find(e, "harmful_effect.a").Text != "harmful effect: poison, weakness" {
		t.Fatal("a firing alert was rewritten")
	}
	poll(3, withEffects(st("a", true, nil, t0), "speed"))
	if find(e, "harmful_effect.a").State != StateResolved {
		t.Fatal("clearing the effect did not resolve")
	}
	poll(4, withEffects(st("a", true, nil, t0), "slowness"))
	poll(5, withEffects(st("a", false, nil, t0), "slowness"))
	if find(e, "harmful_effect.a").State != StateResolved {
		t.Fatal("an offline unit must not fire")
	}
}

func TestEveryDocumentedHarmfulEffectFires(t *testing.T) {
	for _, name := range []string{"poison", "wither", "fatal_poison", "levitation", "darkness", "weakness", "slowness", "mining_fatigue", "nausea", "blindness"} {
		e := NewEngine(Defaults(), t0)
		e.Poll(t0, []telemetry.Status{withEffects(st("a", true, nil, t0), name)}, states("a", "idle"))
		if find(e, "harmful_effect.a") == nil {
			t.Errorf("%s did not fire", name)
		}
	}
}

func TestUnderAttackFollowsTheStatusFlag(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	poll := func(sec int, s telemetry.Status) {
		e.Poll(t0.Add(time.Duration(sec)*time.Second), []telemetry.Status{s}, states("a", "working"))
	}
	attacked := func(online, on bool) telemetry.Status {
		s := st("a", online, nil, t0)
		s.UnderAttack = on
		return s
	}
	poll(0, attacked(true, false))
	if find(e, "under_attack.a") != nil {
		t.Fatal("fired with no attack")
	}
	poll(1, attacked(true, true))
	a := find(e, "under_attack.a")
	if a == nil || a.State != StateFiring || a.Severity != SevWarn || a.Rule != RuleAttack {
		t.Fatalf("did not fire: %+v", a)
	}
	poll(2, attacked(true, false))
	if find(e, "under_attack.a").State != StateResolved {
		t.Fatal("the end of the attack did not resolve")
	}
	poll(3, attacked(true, true))
	poll(4, attacked(false, true))
	if find(e, "under_attack.a").State != StateResolved {
		t.Fatal("an offline unit must not stay under attack")
	}
}
