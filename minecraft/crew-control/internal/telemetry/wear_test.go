package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func pct(n int) *int { return &n }

func wearOnly(slot int, p *int) [len(wearSlots)]*int {
	var w [len(wearSlots)]*int
	w[slot] = p
	return w
}

func attr(e Event, k string) any { return e.Attrs[k] }

func TestWearBucketsAtTheThresholds(t *testing.T) {
	for _, tc := range []struct {
		pct  *int
		want int
	}{{nil, 0}, {pct(100), 0}, {pct(26), 0}, {pct(25), 1}, {pct(11), 1}, {pct(10), 2}, {pct(0), 2}} {
		if got := bucketOf(tc.pct); got != tc.want {
			t.Errorf("bucketOf(%v) = %d, want %d", pctVal(tc.pct), got, tc.want)
		}
	}
}

func pctVal(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestToolWornIsAnnouncedOncePerWorseBucket(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	obs := func(p *int) []Event {
		now = now.Add(time.Second)
		return tr.observe(observation{online: true, wear: wearOnly(heldWear, p)}, now)
	}
	tr.observe(observation{online: true, wear: wearOnly(heldWear, pct(90))}, now)

	if got := obs(pct(40)); len(got) != 0 {
		t.Fatalf("announced above the low bucket: %v", kinds(got))
	}
	got := obs(pct(25))
	if len(got) != 1 || got[0].Kind != "tool_worn" || got[0].Level != "warn" || attr(got[0], "slot") != "held" || attr(got[0], "bucket") != "low" {
		t.Fatalf("low: %+v", got)
	}
	if got := obs(pct(24)); len(got) != 0 {
		t.Fatalf("repeated within the bucket: %v", kinds(got))
	}
	got = obs(pct(9))
	if len(got) != 1 || got[0].Level != "crit" || attr(got[0], "bucket") != "critical" {
		t.Fatalf("critical: %+v", got)
	}
	if got := obs(pct(5)); len(got) != 0 {
		t.Fatalf("repeated within critical: %v", kinds(got))
	}

	// A repaired tool re-arms the slot.
	if got := obs(pct(100)); len(got) != 0 {
		t.Fatalf("repair announced: %v", kinds(got))
	}
	if got := obs(pct(20)); len(got) != 1 || attr(got[0], "bucket") != "low" {
		t.Fatalf("worn again after repair: %+v", got)
	}
}

func heldObs(item string, p *int) observation {
	var o observation
	o.online = true
	o.wear[heldWear], o.wearItem[heldWear] = p, item
	return o
}

func TestSwappingToolsNeitherReannouncesNorRearms(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	obs := func(item string, p *int) []Event {
		now = now.Add(time.Second)
		return tr.observe(heldObs(item, p), now)
	}
	obs("iron_pickaxe", pct(90))
	if got := obs("iron_pickaxe", pct(20)); len(got) != 1 {
		t.Fatalf("worn pickaxe: %v", kinds(got))
	}
	// A fine sword, then an empty hand, then the worn pickaxe again.
	for _, step := range []struct {
		item string
		p    *int
	}{{"iron_sword", pct(80)}, {"", nil}, {"cobblestone", nil}, {"iron_pickaxe", pct(20)}, {"iron_sword", pct(80)}, {"iron_pickaxe", pct(20)}} {
		if got := obs(step.item, step.p); len(got) != 0 {
			t.Fatalf("%s %v announced %v", step.item, pctVal(step.p), kinds(got))
		}
	}
	// A second worn tool is news once, and is not forgotten by swapping.
	if got := obs("iron_axe", pct(20)); len(got) != 1 {
		t.Fatalf("a different worn tool: %v", kinds(got))
	}
	if got := obs("iron_pickaxe", pct(20)); len(got) != 0 {
		t.Fatalf("back to the first: %v", kinds(got))
	}
	// Repairing that same item is what re-arms it.
	obs("iron_pickaxe", pct(100))
	if got := obs("iron_pickaxe", pct(20)); len(got) != 1 {
		t.Fatalf("worn again after repair: %v", kinds(got))
	}
}

func TestToolWornTextCarriesNoPercentage(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	tr.observe(observation{online: true}, now)
	got := tr.observe(observation{online: true, wear: wearOnly(2, pct(7))}, now.Add(time.Second))
	if len(got) != 1 || got[0].Text != "chest worn: critical" || attr(got[0], "slot") != "chest" {
		t.Fatalf("armour: %+v", got)
	}
}

func TestAWornToolAtStartIsABaselineNotAnEvent(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	if got := tr.observe(observation{online: true, wear: wearOnly(heldWear, pct(3))}, now); len(got) != 0 {
		t.Fatalf("baseline announced %v", kinds(got))
	}
	if got := tr.observe(observation{online: true, wear: wearOnly(heldWear, pct(3))}, now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("steady state announced %v", kinds(got))
	}
}

func TestOfflineForgetsTheWearReading(t *testing.T) {
	var tr track
	now := time.Unix(1000, 0)
	worn := wearOnly(heldWear, pct(5))
	tr.observe(observation{online: true, wear: worn}, now)
	if tr.held == nil || *tr.held != 5 {
		t.Fatalf("held = %v", pctVal(tr.held))
	}
	tr.observe(observation{online: false}, now.Add(time.Second))
	if tr.held != nil {
		t.Fatalf("an offline unit keeps its held reading: %d", *tr.held)
	}
	// Back online with the same worn tool: a new session, so it is news.
	if got := tr.observe(observation{online: true, wear: worn}, now.Add(2*time.Second)); len(got) != 2 || got[0].Kind != "online" || got[1].Kind != "tool_worn" {
		t.Fatalf("after reconnect: %v", kinds(got))
	}
}

func TestWearOfPicksHeldItemAndArmourSlots(t *testing.T) {
	inv := []LiveItem{{Slot: 0, Name: "cobblestone", Count: 3}, {Slot: 4, Name: "iron_pickaxe", Count: 1, DurabilityPct: pct(31)}}
	armour := []LiveArmour{{Slot: "helmet", Name: "iron_helmet", DurabilityPct: pct(80)}, {Slot: "boots", Name: "iron_boots"}, {Slot: "held", Name: "x", DurabilityPct: pct(1)}}

	got, names := wearOf(4, true, inv, armour)
	if pctVal(got[heldWear]) != 31 || pctVal(got[1]) != 80 || got[4] != nil {
		t.Errorf("wear = held %v helmet %v boots %v", pctVal(got[0]), pctVal(got[1]), pctVal(got[4]))
	}
	if names[heldWear] != "iron_pickaxe" || names[1] != "iron_helmet" || names[4] != "iron_boots" {
		t.Errorf("names = %v", names)
	}
	if got, names := wearOf(0, true, inv, nil); got[heldWear] != nil || names[heldWear] != "cobblestone" {
		t.Errorf("an item that cannot wear out reads as %v (%s)", pctVal(got[heldWear]), names[heldWear])
	}
	if got, names := wearOf(4, false, inv, nil); got[heldWear] != nil || names[heldWear] != "" {
		t.Errorf("an unknown held slot read as %v (%s)", pctVal(got[heldWear]), names[heldWear])
	}
	// A unit cannot name its way into the held slot through the armour list.
	if got, names := wearOf(9, true, nil, armour); got[heldWear] != nil || names[heldWear] != "" {
		t.Errorf("armour entry named held: %v (%s)", pctVal(got[heldWear]), names[heldWear])
	}
}

const stateWornTool = `{
 "version":"dev","unit":{"id":"crew-01","name":"Ada"},
 "state":{"spawned":true,"dead":false,"dimension":0,"position":[1,64,1],"position_known":true,
  "health":18,"health_known":true,"held_slot":1,"held_known":true,
  "inventory":[{"slot":0,"name":"cobblestone","count":32},{"slot":1,"name":"iron_pickaxe","count":1,"durability_pct":%d}],
  "armour":[{"slot":"helmet","name":"iron_helmet","durability_pct":60},{"slot":"legs","name":"iron_leggings"}]},
 "tasks":{"current":null,"recent":[]}}`

func wornBody(held int) string { return fmt.Sprintf(stateWornTool, held) }

// lastStates records the readings each poll hands to OnPoll.
type lastStates struct {
	mu sync.Mutex
	s  []UnitState
}

func (l *lastStates) set(_ time.Time, s []UnitState) { l.mu.Lock(); l.s = s; l.mu.Unlock() }
func (l *lastStates) get() []UnitState               { l.mu.Lock(); defer l.mu.Unlock(); return l.s }

// run starts p with the hooks already set, as main does: the poll loop reads
// them unsynchronised.
func run(t *testing.T, p *Poller) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestPollerReportsToolWearEventsAndTheHeldReading(t *testing.T) {
	unit := newFakeUnit(t, wornBody(80))
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	got := make(chan []Born, 64)
	p.OnEvents = func(b []Born) { got <- b }
	var ls lastStates
	p.OnPoll = ls.set
	run(t, p)
	waitFor(t, "online", func() bool { return p.Snapshot()[0].Online })

	lv, _ := p.Live("crew-01")
	if len(lv.Armour) != 2 || pctVal(lv.Armour[0].DurabilityPct) != 60 || lv.Armour[1].DurabilityPct != nil {
		t.Errorf("armour = %+v", lv.Armour)
	}
	if lv.Inventory[1].DurabilityPct == nil || *lv.Inventory[1].DurabilityPct != 80 {
		t.Errorf("inventory = %+v", lv.Inventory)
	}
	waitFor(t, "held reading", func() bool {
		s := ls.get()
		return len(s) == 1 && s[0].ToolDurability != nil && *s[0].ToolDurability == 80
	})

	unit.set(200, wornBody(8))
	var worn Born
	for end := time.Now().Add(3 * time.Second); worn.Kind == "" && time.Now().Before(end); {
		select {
		case b := <-got:
			for _, e := range b {
				if e.Kind == "tool_worn" {
					worn = e
				}
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if worn.Kind != "tool_worn" || worn.Unit != "crew-01" || worn.Attrs["slot"] != "held" || worn.Attrs["bucket"] != "critical" {
		t.Fatalf("tool_worn = %+v", worn)
	}
}

func TestAnOlderUnitWithoutDurabilityFieldsHasNoReading(t *testing.T) {
	unit := newFakeUnit(t, stateKnownWithTask)
	p := NewPoller(rosterFor(t, unit.server.URL), &http.Client{}, 20*time.Millisecond)
	var ls lastStates
	p.OnPoll = ls.set
	run(t, p)
	waitFor(t, "a poll", func() bool { return len(ls.get()) == 1 && p.Snapshot()[0].Online })
	if s := ls.get(); s[0].ToolDurability != nil {
		t.Fatalf("states = %+v", s)
	}
}
