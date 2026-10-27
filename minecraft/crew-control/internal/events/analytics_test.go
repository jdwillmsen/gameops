package events

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOutcomeUsesTheIdleRule(t *testing.T) {
	cases := []struct {
		kind, tpl string
		took      time.Duration
		want      string
	}{
		{"task_done", "haul", 200 * time.Millisecond, OutcomeNoop},
		{"task_done", "haul", 3 * time.Second, OutcomeOK},
		{"task_done", "haul", 0, OutcomeOK},
		{"task_done", "goto", 100 * time.Millisecond, OutcomeOK},
		{"task_failed", "haul", 100 * time.Millisecond, OutcomeFailed},
		{"task_started", "haul", 0, ""},
		{"online", "", 0, ""},
	}
	for _, c := range cases {
		if got := Outcome(c.kind, c.tpl, c.took); got != c.want {
			t.Errorf("Outcome(%s,%s,%v) = %q, want %q", c.kind, c.tpl, c.took, got, c.want)
		}
	}
}

func TestEnrichSetsCodeDurationAndOutcome(t *testing.T) {
	e := Event{Unit: "crew-16", Kind: "task_failed"}
	Enrich(&e, Hint{Template: "mine_shaft", Took: 1500 * time.Millisecond, ReasonCode: "missing_tool"})
	if e.ReasonCode != "missing_tool" || e.Outcome != OutcomeFailed || e.DurationMS == nil || *e.DurationMS != 1500 {
		t.Fatalf("%+v", e)
	}
	old := Event{Kind: "task_failed"}
	Enrich(&old, Hint{})
	if old.ReasonCode != "" || old.DurationMS != nil {
		t.Fatalf("an older unit's event gained fields: %+v", old)
	}
}

func TestRollupCarriesTotalDuration(t *testing.T) {
	c := NewCollapser()
	for i := range 10 {
		at := t0.Add(time.Duration(i) * time.Second)
		e, h := finish("crew-03", at, "task_done", "t"+string(rune('A'+i)), "haul", 200*time.Millisecond)
		c.Push(e, h)
	}
	out := c.Flush()
	if len(out) != 1 || out[0].Outcome != OutcomeNoop || out[0].DurationMS == nil || *out[0].DurationMS != 2000 {
		t.Fatalf("%+v", out)
	}
	var p struct {
		Count   int   `json:"count"`
		TotalMS int64 `json:"total_ms"`
	}
	if err := json.Unmarshal(out[0].Payload, &p); err != nil || p.Count != 10 || p.TotalMS != 2000 {
		t.Fatalf("payload %s: %v", out[0].Payload, err)
	}
}

func stored(t *testing.T, m *Memory, ev ...Event) []Event {
	t.Helper()
	if err := m.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	page, err := m.Query(context.Background(), Query{Asc: true, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}

func TestFailureLinksToTheLatestCauseOnTheSameUnitWithinThirtyMinutes(t *testing.T) {
	m := NewMemory()
	got := stored(t, m,
		Event{Unit: "crew-16", At: t0, Kind: "kit_failed"},
		Event{Unit: "crew-17", At: t0.Add(time.Minute), Kind: "kit_failed"},
		Event{Unit: "crew-16", At: t0.Add(2 * time.Minute), Kind: "session_error"},
		Event{Unit: "crew-16", At: t0.Add(3 * time.Minute), Kind: "task_failed"},
		Event{Unit: "crew-16", At: t0.Add(4 * time.Minute), Kind: "task_done"},
		Event{Unit: "crew-16", At: t0.Add(45 * time.Minute), Kind: "task_failed"},
	)
	byAt := map[time.Duration]Event{}
	for _, e := range got {
		byAt[e.At.Sub(t0)] = e
	}
	linked := byAt[3*time.Minute]
	if linked.CauseRef == nil || *linked.CauseRef != byAt[2*time.Minute].ID {
		t.Errorf("failure linked to %v, want the session_error %d", linked.CauseRef, byAt[2*time.Minute].ID)
	}
	if byAt[4*time.Minute].CauseRef != nil {
		t.Error("a success was linked to a cause")
	}
	if byAt[45*time.Minute].CauseRef != nil {
		t.Error("a failure 45 minutes on was linked")
	}
}

func TestLinkCauseIgnoresFutureAndUnstoredEvents(t *testing.T) {
	f := Event{Unit: "u", At: t0, Kind: "task_failed"}
	if LinkCause(f, []Event{{ID: 1, Unit: "u", At: t0.Add(time.Second), Kind: "kit_failed"}}) != nil {
		t.Error("linked to an event after the failure")
	}
	if LinkCause(f, []Event{{Unit: "u", At: t0.Add(-time.Second), Kind: "kit_failed"}}) != nil {
		t.Error("linked to an event with no id")
	}
}

func TestHistoryFiltersOnAnalyticColumns(t *testing.T) {
	l, mux := api(t)
	d := 200
	seed(t, l,
		Event{Unit: "crew-16", At: t0, Kind: "kit_failed", Severity: SevWarn, SessionID: "abc123"},
		Event{Unit: "crew-16", At: t0.Add(time.Second), Kind: "task_failed", Severity: SevCrit, ReasonCode: "missing_tool", Outcome: OutcomeFailed, SessionID: "abc123", DurationMS: &d},
		Event{Unit: "crew-17", At: t0.Add(2 * time.Second), Kind: "task_done", Severity: SevOK, Outcome: OutcomeOK, SessionID: "def456"},
	)
	count := func(q string) (n int, first view) {
		rec := get(mux, "/v1/events?"+q)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
		var out struct{ Events []view }
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Events) > 0 {
			first = out.Events[0]
		}
		return len(out.Events), first
	}
	if n, v := count("reason_code=missing_tool"); n != 1 || v.CauseRef == nil || v.DurationMS == nil || *v.DurationMS != 200 {
		t.Errorf("reason_code: %d %+v", n, v)
	}
	if n, _ := count("outcome=ok"); n != 1 {
		t.Errorf("outcome: %d", n)
	}
	if n, _ := count("session_id=abc123"); n != 2 {
		t.Errorf("session_id: %d", n)
	}
	kit := get(mux, "/v1/events?kind=kit_failed")
	var kitOut struct{ Events []view }
	_ = json.Unmarshal(kit.Body.Bytes(), &kitOut)
	if n, _ := count("cause_ref=" + itoa(kitOut.Events[0].ID)); n != 1 {
		t.Errorf("cause_ref: %d", n)
	}
	for _, bad := range []string{"outcome=maybe", "reason_code=Bad%20Code", "session_id=zz", "cause_ref=0", "cause_ref=x"} {
		if rec := get(mux, "/v1/events?"+bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestFromUnitValidatesAndBounds(t *testing.T) {
	now := t0
	long := strings.Repeat("é", 200)
	e, err := FromUnit("crew-16", UnitEvent{Kind: "kit_failed", ReasonCode: "kit_no_path", Reason: long, SessionID: "abc123", CrewVersion: "1.36.0"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		ReasonText string `json:"reason_text"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	if len(p.ReasonText) > ReasonTextMax || !strings.HasPrefix(long, p.ReasonText) {
		t.Errorf("reason text %d bytes", len(p.ReasonText))
	}
	if e.Severity != SevWarn || e.ReasonCode != "kit_no_path" || e.At != now || e.Pos != nil {
		t.Errorf("%+v", e)
	}
	if e, _ := FromUnit("u", UnitEvent{Kind: "died", ReasonCode: "from_the_future"}, now); e.ReasonCode != "other" {
		t.Errorf("unknown code stored as %q", e.ReasonCode)
	}
	for name, u := range map[string]UnitEvent{
		"kind":    {Kind: "task_done"},
		"session": {Kind: "died", SessionID: "not hex!"},
	} {
		if _, err := FromUnit("u", u, now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if e, _ := FromUnit("u", UnitEvent{Kind: "died", At: now.Add(-24 * time.Hour)}, now); !e.At.Equal(now) {
		t.Errorf("stale time kept: %v", e.At)
	}
}

func TestFromUnitKeepsAMovementClipReasonAndNothingPositional(t *testing.T) {
	for _, code := range []string{"predicted_inside_block", "corrected_inside_block", "corrected_in_free_space"} {
		e, err := FromUnit("crew-03", UnitEvent{Kind: "movement_clip", ReasonCode: code}, t0)
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if e.ReasonCode != code || e.Severity != SevWarn || e.Pos != nil {
			t.Errorf("%s stored as %+v", code, e)
		}
	}
}
