package explorer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func ev(id int64, unit, kind, sev string, at time.Duration, payload string) events.Event {
	return events.Event{ID: id, Unit: unit, Kind: kind, Severity: sev, At: t0.Add(at), Payload: json.RawMessage(payload)}
}

func params(t *testing.T, q string) Params {
	t.Helper()
	v, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseParams(v)
	if err != nil {
		t.Fatalf("ParseParams(%q): %v", q, err)
	}
	return p
}

func ids(es []events.Event) []int64 {
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func sample() []events.Event {
	task := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	e4 := ev(4, "crew-02", "task_failed", "warn", 4*time.Second, `{"text":"no pickup after 3 tries"}`)
	e4.TaskID = &task
	return []events.Event{
		ev(1, "crew-01", "online", "ok", 1*time.Second, `{}`),
		ev(2, "crew-02", "task_done", "info", 2*time.Second, `{"text":"gathered 8 oak_log"}`),
		ev(3, "crew-01", "died", "crit", 3*time.Second, `{"text":"fell"}`),
		e4,
	}
}

func run(t *testing.T, q string, all []events.Event) Result {
	t.Helper()
	p := params(t, q)
	r, err := Run(all, p.Filter, p.Sort, p.Cursor, p.Limit, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFilters(t *testing.T) {
	cases := []struct {
		q    string
		want []int64
	}{
		{"", []int64{4, 3, 2, 1}},
		{"unit=crew-01", []int64{3, 1}},
		{"unit=crew-01,crew-02&kind=died,task_done", []int64{3, 2}},
		{"severity=warn,crit", []int64{4, 3}},
		{"task=11111111-1111-1111-1111-111111111111", []int64{4}},
		{"q=NO+PICKUP", []int64{4}},
		{"q=crew-02", []int64{4, 2}},
		{"q=died", []int64{3}},
		{"q=11111111", []int64{4}},
		{"since=2026-10-20T12:00:02Z&until=2026-10-20T12:00:03Z", []int64{3, 2}},
		{"range=1m", []int64{}},
		{"unit=crew-09", []int64{}},
	}
	for _, c := range cases {
		if got := ids(run(t, c.q, sample()).Events); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.q, got, c.want)
		}
	}
}

func TestRangeIsRelativeToNow(t *testing.T) {
	p := params(t, "range=2s")
	// Now is t0+4s, so a 2 s range keeps events at or after t0+2s.
	r, _ := Run(sample(), p.Filter, p.Sort, "", 10, t0.Add(4*time.Second))
	if got := ids(r.Events); !slices.Equal(got, []int64{4, 3, 2}) {
		t.Fatalf("got %v", got)
	}
}

func TestSortKeysAndTiebreak(t *testing.T) {
	all := sample()
	cases := map[string][]int64{
		"sort=unit&dir=asc":     {1, 3, 2, 4},
		"sort=unit":             {4, 2, 3, 1},
		"sort=severity&dir=asc": {1, 2, 4, 3},
		"sort=severity":         {3, 4, 2, 1},
		"sort=kind&dir=asc":     {3, 1, 2, 4},
		"dir=asc":               {1, 2, 3, 4},
	}
	for q, want := range cases {
		if got := ids(run(t, q, all).Events); !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", q, got, want)
		}
	}
}

func TestKeysetPagingIsExactOnSharedValues(t *testing.T) {
	var all []events.Event
	for i := 1; i <= 25; i++ {
		// Every event shares one timestamp and unit, so only the id breaks ties.
		all = append(all, ev(int64(i), "crew-01", "online", "ok", 0, `{}`))
	}
	for _, q := range []string{"limit=10", "limit=10&dir=asc", "limit=10&sort=unit", "limit=10&sort=severity&dir=asc"} {
		var seen []int64
		cursor := ""
		for pages := 0; pages < 10; pages++ {
			p := params(t, q)
			r, err := Run(all, p.Filter, p.Sort, cursor, p.Limit, t0)
			if err != nil {
				t.Fatal(err)
			}
			if r.Total != 25 {
				t.Fatalf("total %d", r.Total)
			}
			seen = append(seen, ids(r.Events)...)
			if r.Next == "" {
				break
			}
			cursor = r.Next
		}
		if len(seen) != 25 || len(slices.Compact(slices.Sorted(slices.Values(seen)))) != 25 {
			t.Errorf("%q: pages covered %v", q, seen)
		}
	}
}

func TestCursorSurvivesNewEventsAndRejectsGarbage(t *testing.T) {
	all := sample()
	p := params(t, "limit=2")
	first, _ := Run(all, p.Filter, p.Sort, "", 2, t0)
	// A newer event arriving between pages must not shift the next page.
	all = append(all, ev(5, "crew-01", "online", "ok", 10*time.Second, `{}`))
	next, err := Run(all, p.Filter, p.Sort, first.Next, 2, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(next.Events); !slices.Equal(got, []int64{2, 1}) {
		t.Fatalf("got %v", got)
	}
	if _, err := Run(all, p.Filter, p.Sort, "garbage", 2, t0); err == nil {
		t.Fatal("want error")
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, q := range []string{"severity=loud", "sort=colour", "dir=up", "since=yesterday", "range=forever", "range=9999h", "limit=0", "limit=201", "limit=x"} {
		v, _ := url.ParseQuery(q)
		if _, err := ParseParams(v); err == nil {
			t.Errorf("%q accepted", q)
		}
	}
}

func TestValuesRoundTrip(t *testing.T) {
	q := "unit=crew-01%2Ccrew-02&severity=crit&q=no+pickup&range=1h0m0s&sort=unit&dir=asc"
	p := params(t, q)
	again := params(t, p.Values().Encode())
	if p.Values().Encode() != again.Values().Encode() {
		t.Fatalf("%s != %s", p.Values().Encode(), again.Values().Encode())
	}
	if got := params(t, "").Values().Encode(); got != "" {
		t.Fatalf("defaults should encode empty, got %q", got)
	}
}

func bigSet(n int) []events.Event {
	kinds := []string{"online", "offline", "died", "respawned", "task_started", "task_done", "task_failed", "stuck", "low_health"}
	sevs := []string{"ok", "info", "warn", "crit"}
	all := make([]events.Event, n)
	for i := range all {
		all[i] = ev(int64(i+1), fmt.Sprintf("crew-%02d", i%12), kinds[i%len(kinds)], sevs[i%len(sevs)],
			time.Duration((i*7919)%n)*time.Second,
			fmt.Sprintf(`{"text":"gathered %d oak_log","template":"gather_and_deposit","took_ms":%d}`, i%64, i))
	}
	return all
}

// The target for the whole feature: filter and sort 10,000 events in under
// 200 ms. It runs the heaviest combination (multi-filter, payload search and
// a non-default sort) over the full set, not just the cheap path.
func TestFilterAndSort10kWithinBudget(t *testing.T) {
	all := bigSet(10000)
	p := params(t, "unit=crew-01,crew-02,crew-03,crew-04,crew-05,crew-06&severity=info,warn,crit&q=oak_log&sort=unit&dir=asc")
	start := time.Now()
	r, err := Run(all, p.Filter, p.Sort, "", 100, t0.Add(24*time.Hour))
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("filter+sort of %d events, %d matched: %v (budget %v)", len(all), r.Total, took, budget)
	if r.Total == 0 || took > budget {
		t.Fatalf("total %d, took %v", r.Total, took)
	}
}

func BenchmarkFilterAndSort10k(b *testing.B) {
	all := bigSet(10000)
	v, _ := url.ParseQuery("unit=crew-01,crew-02,crew-03,crew-04,crew-05,crew-06&severity=info,warn,crit&q=oak_log&sort=unit&dir=asc")
	p, _ := ParseParams(v)
	b.ResetTimer()
	for range b.N {
		if _, err := Run(all, p.Filter, p.Sort, "", 100, t0); err != nil {
			b.Fatal(err)
		}
	}
}
