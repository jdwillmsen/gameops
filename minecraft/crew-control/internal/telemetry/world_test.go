package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

func inDimension(dim int, known bool) string {
	body := strings.Replace(stateKnownWithTask, `"dimension":0`, fmt.Sprintf(`"dimension":%d`, dim), 1)
	if !known {
		body = strings.Replace(body, `"position_known":true`, `"position_known":false`, 1)
	}
	return body
}

func rosterInWorld(t *testing.T, url, world string) roster.Roster {
	t.Helper()
	r, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Ada","url":%q,"world":%q}]`, url, world))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStatusPositionAndStateCarryTheRosterWorldAndAClosedDimension(t *testing.T) {
	unit := newFakeUnit(t, inDimension(1, true))
	p := startPoller(t, rosterInWorld(t, unit.server.URL, "fwb"), 20*time.Millisecond)
	waitFor(t, "online in the nether", func() bool { s := p.Snapshot()[0]; return s.Online && s.Dim == "nether" })

	if s := p.Snapshot()[0]; s.World != "fwb" {
		t.Errorf("status world = %q", s.World)
	}
	pos := p.Positions()[0]
	if pos.World != "fwb" || pos.DimName != "nether" || pos.Dim != 1 {
		t.Errorf("position = %+v", pos)
	}
	p.mu.Lock()
	st := p.statesLocked()[0]
	p.mu.Unlock()
	if st.World != "fwb" || st.Dim != "nether" {
		t.Errorf("unit state = %+v", st)
	}

	// The public status names the dimension and nothing about where in it.
	raw, _ := json.Marshal(p.Snapshot()[0])
	if !strings.Contains(string(raw), `"world":"fwb"`) || !strings.Contains(string(raw), `"dim":"nether"`) || strings.Contains(string(raw), "12.5") {
		t.Errorf("status json = %s", raw)
	}
}

func TestAnUnlistedDimensionIdReadsAsOtherAndAnUnlocatedUnitHasNone(t *testing.T) {
	unit := newFakeUnit(t, inDimension(41, true))
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "other", func() bool { return p.Snapshot()[0].Dim == "other" })

	unit.set(200, inDimension(2, false))
	waitFor(t, "no dimension while the position is unknown", func() bool { s := p.Snapshot()[0]; return s.Online && s.Dim == "" })
	if got := p.Positions()[0]; got.DimName != "" || got.Known {
		t.Errorf("position = %+v", got)
	}
	if raw, _ := json.Marshal(p.Snapshot()[0]); strings.Contains(string(raw), `"dim"`) {
		t.Errorf("status json names a dimension it does not know: %s", raw)
	}
}

func TestAChangeOfDimensionIsPublishedToTheStream(t *testing.T) {
	unit := newFakeUnit(t, inDimension(0, true))
	p := startPoller(t, rosterFor(t, unit.server.URL), 20*time.Millisecond)
	waitFor(t, "overworld", func() bool { return p.Snapshot()[0].Dim == "overworld" })
	updates, cancel := p.Subscribe()
	defer cancel()
	for len(updates) > 0 {
		<-updates
	}

	unit.set(200, inDimension(2, true))
	select {
	case snap := <-updates:
		if snap[0].Dim != "end" {
			t.Errorf("published dim = %q", snap[0].Dim)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a dimension change was not published")
	}
}

func TestAnOfflineUnitKeepsItsWorldButLosesItsDimension(t *testing.T) {
	unit := newFakeUnit(t, inDimension(1, true))
	p := startPoller(t, rosterInWorld(t, unit.server.URL, "fwb"), 20*time.Millisecond)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Dim == "nether" })

	unit.set(500, "")
	waitFor(t, "offline", func() bool { return !p.Snapshot()[0].Online })
	s := p.Snapshot()[0]
	if s.World != "fwb" || s.Dim != "" {
		t.Errorf("offline status = %+v", s)
	}
	if got := p.Positions()[0]; got.World != "fwb" || got.DimName != "" {
		t.Errorf("offline position = %+v", got)
	}
}

func TestEventsCarryTheWorldAndTheDimensionTheUnitWasIn(t *testing.T) {
	unit := newFakeUnit(t, inDimension(1, true))
	p := startPoller(t, rosterInWorld(t, unit.server.URL, "fwb"), 20*time.Millisecond)
	waitFor(t, "nether", func() bool { return p.Snapshot()[0].Dim == "nether" })

	p.mu.Lock()
	got := p.appendEventLocked(0, Event{At: time.Now(), Kind: "stuck", Level: "warn", Text: "gather has not progressed"})
	p.mu.Unlock()
	if got.World != "fwb" || got.Dim != "nether" {
		t.Errorf("event = %+v", got)
	}
	if fed := p.Events(); fed[len(fed)-1].World != "fwb" {
		t.Errorf("the feed lost the world: %+v", fed[len(fed)-1])
	}

	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"world":"fwb"`) || !strings.Contains(string(raw), `"dim":"nether"`) {
		t.Errorf("event json = %s", raw)
	}

	unit.set(200, inDimension(0, false))
	waitFor(t, "unlocated", func() bool { return p.Positions()[0].DimName == "" })
	p.mu.Lock()
	got = p.appendEventLocked(0, Event{At: time.Now(), Kind: "stuck", Level: "warn", Text: "x"})
	p.mu.Unlock()
	if raw, _ := json.Marshal(got); got.Dim != "" || strings.Contains(string(raw), `"dim"`) {
		t.Errorf("an event with no known position named a dimension: %s", raw)
	}
}

func TestTaskEventsOnTheFeedNameTheirTemplateAndTheDurationStaysOut(t *testing.T) {
	raw, _ := json.Marshal(Event{Kind: "task_done", Level: "ok", Text: "finished haul", Template: "haul", Took: 3 * time.Second, Reason: "x", ReasonCode: "y"})
	for _, want := range []string{`"template":"haul"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("feed event lacks %s: %s", want, raw)
		}
	}
	for _, leak := range []string{"Took", "took", "reason", "Reason", "3000000000"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("feed event carries %q: %s", leak, raw)
		}
	}
	if raw, _ := json.Marshal(Event{Kind: "online"}); strings.Contains(string(raw), "template") {
		t.Errorf("an event with no template names one: %s", raw)
	}
}
