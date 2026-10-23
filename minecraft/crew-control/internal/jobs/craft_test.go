package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
)

func craftWorld(t *testing.T) (*ledger.Memory, ledger.Place) {
	t.Helper()
	target := chestPlace("store", 3)
	l := world(t, target, areaPlace("grove-1", 20, "oak_log"))
	return l, target
}

func planCraftAt(t *testing.T, l ledger.Ledger, item string, n int) []Step {
	t.Helper()
	steps, err := PlanCraft(ctx, l, CraftSpec{Place: "store", Want: map[string]int{item: n}}, free(2), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSteps(steps); err != nil {
		t.Fatal(err)
	}
	return steps
}

func itemsOf(t *testing.T, s Step) map[string]any {
	t.Helper()
	return tmpl(t, s)["items"].(map[string]any)
}

// chainOf checks the one-unit invariant: every step is pinned to the same
// unit and waits on the step before it.
func chainOf(t *testing.T, steps []Step) {
	t.Helper()
	var unit string
	prev := ""
	for i, s := range steps {
		if i == 0 {
			unit = s.Unit
			if len(s.DependsOn) != 0 {
				t.Fatalf("first step %q waits on %v", s.Key, s.DependsOn)
			}
		} else if s.Unit != unit {
			t.Fatalf("step %q on %q, first on %q", s.Key, s.Unit, unit)
		} else if strings.Join(s.DependsOn, ",") != prev {
			t.Fatalf("step %q waits on %v, want %q", s.Key, s.DependsOn, prev)
		}
		prev = s.Key
	}
}

// TestPlanCraftExpansion reads the recipe expansion directly: a goal that
// needs a table stops the plan before steps, so whole-plan traces only
// exist for the table-free recipes.
func TestPlanCraftExpansion(t *testing.T) {
	p := &craftPlan{crafts: map[string]int{}, raws: map[string]int{}}
	p.expand("wooden_hoe", 1)
	if got := strings.Join(p.order, ","); got != "oak_planks,stick,wooden_hoe" {
		t.Fatalf("order %s", got)
	}
	// The hoe and the sticks it needs together want 4 planks: rounded up
	// per site that is two whole crafts, merged into one, from two logs.
	if c := p.crafts["oak_planks"]; c != 2 {
		t.Fatalf("planks %d crafts", c)
	}
	if c := p.crafts["stick"]; c != 1 {
		t.Fatalf("sticks %d crafts", c)
	}
	if c := p.crafts["wooden_hoe"]; c != 1 {
		t.Fatalf("hoes %d crafts", c)
	}
	if n := p.raws["oak_log"]; n != 2 {
		t.Fatalf("raw oak_log %d", n)
	}
	if len(p.raws) != 1 {
		t.Fatalf("raws %v", p.raws)
	}

	// A merged tree: the sticks' and the hoe's plank demand is one total,
	// rounded up per site (8 and 4 planks make 2 and 1 whole crafts).
	q := &craftPlan{crafts: map[string]int{}, raws: map[string]int{}}
	q.expand("wooden_hoe", 4)
	if c := q.crafts["oak_planks"]; c != 3 {
		t.Fatalf("planks %d crafts", c)
	}
	if n := q.raws["oak_log"]; n != 3 {
		t.Fatalf("raw oak_log %d", n)
	}
}

func TestPlanCraftUnplannable(t *testing.T) {
	withLogs := func(t *testing.T) ledger.Ledger {
		t.Helper()
		l, _ := craftWorld(t)
		src := chestPlace("outbox-a", 5)
		if err := l.PutPlace(ctx, src); err != nil {
			t.Fatal(err)
		}
		observe(t, l, src, time.Minute, map[string]int{"oak_log": 10})
		return l
	}
	cases := map[string]struct {
		mk   func(*testing.T) ledger.Ledger
		item string
		n    int
		want string
	}{
		// Even with every raw in stock, a recipe that needs a table stops
		// the plan: placing one is not plannable yet.
		"table": {withLogs, "chest", 1, "needs a crafting table"},
		"no source": {func(t *testing.T) ledger.Ledger {
			t.Helper()
			return world(t, chestPlace("store", 3))
		}, "stick", 4, "no source for oak_log"},
		"no recipe": {func(t *testing.T) ledger.Ledger {
			t.Helper()
			return world(t, chestPlace("store", 3), areaPlace("quarry-1", 20, "cobblestone"))
		}, "diamond", 1, "no recipe for diamond"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := PlanCraft(ctx, tc.mk(t), CraftSpec{Place: "store", Want: map[string]int{tc.item: tc.n}}, free(2), time.Now().Add(-time.Hour))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if !errors.Is(err, ErrUnplannable) {
				t.Fatalf("err %v is not unplannable", err)
			}
		})
	}
}

func TestPlanCraftFourSticksFromChest(t *testing.T) {
	l, _ := craftWorld(t)
	src := chestPlace("outbox-a", 5)
	if err := l.PutPlace(ctx, src); err != nil {
		t.Fatal(err)
	}
	observe(t, l, src, time.Minute, map[string]int{"oak_log": 8})
	steps := planCraftAt(t, l, "stick", 4)
	if got := strings.Join(keys(steps), ","); got != "withdraw-outbox-a,craft-oak_planks,craft-stick,deposit,check" {
		t.Fatalf("keys %s", got)
	}
	chainOf(t, steps)
	for _, s := range steps {
		if s.Unit != "crew-01" {
			t.Fatalf("%s on %q", s.Key, s.Unit)
		}
	}
	if m := tmpl(t, stepNamed(t, steps, "withdraw-outbox-a")); m["items"].(map[string]any)["oak_log"] != float64(1) {
		t.Fatalf("withdraw %v", m)
	}
	if c := tmpl(t, stepNamed(t, steps, "craft-oak_planks"))["count"].(float64); c != 4 {
		t.Fatalf("planks %v", c)
	}
	if c := tmpl(t, stepNamed(t, steps, "craft-stick"))["count"].(float64); c != 4 {
		t.Fatalf("sticks %v", c)
	}
	if got := itemsOf(t, stepNamed(t, steps, "deposit")); got["stick"] != float64(4) || len(got) != 1 {
		t.Fatalf("deposit %v", got)
	}
}

func TestPlanCraftFourSticksFromGrove(t *testing.T) {
	l, target := craftWorld(t)
	steps := planCraftAt(t, l, "stick", 4)
	if got := strings.Join(keys(steps), ","); got != "gather-oak_log-1,withdraw-store,craft-oak_planks,craft-stick,deposit,check" {
		t.Fatalf("keys %s", got)
	}
	chainOf(t, steps)
	for _, s := range steps {
		if s.Unit != "crew-01" {
			t.Fatalf("%s on %q", s.Key, s.Unit)
		}
	}
	g := tmpl(t, stepNamed(t, steps, "gather-oak_log-1"))
	if g["template"] != "gather_and_deposit" || g["item"] != "oak_log" || g["qty"] != float64(1) {
		t.Fatalf("gather %v", g)
	}
	if area := g["area"].(map[string]any); area["min"].(map[string]any)["x"] != float64(20) {
		t.Fatalf("gather area %v", area)
	}
	// What the gather leaves in the target chest is pulled back out for the
	// crafts: the shortfall's route back into the unit's hands.
	if m := tmpl(t, stepNamed(t, steps, "withdraw-store")); m["chest"].(map[string]any)["x"] != float64(3) || m["items"].(map[string]any)["oak_log"] != float64(1) {
		t.Fatalf("withdraw %v", m)
	}
	if c := tmpl(t, stepNamed(t, steps, "deposit"))["chest"].(map[string]any)["x"]; c != float64(target.Box.Min.X) {
		t.Fatalf("deposit %v", c)
	}
}

func TestPlanCraftRefusesARunBiggerThanAStack(t *testing.T) {
	l, _ := craftWorld(t)
	src := chestPlace("outbox-a", 5)
	if err := l.PutPlace(ctx, src); err != nil {
		t.Fatal(err)
	}
	observe(t, l, src, time.Minute, map[string]int{"oak_log": 64})
	// Sixty-four tables want 256 planks, more than a unit holds as one
	// craft's result.
	_, err := PlanCraft(ctx, l, CraftSpec{Place: "store", Want: map[string]int{"crafting_table": 64}}, nil, time.Now().Add(-time.Hour))
	if !errors.Is(err, ErrUnplannable) {
		t.Fatalf("err = %v, want unplannable", err)
	}
}

func TestCheckCraft(t *testing.T) {
	l, target := craftWorld(t)
	src := chestPlace("outbox-a", 5)
	if err := l.PutPlace(ctx, src); err != nil {
		t.Fatal(err)
	}
	spec := CraftSpec{Place: "store", Want: map[string]int{"stick": 4}}

	// Stock seen before the job began is not evidence the job did anything.
	observe(t, l, target, 20*time.Minute, map[string]int{"stick": 30})
	since := time.Now().Add(-10 * time.Minute)
	ok, short, top, err := checkCraft(ctx, l, spec, since, free(2))
	if err != nil || ok || short != "no fresh count for store" || len(top) != 1 || top[0].Key != "r1-check" {
		t.Fatalf("old: %v %q %v %v", ok, short, top, err)
	}
	if tmpl(t, top[0])["template"] != "inspect" {
		t.Fatalf("top-up %v", tmpl(t, top[0]))
	}

	// A fresh shortfall plans one more round of the same work.
	observe(t, l, target, time.Minute, map[string]int{"stick": 2})
	ok, short, top, err = checkCraft(ctx, l, spec, since, free(2))
	if err != nil || ok || short != "have 2 of 4 stick" {
		t.Fatalf("short: %v %q %v", ok, short, err)
	}
	if len(top) == 0 {
		t.Fatal("no top-up")
	}
	for _, s := range top {
		if !strings.HasPrefix(s.Key, "r1-") {
			t.Fatalf("key %q", s.Key)
		}
		for _, d := range s.DependsOn {
			if !strings.HasPrefix(d, "r1-") {
				t.Fatalf("dep %q", d)
			}
		}
	}
	if err := ValidateSteps(top); err != nil {
		t.Fatal(err)
	}

	observe(t, l, target, time.Minute, map[string]int{"stick": 4})
	if ok, short, top, err = checkCraft(ctx, l, spec, since, free(2)); !ok || short != "" || top != nil || err != nil {
		t.Fatalf("met: %v %q %v %v", ok, short, top, err)
	}
}

func TestCraftCheckReadsJob(t *testing.T) {
	l, target := craftWorld(t)
	observe(t, l, target, time.Minute, map[string]int{"stick": 4})
	spec, _ := json.Marshal(CraftSpec{Place: "store", Want: map[string]int{"stick": 4}})
	chk := CraftCheck(l, func(context.Context) ([]ledger.UnitStatus, error) { return nil, nil })
	j := Job{ID: uuid.New(), Kind: KindCraft, Spec: spec, Created: time.Now().Add(-time.Hour)}
	if ok, _, _, err := chk(ctx, j); !ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	j.Spec = json.RawMessage(`{`)
	if _, _, _, err := chk(ctx, j); err == nil {
		t.Fatal("bad spec")
	}
}
