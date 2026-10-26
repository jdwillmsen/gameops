package explain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

var t0 = time.Date(2026, 10, 23, 6, 21, 0, 0, time.UTC)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func ev(id int64, s int, kind, reason string) unitlog.Event {
	return unitlog.Event{ID: id, Unit: "u", At: at(s), Kind: kind, Reason: reason}
}

func TestExplain(t *testing.T) {
	cases := []struct {
		name        string
		es          []unitlog.Event
		wantClass   string
		wantRule    string
		wantChain   []int64
		unexplained int
	}{
		{"missing tool after a failed kit pickup",
			[]unitlog.Event{ev(1, 0, unitlog.KindKitFailed, ""), ev(2, 30, unitlog.KindTaskFailed, "missing_tool")},
			ClassMissingTool, "missing_tool.kit_failed", []int64{1, 2}, 0},
		{"missing tool after death",
			[]unitlog.Event{ev(1, 0, unitlog.KindDied, ""), ev(2, 30, unitlog.KindTaskFailed, "missing_tool")},
			ClassMissingTool, "missing_tool.died", []int64{1, 2}, 0},
		{"explicit cause reference wins over the nearest event",
			[]unitlog.Event{ev(1, 0, unitlog.KindKitFailed, ""), ev(2, 10, unitlog.KindDied, ""),
				{ID: 3, Unit: "u", At: at(20), Kind: unitlog.KindTaskFailed, Reason: "missing_tool", Cause: 1}},
			ClassMissingTool, "missing_tool.kit_failed", []int64{1, 3}, 0},
		{"missing tool with no predecessor is unexplained",
			[]unitlog.Event{ev(1, 0, unitlog.KindTaskFailed, "missing_tool")},
			"", "", nil, 1},
		{"missing tool whose kit failure is long past is unexplained",
			[]unitlog.Event{ev(1, 0, unitlog.KindKitFailed, ""), ev(2, 3600, unitlog.KindTaskFailed, "missing_tool")},
			"", "", nil, 1},
		{"session lost within a minute of a session error",
			[]unitlog.Event{ev(1, 0, unitlog.KindSession, ""), ev(2, 45, unitlog.KindTaskFailed, "session_lost")},
			ClassSessionLost, "session_lost.session_error", []int64{1, 2}, 0},
		{"session lost with the error too far back",
			[]unitlog.Event{ev(1, 0, unitlog.KindSession, ""), ev(2, 90, unitlog.KindTaskFailed, "session_lost")},
			"", "", nil, 1},
		{"low hunger before a stall",
			[]unitlog.Event{ev(1, 0, unitlog.KindLowHunger, ""), ev(2, 30, unitlog.KindTaskFailed, "other")},
			ClassLowHunger, "vitals.low_hunger", []int64{1, 2}, 0},
		{"low health before a stall",
			[]unitlog.Event{ev(1, 0, unitlog.KindLowHealth, ""), ev(2, 30, unitlog.KindTaskFailed, "other")},
			ClassLowHealth, "vitals.low_health", []int64{1, 2}, 0},
		{"anything else is unexplained",
			[]unitlog.Event{ev(1, 0, unitlog.KindTaskFailed, "other")},
			"", "", nil, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Explain("u", at(-10), at(7200), c.es)
			if res.Unexplained.Count != c.unexplained {
				t.Fatalf("unexplained = %d, want %d", res.Unexplained.Count, c.unexplained)
			}
			if c.wantClass == "" {
				if len(res.Findings) != 0 {
					t.Fatalf("findings = %+v", res.Findings)
				}
				return
			}
			if len(res.Findings) != 1 {
				t.Fatalf("findings = %+v", res.Findings)
			}
			f := res.Findings[0]
			if f.Class != c.wantClass || f.Rule != c.wantRule || !slices.Equal(f.Chain, c.wantChain) {
				t.Fatalf("finding = %+v", f)
			}
		})
	}
}

func TestGeometryTrapNeedsRepeatsFromOneAnchor(t *testing.T) {
	mk := func(anchors ...string) []unitlog.Event {
		var es []unitlog.Event
		for i, a := range anchors {
			e := ev(int64(i+1), i*10, unitlog.KindTaskFailed, "no_path")
			e.Anchor = a
			es = append(es, e)
		}
		return es
	}
	cases := []struct {
		name    string
		anchors []string
		trapped int
	}{
		{"three from one anchor", []string{"a", "a", "a"}, 3},
		{"two is not a pattern", []string{"a", "a"}, 0},
		{"moving unit is not trapped", []string{"a", "b", "c", "d"}, 0},
		{"no anchor means no claim", []string{"", "", ""}, 0},
		{"a run then a move", []string{"a", "a", "a", "b"}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Explain("u", at(-1), at(1000), mk(c.anchors...))
			got := 0
			for _, f := range res.Findings {
				if f.Class == ClassGeometryTrap {
					got += f.Count
					if f.Sentence == "" || f.Rule != "no_path.same_anchor" {
						t.Fatalf("finding = %+v", f)
					}
				}
			}
			if got != c.trapped {
				t.Fatalf("trapped = %d, want %d", got, c.trapped)
			}
			if res.Explained+res.Unexplained.Count != res.Total {
				t.Fatalf("explained %d + unexplained %d != total %d", res.Explained, res.Unexplained.Count, res.Total)
			}
		})
	}
}

func TestOnlyFailuresInTheWindowAreCounted(t *testing.T) {
	es := []unitlog.Event{ev(1, 0, unitlog.KindKitFailed, ""), ev(2, 10, unitlog.KindTaskFailed, "missing_tool"),
		ev(3, 5000, unitlog.KindTaskFailed, "other")}
	res := Explain("u", at(5), at(100), es)
	if res.Total != 1 || res.Explained != 1 {
		t.Fatalf("res = %+v", res)
	}
}

func TestAPI(t *testing.T) {
	m := events.NewMemory()
	now := t0.Add(time.Hour)
	pl := func(m map[string]any) json.RawMessage { b, _ := json.Marshal(m); return b }
	_ = m.Append(context.Background(), []events.Event{
		{Unit: "crew-16", At: now.Add(-10 * time.Minute), Kind: "kit_failed", Severity: "warn", Payload: pl(nil)},
		{Unit: "crew-16", At: now.Add(-9 * time.Minute), Kind: "task_failed", Severity: "crit", Payload: pl(map[string]any{"reason_code": "missing_tool"})},
		{Unit: "crew-17", At: now.Add(-9 * time.Minute), Kind: "task_failed", Severity: "crit", Payload: pl(map[string]any{"reason_code": "missing_tool"})},
	})
	a := NewAPI(m)
	a.now = func() time.Time { return now }
	mux := http.NewServeMux()
	a.Mount(mux)

	get := func(path string) (int, Result) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var r Result
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		return rec.Code, r
	}
	code, r := get("/v1/units/crew-16/explain")
	if code != 200 || r.Total != 1 || len(r.Findings) != 1 || r.Findings[0].Rule != "missing_tool.kit_failed" {
		t.Fatalf("crew-16: %d %+v", code, r)
	}
	if code, r = get("/v1/units/crew-17/explain"); code != 200 || r.Unexplained.Count != 1 {
		t.Fatalf("crew-17: %d %+v", code, r)
	}
	for _, bad := range []string{"?from=nope", "?from=2030-01-01T00:00:00Z&to=2029-01-01T00:00:00Z", "?from=2020-01-01T00:00:00Z"} {
		if code, _ = get("/v1/units/crew-16/explain" + bad); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
}
