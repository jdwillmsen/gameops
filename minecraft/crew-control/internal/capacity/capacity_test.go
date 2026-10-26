package capacity

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

var t0 = time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func batch(unit, tpl, kind string, n int, from time.Time, every, dur time.Duration, idle bool) []unitlog.Event {
	out := make([]unitlog.Event, n)
	for i := range out {
		out[i] = unitlog.Event{Unit: unit, Template: tpl, Kind: kind, At: from.Add(time.Duration(i) * every), Duration: dur, Idle: idle}
	}
	return out
}

func TestLittlesLaw(t *testing.T) {
	to := t0.Add(10 * time.Minute)
	// 60 gathers in 10 min at 20 s each: lambda 6/min, W 20 s, L 2.
	// 30 mines at 30 s: lambda 3/min, L 1.5. Two units did the work.
	es := batch("a", "gather", unitlog.KindTaskDone, 60, t0, 10*time.Second, 20*time.Second, false)
	es = append(es, batch("b", "mine", unitlog.KindTaskDone, 30, t0, 20*time.Second, 30*time.Second, false)...)
	r := Compute(t0, to, es)
	if r.WorkingUnits != 2 {
		t.Fatalf("working = %d", r.WorkingUnits)
	}
	g := r.Templates[0]
	if g.Template != "gather" || !near(g.LambdaPerMin, 6) || !near(g.MeanDurationS, 20) || !near(g.L, 2) {
		t.Fatalf("gather = %+v", g)
	}
	if !near(r.BusyUnits, 3.5) || !near(r.Utilization, 1.75) {
		t.Fatalf("busy %v util %v", r.BusyUnits, r.Utilization)
	}
	if r.NonStationary {
		t.Fatal("even arrivals flagged non-stationary")
	}
}

func TestFailedWorkCountsButIsShownApart(t *testing.T) {
	to := t0.Add(10 * time.Minute)
	es := batch("a", "gather", unitlog.KindTaskDone, 10, t0, time.Minute, 10*time.Second, false)
	es = append(es, batch("a", "gather", unitlog.KindTaskFailed, 10, t0.Add(30*time.Second), time.Minute, 30*time.Second, false)...)
	g := Compute(t0, to, es).Templates[0]
	if g.Completed != 10 || g.Failed != 10 || !near(g.LambdaPerMin, 2) || !near(g.FailedTimeS, 300) || !near(g.MeanDurationS, 20) {
		t.Fatalf("gather = %+v", g)
	}
}

func TestIdleRollUpsAreReportedApart(t *testing.T) {
	to := t0.Add(10 * time.Minute)
	es := batch("h", "haul_idle", unitlog.KindTaskDone, 100, t0, time.Second, 5*time.Second, true)
	es = append(es, batch("a", "gather", unitlog.KindTaskDone, 10, t0, time.Minute, 6*time.Second, false)...)
	r := Compute(t0, to, es)
	if len(r.Templates) != 1 || r.WorkingUnits != 1 || len(r.Idle) != 1 || r.Idle[0].Count != 100 {
		t.Fatalf("report = %+v", r)
	}
}

func TestNonStationary(t *testing.T) {
	to := t0.Add(10 * time.Minute)
	cases := []struct {
		name         string
		first, later int
		want         bool
	}{
		{"even", 10, 10, false},
		{"within 2x", 10, 19, false},
		{"over 2x", 10, 25, true},
		{"stopped", 10, 0, true},
		{"started", 0, 10, true},
		{"never ran", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			es := batch("a", "gather", unitlog.KindTaskDone, c.first, t0, 10*time.Second, time.Second, false)
			es = append(es, batch("a", "gather", unitlog.KindTaskDone, c.later, t0.Add(5*time.Minute), 10*time.Second, time.Second, false)...)
			r := Compute(t0, to, es)
			if r.NonStationary != c.want {
				t.Fatalf("flag = %v", r.NonStationary)
			}
		})
	}
}

func TestEmptyWindow(t *testing.T) {
	r := Compute(t0, t0.Add(time.Minute), nil)
	if r.Utilization != 0 || r.WorkingUnits != 0 || r.Templates == nil {
		t.Fatalf("report = %+v", r)
	}
}

func TestAPI(t *testing.T) {
	m := events.NewMemory()
	now := t0.Add(time.Hour)
	pl, _ := json.Marshal(map[string]any{"template": "gather", "duration_ms": 20000})
	var evs []events.Event
	for i := 0; i < 30; i++ {
		evs = append(evs, events.Event{Unit: "a", At: now.Add(-time.Duration(i+1) * time.Minute), Kind: "task_done", Severity: "ok", Payload: pl})
	}
	_ = m.Append(context.Background(), evs)
	a := NewAPI(m)
	a.now = func() time.Time { return now }
	mux := http.NewServeMux()
	a.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/capacity?window=30m", nil))
	var r Report
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(r.Templates) != 1 || !near(r.Templates[0].LambdaPerMin, 1) || !near(r.Utilization, 20.0/60) {
		t.Fatalf("report = %+v", r)
	}
	for _, w := range []string{"nope", "10s", "48h"} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/capacity?window="+w, nil))
		if rec.Code != 400 {
			t.Fatalf("window %s: %d", w, rec.Code)
		}
	}
}
