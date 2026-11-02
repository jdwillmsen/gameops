package insight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

func stored(unit string, at time.Time, kind, reason string) events.Event {
	p, _ := json.Marshal(map[string]string{"reason_code": reason})
	return events.Event{Unit: unit, At: at, Kind: kind, Severity: events.SevInfo, Payload: p}
}

// An hour shaped like the one that motivated the rules, built synthetically:
// four dead units, eight workers and two haulers, then a server restart that
// slows every worker at once and raises session errors.
func replayHour(t *testing.T) *events.Memory {
	t.Helper()
	m := events.NewMemory()
	var evs []events.Event
	for i := 0; i < 4; i++ {
		u := "dead-" + string(rune('a'+i))
		for k := 0; k < 120; k++ {
			evs = append(evs, stored(u, t0.Add(time.Duration(k)*30*time.Second), "task_failed", "no axe"))
		}
	}
	for i := 0; i < 8; i++ {
		u := "work-" + string(rune('a'+i))
		for k := 0; k < 28*4; k++ {
			evs = append(evs, stored(u, t0.Add(time.Duration(k)*15*time.Second), "task_done", ""))
		}
	}
	for i := 0; i < 2; i++ {
		u := "haul-" + string(rune('a'+i))
		for k := 0; k < 60*30; k++ {
			evs = append(evs, stored(u, t0.Add(time.Duration(k)*2*time.Second), "task_done", ""))
		}
	}
	for k := 0; k < 6; k++ {
		evs = append(evs, stored("work-a", t0.Add(28*time.Minute+time.Duration(k)*5*time.Second), "session_error", ""))
	}
	if err := m.Append(context.Background(), evs); err != nil {
		t.Fatal(err)
	}
	return m
}

func countRules(l []Insight, state string) map[string]int {
	out := map[string]int{}
	for _, in := range l {
		if in.State == state {
			out[in.Rule]++
		}
	}
	return out
}

func TestReplayFlagsDeadUnitsThenCommonMode(t *testing.T) {
	store := replayHour(t)
	tr := NewTracker()
	r := NewRunner(store, tr)

	r.Now = func() time.Time { return t0.Add(5*time.Minute + time.Second) }
	r.Once(context.Background())
	got := countRules(tr.List(), StateFiring)
	if got[RuleZeroSuccess] != 4 || len(got) != 1 {
		t.Fatalf("at minute 5: %v, want only four R1", got)
	}
	for _, in := range tr.List() {
		if in.Unit[:4] != "dead" {
			t.Fatalf("insight on %s, only dead units should fire", in.Unit)
		}
	}

	r.Now = func() time.Time { return t0.Add(29*time.Minute + 30*time.Second) }
	r.Once(context.Background())
	got = countRules(tr.List(), StateFiring)
	if got[RuleCommonMode] != 1 || got[RuleThroughput] != 0 {
		t.Fatalf("at minute 29: %v, want one R4 and no per-unit R2", got)
	}
	for _, in := range tr.List() {
		if len(in.Unit) >= 4 && in.Unit[:4] == "haul" {
			t.Fatalf("hauler %s flagged", in.Unit)
		}
	}
}

type noGate struct{ signed bool }

func (g noGate) Operator(*http.Request) (auth.Operator, bool) {
	return auth.Operator{ID: "o"}, g.signed
}

func TestAPIHidesEvidenceFromAnonymous(t *testing.T) {
	tr := NewTracker()
	tr.Apply(t0, []Finding{{Rule: RuleZeroSuccess, Unit: "u", Severity: SevCrit, Evidence: []int64{7}, Detail: "d"}})
	for _, signed := range []bool{false, true} {
		mux := http.NewServeMux()
		NewAPI(tr).Mount(mux, noGate{signed})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/insights", nil))
		var out struct {
			Insights []struct {
				Evidence []int64 `json:"evidence"`
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Insights) != 1 {
			t.Fatalf("body %s err %v", rec.Body, err)
		}
		if (len(out.Insights[0].Evidence) == 1) != signed {
			t.Fatalf("signed=%v evidence=%v", signed, out.Insights[0].Evidence)
		}
	}
}

func TestLoadKeepsTheNewestWhenThePageCapBites(t *testing.T) {
	m := events.NewMemory()
	now := t0.Add(time.Hour)
	var evs []events.Event
	n := pageSize*maxPages + 500
	for i := 0; i < n; i++ {
		evs = append(evs, stored("u", now.Add(-lookback+time.Duration(i)*time.Millisecond), "task_done", ""))
	}
	_ = m.Append(context.Background(), evs)
	got, err := NewRunner(m, NewTracker()).load(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != pageSize*maxPages {
		t.Fatalf("len = %d, want the cap %d", len(got), pageSize*maxPages)
	}
	if !got[len(got)-1].At.Equal(now.Add(-lookback+time.Duration(n-1)*time.Millisecond)) || got[0].At.After(got[1].At) {
		t.Fatalf("want the newest event last in oldest-first order, got first %v last %v", got[0].At, got[len(got)-1].At)
	}
}

func TestAWorldInsightIsPublicButItsNumbersAreSignedInOnly(t *testing.T) {
	tr := NewTracker()
	tr.Apply(t0, []Finding{{Rule: RuleWorldDiverges, World: "fwb", Severity: SevWarn, Evidence: []int64{7}, Detail: "d", Attrs: map[string]any{"unit_hours": 3.5}}})
	for _, signed := range []bool{false, true} {
		mux := http.NewServeMux()
		NewAPI(tr).Mount(mux, noGate{signed})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/insights", nil))
		var out struct {
			Insights []struct {
				World string         `json:"world"`
				Unit  string         `json:"unit"`
				Fleet bool           `json:"fleet"`
				Attrs map[string]any `json:"attrs"`
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Insights) != 1 {
			t.Fatalf("body %s err %v", rec.Body, err)
		}
		in := out.Insights[0]
		if in.World != "fwb" || in.Unit != "" || in.Fleet {
			t.Errorf("signed=%v insight = %+v", signed, in)
		}
		if (in.Attrs != nil) != signed {
			t.Errorf("signed=%v attrs=%v", signed, in.Attrs)
		}
	}
}

func TestOncePassesTheRostersWorldsToR6(t *testing.T) {
	m := events.NewMemory()
	now := t0.Add(3 * time.Hour)
	worlds := map[string]string{}
	var evs []events.Event
	for w, rate := range map[string]int{"alpha": 40, "beta": 40, "gamma": 40, "delta": 4} {
		u := w + "-1"
		worlds[u] = w
		for i := 0; i < rate; i++ {
			e := stored(u, now.Add(-time.Hour).Add(time.Duration(i+1)*time.Hour/time.Duration(rate+2)), "task_done", "")
			e.Payload = []byte(`{"template":"gather_and_deposit"}`)
			evs = append(evs, e)
		}
	}
	if err := m.Append(context.Background(), evs); err != nil {
		t.Fatal(err)
	}
	tr := NewTracker()
	r := NewRunner(m, tr)
	r.Now = func() time.Time { return now }
	r.Cfg.R6MinUnitHours = 0.9
	r.Worlds = func() map[string]string { return worlds }
	r.Once(context.Background())
	var got []string
	for _, in := range tr.List() {
		if in.Rule == RuleWorldDiverges && in.State == StateFiring {
			got = append(got, in.World)
		}
	}
	if len(got) != 1 || got[0] != "delta" {
		t.Fatalf("R6 insights = %v, want delta", got)
	}
}
