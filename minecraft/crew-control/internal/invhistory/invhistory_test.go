package invhistory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func items(kv ...any) []telemetry.LiveItem {
	var out []telemetry.LiveItem
	for i := 0; i < len(kv); i += 2 {
		out = append(out, telemetry.LiveItem{Slot: i / 2, Name: kv[i].(string), Count: kv[i+1].(int)})
	}
	return out
}

func changesOf(t *testing.T, e events.Event) []Change {
	t.Helper()
	var p payload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Changes
}

func TestFirstSightIsABaselineNotAChange(t *testing.T) {
	tr := NewTracker()
	if _, ok := tr.Observe(t0, "crew-01", true, items("stone", 10)); ok {
		t.Fatal("first sight recorded a change")
	}
	if _, ok := tr.Observe(t0.Add(time.Minute), "crew-01", true, items("stone", 10)); ok {
		t.Fatal("an unchanged inventory recorded a change")
	}
}

func TestChangeSumsSlotsAndNamesGainsLossesAndNewItems(t *testing.T) {
	tr := NewTracker()
	tr.Observe(t0, "crew-01", true, items("stone", 10, "stone", 5, "coal", 3))
	ev, ok := tr.Observe(t0.Add(time.Minute), "crew-01", true, items("stone", 20, "iron", 2))
	if !ok {
		t.Fatal("no change recorded")
	}
	if ev.Kind != Kind || ev.Unit != "crew-01" || !ev.At.Equal(t0.Add(time.Minute)) {
		t.Errorf("event = %+v", ev)
	}
	want := []Change{{"coal", 3, 0}, {"iron", 0, 2}, {"stone", 15, 20}}
	got := changesOf(t, ev)
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestChangesWithinTheGapFoldIntoTheNext(t *testing.T) {
	tr := NewTracker()
	tr.Observe(t0, "crew-01", true, items("stone", 1))
	if _, ok := tr.Observe(t0.Add(time.Second), "crew-01", true, items("stone", 2)); !ok {
		t.Fatal("first change should record at once")
	}
	if _, ok := tr.Observe(t0.Add(5*time.Second), "crew-01", true, items("stone", 9)); ok {
		t.Fatal("a change inside the gap was recorded")
	}
	ev, ok := tr.Observe(t0.Add(time.Second+MinGap), "crew-01", true, items("stone", 9))
	if !ok {
		t.Fatal("held change never recorded")
	}
	if got := changesOf(t, ev); len(got) != 1 || got[0] != (Change{"stone", 2, 9}) {
		t.Errorf("changes = %v, want the net 2 -> 9", got)
	}
}

func TestOfflineKeepsTheBaseline(t *testing.T) {
	tr := NewTracker()
	tr.Observe(t0, "crew-01", true, items("stone", 4))
	if _, ok := tr.Observe(t0.Add(time.Minute), "crew-01", false, nil); ok {
		t.Fatal("an offline unit recorded a change")
	}
	ev, ok := tr.Observe(t0.Add(2*time.Minute), "crew-01", true, items("stone", 6))
	if !ok {
		t.Fatal("change after offline not recorded")
	}
	if got := changesOf(t, ev); got[0] != (Change{"stone", 4, 6}) {
		t.Errorf("changes = %v", got)
	}
}

func TestUnitsAreTrackedApart(t *testing.T) {
	tr := NewTracker()
	tr.Observe(t0, "crew-01", true, items("stone", 1))
	tr.Observe(t0, "crew-02", true, items("stone", 1))
	if _, ok := tr.Observe(t0.Add(time.Minute), "crew-02", true, items("stone", 5)); !ok {
		t.Fatal("crew-02 change missing")
	}
	if _, ok := tr.Observe(t0.Add(time.Minute), "crew-01", true, items("stone", 1)); ok {
		t.Fatal("crew-01 inherited crew-02's change")
	}
}

func record(t *testing.T, s events.Store, tr *Tracker, unit string, at time.Time, in []telemetry.LiveItem) {
	t.Helper()
	if ev, ok := tr.Observe(at, unit, true, in); ok {
		if err := s.Append(context.Background(), []events.Event{ev}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadNetsTheWindowAndLeavesOtherEventsOut(t *testing.T) {
	s := events.NewMemory()
	tr := NewTracker()
	record(t, s, tr, "crew-01", t0, items("stone", 10, "coal", 5))
	record(t, s, tr, "crew-01", t0.Add(time.Hour), items("stone", 30, "coal", 5))
	record(t, s, tr, "crew-01", t0.Add(2*time.Hour), items("stone", 25, "coal", 5, "iron", 1))
	record(t, s, tr, "crew-02", t0.Add(time.Hour), items("dirt", 1))
	record(t, s, tr, "crew-02", t0.Add(2*time.Hour), items("dirt", 2))
	_ = s.Append(context.Background(), []events.Event{{Unit: "crew-01", At: t0.Add(time.Hour), Kind: "died", Severity: events.SevWarn, Payload: json.RawMessage(`{}`)}})

	rep, err := Read(context.Background(), s, "crew-01", t0.Add(-time.Hour), t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(rep.Entries))
	}
	if !rep.Entries[0].At.After(rep.Entries[1].At) {
		t.Error("entries are not newest first")
	}
	want := []Net{{"stone", 15}, {"iron", 1}}
	if len(rep.Net) != len(want) || rep.Net[0] != want[0] || rep.Net[1] != want[1] {
		t.Errorf("net = %v, want %v", rep.Net, want)
	}

	narrow, _ := Read(context.Background(), s, "crew-01", t0.Add(90*time.Minute), t0.Add(24*time.Hour))
	if len(narrow.Entries) != 1 || len(narrow.Net) != 2 || narrow.Net[1].Item != "stone" || narrow.Net[1].Delta != -5 {
		t.Errorf("narrow window = %+v", narrow)
	}
}

func TestNetOmitsAnItemThatCameBack(t *testing.T) {
	s := events.NewMemory()
	tr := NewTracker()
	record(t, s, tr, "crew-01", t0, items("stone", 10))
	record(t, s, tr, "crew-01", t0.Add(time.Minute), items("stone", 20))
	record(t, s, tr, "crew-01", t0.Add(3*time.Minute), items("stone", 10))
	rep, _ := Read(context.Background(), s, "crew-01", t0, t0.Add(time.Hour))
	if len(rep.Entries) != 2 || len(rep.Net) != 0 {
		t.Errorf("report = %+v", rep)
	}
}

type gate bool

func (g gate) Operator(*http.Request) (auth.Operator, bool) {
	return auth.Operator{ID: "owner"}, bool(g)
}

func serve(t *testing.T, s events.Store, signed bool, now time.Time, url string) *httptest.ResponseRecorder {
	t.Helper()
	a := NewAPI(s, func(id string) bool { return id == "crew-01" })
	a.now = func() time.Time { return now }
	mux := http.NewServeMux()
	a.Mount(mux, gate(signed))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	return rec
}

func TestAPI(t *testing.T) {
	s := events.NewMemory()
	tr := NewTracker()
	record(t, s, tr, "crew-01", t0.Add(-10*24*time.Hour), items("stone", 1))
	record(t, s, tr, "crew-01", t0.Add(-9*24*time.Hour), items("stone", 4))
	record(t, s, tr, "crew-01", t0.Add(-time.Hour), items("stone", 5))

	if c := serve(t, s, false, t0, "/v1/units/crew-01/inventory/changes").Code; c != 401 {
		t.Errorf("signed out = %d, want 401", c)
	}
	if c := serve(t, s, true, t0, "/v1/units/crew-99/inventory/changes").Code; c != 404 {
		t.Errorf("unknown unit = %d, want 404", c)
	}
	for _, q := range []string{"days=0", "days=31", "days=x"} {
		if c := serve(t, s, true, t0, "/v1/units/crew-01/inventory/changes?"+q).Code; c != 400 {
			t.Errorf("%s = %d, want 400", q, c)
		}
	}

	rec := serve(t, s, true, t0, "/v1/units/crew-01/inventory/changes")
	var rep Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rec.Code != 200 {
		t.Fatalf("status %d: %v", rec.Code, err)
	}
	if len(rep.Entries) != 1 || len(rep.Net) != 1 || rep.Net[0] != (Net{"stone", 1}) {
		t.Errorf("default 7 days = %+v", rep)
	}
	rec = serve(t, s, true, t0, "/v1/units/crew-01/inventory/changes?days=30")
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if len(rep.Entries) != 2 || rep.Net[0] != (Net{"stone", 4}) {
		t.Errorf("30 days = %+v", rep)
	}
}

func TestEmptyHistoryIsEmptyListsNotNull(t *testing.T) {
	rec := serve(t, events.NewMemory(), true, t0, "/v1/units/crew-01/inventory/changes")
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if string(raw["entries"]) != "[]" || string(raw["net"]) != "[]" {
		t.Errorf("body = %s", rec.Body)
	}
}
