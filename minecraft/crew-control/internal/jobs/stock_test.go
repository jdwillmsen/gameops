package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
)

func chestPlace(id string, x int) ledger.Place {
	p := ledger.Pos{X: x, Y: 64, Z: 0}
	return ledger.Place{ID: id, Name: id, Kind: ledger.KindChest, Box: ledger.Box{Min: p, Max: p}}
}

func areaPlace(id string, x int, items ...string) ledger.Place {
	return ledger.Place{ID: id, Name: id, Kind: ledger.KindArea, Items: items,
		Box: ledger.Box{Min: ledger.Pos{X: x, Y: 60, Z: 10}, Max: ledger.Pos{X: x + 9, Y: 70, Z: 19}}}
}

func world(t *testing.T, ps ...ledger.Place) *ledger.Memory {
	t.Helper()
	l := ledger.NewMemory()
	for _, p := range ps {
		if err := l.PutPlace(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func observe(t *testing.T, l *ledger.Memory, p ledger.Place, ago time.Duration, items map[string]int) {
	t.Helper()
	if err := l.Observe(context.Background(), "crew-01", time.Now().Add(-ago), p.Box.Min, items, false); err != nil {
		t.Fatal(err)
	}
}

func free(n int) []ledger.UnitStatus {
	var out []ledger.UnitStatus
	for i := 0; i < n; i++ {
		out = append(out, ledger.UnitStatus{ID: "crew-0" + string(rune('1'+i)), Online: true})
	}
	return out
}

func keys(steps []Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Key)
	}
	return out
}

func stepNamed(t *testing.T, steps []Step, k string) Step {
	t.Helper()
	for _, s := range steps {
		if s.Key == k {
			return s
		}
	}
	t.Fatalf("no step %q in %v", k, keys(steps))
	return Step{}
}

func tmpl(t *testing.T, s Step) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(s.Template, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParallelDemo(t *testing.T) {
	l := world(t, areaPlace("quarry-1", 0), areaPlace("grove-1", 20),
		chestPlace("outbox-a", 1), chestPlace("outbox-b", 2), chestPlace("store", 3))
	steps, err := ParallelDemo(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSteps(steps); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keys(steps), ","); got != "mine,chop,haul,check" {
		t.Fatalf("keys %s", got)
	}
	mine, chop, haul, check := steps[0], steps[1], steps[2], steps[3]
	if mine.Unit != "crew-01" || chop.Unit != "crew-02" || haul.Unit != "crew-03" || check.Unit != "" {
		t.Fatalf("units %q %q %q %q", mine.Unit, chop.Unit, haul.Unit, check.Unit)
	}
	if len(mine.DependsOn) != 0 || len(chop.DependsOn) != 0 {
		t.Fatal("mine and chop run first")
	}
	if strings.Join(haul.DependsOn, ",") != "mine,chop" || strings.Join(check.DependsOn, ",") != "haul" {
		t.Fatalf("deps %v %v", haul.DependsOn, check.DependsOn)
	}
	m := tmpl(t, mine)
	if m["template"] != "gather_and_deposit" || m["item"] != "cobblestone" || m["qty"] != float64(16) {
		t.Fatalf("mine %v", m)
	}
	if m["chest"].(map[string]any)["x"] != float64(1) || m["area"].(map[string]any)["max"].(map[string]any)["x"] != float64(9) {
		t.Fatalf("mine places %v", m)
	}
	h := tmpl(t, haul)
	if h["template"] != "haul" || h["to"].(map[string]any)["x"] != float64(3) || len(h["from"].([]any)) != 2 {
		t.Fatalf("haul %v", h)
	}
	if items := h["items"].(map[string]any); items["cobblestone"] != float64(16) || items["oak_log"] != float64(8) {
		t.Fatalf("haul items %v", items)
	}
	if c := tmpl(t, check); c["template"] != "inspect" || c["chest"].(map[string]any)["x"] != float64(3) {
		t.Fatalf("check %v", c)
	}
}

func TestParallelDemoMissingPlace(t *testing.T) {
	l := world(t, areaPlace("quarry-1", 0))
	_, err := ParallelDemo(context.Background(), l)
	if err == nil || err.Error() != "place grove-1 not found" {
		t.Fatalf("err %v", err)
	}
}

func stockWorld(t *testing.T) (*ledger.Memory, ledger.Place) {
	target := chestPlace("store", 3)
	l := world(t, target, areaPlace("quarry-1", 0, "cobblestone"), areaPlace("grove-1", 20, "oak_log"))
	return l, target
}

func plan(t *testing.T, l ledger.Ledger, want map[string]int, n int, since time.Time) []Step {
	t.Helper()
	steps, err := planStock(context.Background(), l, StockSpec{Place: "store", Want: want}, free(n), since, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSteps(steps); err != nil {
		t.Fatal(err)
	}
	return steps
}

func TestPlanStockNeedMet(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, time.Minute, map[string]int{"oak_log": 40})
	steps := plan(t, l, map[string]int{"oak_log": 32}, 3, time.Now().Add(-time.Hour))
	if got := strings.Join(keys(steps), ","); got != "check" {
		t.Fatalf("keys %s", got)
	}
}

func TestPlanStockChunksEvenly(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, time.Minute, map[string]int{})
	steps := plan(t, l, map[string]int{"oak_log": 32}, 3, time.Now().Add(-time.Hour))
	// An empty chest has no rows, so it reads as missing and gets an inspect.
	if got := strings.Join(keys(steps), ","); got != "inspect-target,gather-oak_log-1,gather-oak_log-2,check" {
		t.Fatalf("keys %s", got)
	}
	for _, k := range []string{"gather-oak_log-1", "gather-oak_log-2"} {
		s := stepNamed(t, steps, k)
		if tmpl(t, s)["qty"] != float64(16) || s.Unit != "" {
			t.Fatalf("%s qty %v unit %q", k, tmpl(t, s)["qty"], s.Unit)
		}
		if strings.Join(s.DependsOn, ",") != "inspect-target" {
			t.Fatalf("%s deps %v", k, s.DependsOn)
		}
	}
	if len(stepNamed(t, steps, "check").DependsOn) != 3 {
		t.Fatal("check waits for everything")
	}
}

func TestPlanStockChunkLimits(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, time.Minute, map[string]int{"cobblestone": 1})
	since := time.Now().Add(-time.Hour)
	// One free unit would carry 129 alone; no chunk may exceed 64.
	steps := plan(t, l, map[string]int{"cobblestone": 130}, 1, since)
	sum, n := 0, 0
	for _, s := range steps {
		if strings.HasPrefix(s.Key, "gather-") {
			q := int(tmpl(t, s)["qty"].(float64))
			if q > 64 {
				t.Fatalf("chunk %d", q)
			}
			sum += q
			n++
		}
	}
	if sum != 129 || n != 3 {
		t.Fatalf("sum %d chunks %d", sum, n)
	}
	// No free unit still plans one chunk, left for the scheduler to place.
	steps = plan(t, l, map[string]int{"cobblestone": 10}, 0, since)
	if g := stepNamed(t, steps, "gather-cobblestone-1"); tmpl(t, g)["qty"] != float64(9) {
		t.Fatal("remaining need")
	}
	// More chunks than free units: units are unpinned.
	steps = plan(t, l, map[string]int{"cobblestone": 256}, 2, since)
	if got := strings.Join(keys(steps), ","); got != "gather-cobblestone-1,gather-cobblestone-2,gather-cobblestone-3,gather-cobblestone-4,check" {
		t.Fatalf("keys %s", got)
	}
	for _, s := range steps {
		if s.Unit != "" {
			t.Fatalf("%s pinned to %s", s.Key, s.Unit)
		}
	}
}

func TestPlanStockSplitsNeedIntoChunks(t *testing.T) {
	cases := []struct {
		need, free, want int
	}{
		{1, 0, 1}, {1, 1, 1}, {1, 3, 1},
		{16, 0, 1}, {16, 1, 1}, {16, 3, 1},
		{17, 0, 1}, {17, 1, 1}, {17, 3, 2},
		{64, 0, 1}, {64, 1, 1}, {64, 3, 3},
		{65, 0, 2}, {65, 1, 2}, {65, 3, 3},
		{256, 0, 4}, {256, 1, 4}, {256, 3, 4},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("need %d free %d", tc.need, tc.free), func(t *testing.T) {
			l, target := stockWorld(t)
			observe(t, l, target, time.Minute, map[string]int{})
			steps := plan(t, l, map[string]int{"cobblestone": tc.need}, tc.free, time.Now().Add(-time.Hour))
			var got []int
			for _, s := range steps {
				if strings.HasPrefix(s.Key, "gather-") {
					got = append(got, int(tmpl(t, s)["qty"].(float64)))
				}
			}
			sum := 0
			for _, q := range got {
				if q < 1 || q > 64 {
					t.Fatalf("chunk %d in %v", q, got)
				}
				sum += q
			}
			if sum != tc.need {
				t.Fatalf("chunks %v sum to %d, want %d", got, sum, tc.need)
			}
			if len(got) != tc.want {
				t.Fatalf("chunks %v: %d of them, want %d", got, len(got), tc.want)
			}
		})
	}
}

func TestPlanStockHaulsFromChest(t *testing.T) {
	l, target := stockWorld(t)
	src := chestPlace("outbox-a", 5)
	if err := l.PutPlace(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	observe(t, l, target, time.Minute, map[string]int{"oak_log": 4})
	observe(t, l, src, time.Minute, map[string]int{"oak_log": 20, "cobblestone": 9})
	steps := plan(t, l, map[string]int{"oak_log": 40}, 3, time.Now().Add(-time.Hour))
	h := stepNamed(t, steps, "haul-oak_log-outbox-a")
	m := tmpl(t, h)
	items := m["items"].(map[string]any)
	if m["template"] != "haul" || len(items) != 1 || items["oak_log"] != float64(20) {
		t.Fatalf("haul %v", m)
	}
	if m["to"].(map[string]any)["x"] != float64(3) || m["from"].([]any)[0].(map[string]any)["x"] != float64(5) {
		t.Fatalf("haul route %v", m)
	}
	// 36 needed, 20 hauled, 16 gathered.
	g := stepNamed(t, steps, "gather-oak_log-1")
	if tmpl(t, g)["qty"] != float64(16) {
		t.Fatalf("gather %v", tmpl(t, g))
	}
	if len(h.DependsOn) != 0 {
		t.Fatalf("fresh target needs no inspect: %v", h.DependsOn)
	}
}

func TestPlanStockStaleTargetInspectsFirst(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, 30*time.Minute, map[string]int{"oak_log": 4})
	steps := plan(t, l, map[string]int{"oak_log": 20}, 3, time.Now().Add(-time.Hour))
	if steps[0].Key != "inspect-target" || tmpl(t, steps[0])["template"] != "inspect" {
		t.Fatalf("first %v", keys(steps))
	}
	if g := stepNamed(t, steps, "gather-oak_log-1"); strings.Join(g.DependsOn, ",") != "inspect-target" {
		t.Fatalf("deps %v", g.DependsOn)
	}
}

func TestPlanStockStaleItemInspectsDespiteFreshPartial(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, 30*time.Minute, map[string]int{"cobblestone": 20})
	if err := l.Observe(context.Background(), "crew-02", time.Now().Add(-time.Minute), target.Box.Min, map[string]int{"oak_log": 3}, true); err != nil {
		t.Fatal(err)
	}
	steps := plan(t, l, map[string]int{"cobblestone": 16}, 3, time.Now().Add(-freshFor))
	if steps[0].Key != "inspect-target" {
		t.Fatalf("a stale wanted item planned without a look: %v", keys(steps))
	}
}

func TestPlanStockStaleSourceIgnored(t *testing.T) {
	l, target := stockWorld(t)
	src := chestPlace("outbox-a", 5)
	_ = l.PutPlace(context.Background(), src)
	observe(t, l, target, time.Minute, map[string]int{"oak_log": 1})
	observe(t, l, src, 30*time.Minute, map[string]int{"oak_log": 20})
	steps := plan(t, l, map[string]int{"oak_log": 10}, 3, time.Now().Add(-time.Hour))
	for _, k := range keys(steps) {
		if strings.HasPrefix(k, "haul-") {
			t.Fatalf("stale chest hauled: %v", keys(steps))
		}
	}
}

func TestPlanStockErrors(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, time.Minute, map[string]int{"oak_log": 1})
	ctx := context.Background()
	for name, c := range map[string]struct {
		spec StockSpec
		want string
	}{
		"unknown item":  {StockSpec{Place: "store", Want: map[string]int{"diamond": 1}}, "no source for diamond"},
		"no area":       {StockSpec{Place: "store", Want: map[string]int{"cobblestone": 5}}, ""},
		"missing place": {StockSpec{Place: "nope", Want: map[string]int{"oak_log": 1}}, "place nope not found"},
		"area target":   {StockSpec{Place: "quarry-1", Want: map[string]int{"oak_log": 1}}, "place quarry-1 is not a chest"},
		"zero":          {StockSpec{Place: "store", Want: map[string]int{"oak_log": 0}}, "want for oak_log must be 1 to 256"},
		"too many":      {StockSpec{Place: "store", Want: map[string]int{"oak_log": 257}}, "want for oak_log must be 1 to 256"},
		"nothing":       {StockSpec{Place: "store", Want: map[string]int{}}, "want nothing"},
	} {
		if name == "no area" {
			l2 := world(t, chestPlace("store", 3), areaPlace("grove-1", 20, "oak_log"))
			_, err := PlanStock(ctx, l2, c.spec, free(2), time.Now())
			if err == nil || err.Error() != "no source for cobblestone" {
				t.Fatalf("%s: %v", name, err)
			}
			continue
		}
		_, err := PlanStock(ctx, l, c.spec, free(2), time.Now())
		if err == nil || err.Error() != c.want {
			t.Fatalf("%s: err %v, want %q", name, err, c.want)
		}
	}
}

func TestCheckStock(t *testing.T) {
	l, target := stockWorld(t)
	ctx := context.Background()
	spec := StockSpec{Place: "store", Want: map[string]int{"oak_log": 20}}
	since := time.Now().Add(-10 * time.Minute)

	// Stock seen before the job began is not evidence the job did anything.
	observe(t, l, target, 20*time.Minute, map[string]int{"oak_log": 50})
	ok, short, top, err := checkStock(ctx, l, spec, since, free(2), time.Now())
	if err != nil || ok || short != "no fresh count for store" || len(top) != 1 {
		t.Fatalf("old: %v %q %v %v", ok, short, top, err)
	}
	if top[0].Key != "r1-check" || tmpl(t, top[0])["template"] != "inspect" {
		t.Fatalf("top-up %v", keys(top))
	}

	l2, target2 := stockWorld(t)
	observe(t, l2, target2, time.Minute, map[string]int{"oak_log": 12})
	ok, short, top, err = checkStock(ctx, l2, spec, since, free(2), time.Now())
	if err != nil || ok || short != "have 12 of 20 oak_log" {
		t.Fatalf("short: %v %q %v", ok, short, err)
	}
	if len(top) == 0 || !strings.HasPrefix(top[0].Key, "r1-") {
		t.Fatalf("top-up %v", keys(top))
	}
	for _, s := range top {
		if !strings.HasPrefix(s.Key, "r1-") {
			t.Fatalf("key %q", s.Key)
		}
	}
	if g := stepNamed(t, top, "r1-gather-oak_log-1"); tmpl(t, g)["qty"] != float64(8) {
		t.Fatalf("remaining %v", tmpl(t, g))
	}
	if err := ValidateSteps(top); err != nil {
		t.Fatal(err)
	}

	// A partial report since the job began does not vouch for an item it
	// left out.
	l4, target4 := stockWorld(t)
	observe(t, l4, target4, 20*time.Minute, map[string]int{"oak_log": 50})
	if err := l4.Observe(ctx, "crew-02", time.Now().Add(-time.Minute), target4.Box.Min, map[string]int{"cobblestone": 3}, true); err != nil {
		t.Fatal(err)
	}
	ok, short, top, err = checkStock(ctx, l4, spec, since, free(2), time.Now())
	if err != nil || ok || short != "no fresh count for store" || len(top) != 1 || top[0].Key != "r1-check" {
		t.Fatalf("partial: %v %q %v %v", ok, short, keys(top), err)
	}

	l3, target3 := stockWorld(t)
	observe(t, l3, target3, time.Minute, map[string]int{"oak_log": 20})
	if ok, _, top, err = checkStock(ctx, l3, spec, since, nil, time.Now()); !ok || err != nil || top != nil {
		t.Fatalf("met: %v %v %v", ok, top, err)
	}
}

func TestStockCheckReadsJob(t *testing.T) {
	l, target := stockWorld(t)
	observe(t, l, target, time.Minute, map[string]int{"oak_log": 20})
	spec, _ := json.Marshal(StockSpec{Place: "store", Want: map[string]int{"oak_log": 20}})
	chk := StockCheck(l, func(context.Context) ([]ledger.UnitStatus, error) { return nil, nil }, time.Now)
	j := Job{ID: uuid.New(), Kind: KindStock, Spec: spec, Created: time.Now().Add(-time.Hour)}
	if ok, _, _, err := chk(context.Background(), j); !ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	j.Spec = json.RawMessage(`{`)
	if _, _, _, err := chk(context.Background(), j); err == nil {
		t.Fatal("bad spec")
	}
}
