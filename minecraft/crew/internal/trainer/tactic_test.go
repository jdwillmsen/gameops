package trainer

import (
	"context"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

var zombieTactics = []string{"zombie_bait", "zombie_door", "zombie_kite", "zombie_pillar"}

func optionsFor(f tactics.Family) []string {
	if f == tactics.FamilyZombie {
		return zombieTactics
	}
	return nil
}

// tacticGuard fails the test when an evaluated profile names a tactic that is
// not an option, and records which names it saw.
type tacticGuard struct {
	t     *testing.T
	inner Evaluator
	valid map[string]bool
	mu    sync.Mutex
	seen  map[string]int
}

func newTacticGuard(t *testing.T, inner Evaluator, options []string) *tacticGuard {
	g := &tacticGuard{t: t, inner: inner, valid: map[string]bool{}, seen: map[string]int{}}
	for _, o := range options {
		g.valid[o] = true
	}
	return g
}

func (g *tacticGuard) Evaluate(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
	for _, n := range tacticList(p, s.Family) {
		g.mu.Lock()
		g.seen[n]++
		g.mu.Unlock()
		if !g.valid[n] {
			g.t.Errorf("profile %s was evaluated with unknown tactic %q", p.Name, n)
		}
	}
	return g.inner.Evaluate(ctx, p, s)
}

func TestLoopFindsTheRewardedTactic(t *testing.T) {
	fake := NewFake(tactics.FamilyZombie)
	fake.Tactic, fake.TacticReward = "zombie_kite", 25
	guard := newTacticGuard(t, fake, zombieTactics)
	cfg := baseConfig(t, guard)
	cfg.TacticOptions = optionsFor
	cfg.MaxGenerations = 60

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := tacticList(res.Champion.Profile, tactics.FamilyZombie)
	if len(got) == 0 || got[0] != "zombie_kite" {
		t.Errorf("champion tactics = %v, want to lead with zombie_kite (promotions %d)", got, res.Promotions)
	}
	if len(guard.seen) < 2 {
		t.Errorf("only %v were ever tried; the loop did not explore tactics", guard.seen)
	}
}

func TestNoTacticOptionsMeansNoTacticChanges(t *testing.T) {
	fake := NewFake(tactics.FamilyZombie)
	guard := newTacticGuard(t, fake, nil)
	cfg := baseConfig(t, guard)
	cfg.MaxGenerations = 15

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(guard.seen) != 0 {
		t.Errorf("tactics appeared without options: %v", guard.seen)
	}
	if got := tacticList(res.Champion.Profile, tactics.FamilyZombie); len(got) != 0 {
		t.Errorf("champion tactics = %v, want none", got)
	}
}

func TestUnknownBaseTacticIsNeverEmitted(t *testing.T) {
	fake := NewFake(tactics.FamilyZombie)
	guard := newTacticGuard(t, fake, zombieTactics)
	cfg := baseConfig(t, guard)
	cfg.TacticOptions = optionsFor
	cfg.Base = tactics.NewProfile("base")
	cfg.Base.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {"ghost"}, tactics.FamilySkeleton: {"kept"}}
	cfg.MaxGenerations = 10

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := tacticList(res.Champion.Profile, tactics.FamilyZombie); len(got) > 0 && !guard.valid[got[0]] {
		t.Errorf("champion keeps unknown tactic %v", got)
	}
	if got := cfg.Base.Tactics[tactics.FamilyZombie]; len(got) != 1 || got[0] != "ghost" {
		t.Error("the caller's Base map was modified")
	}
	if got := res.Champion.Profile.Tactics[tactics.FamilySkeleton]; len(got) != 1 || got[0] != "kept" {
		t.Error("another family's tactic was dropped")
	}
}

func TestStoredChampionWithRetiredTacticIsCleanedBeforeFighting(t *testing.T) {
	fake := NewFake(tactics.FamilyZombie)
	guard := newTacticGuard(t, fake, zombieTactics)
	cfg := baseConfig(t, guard)
	cfg.TacticOptions = optionsFor
	old := tactics.NewProfile("old")
	old.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {"retired"}}
	old.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyZombie: tactics.Defaults()}
	if _, err := cfg.Store.Append(Record{Profile: old, Family: tactics.FamilyZombie, RunID: "r"}, nil); err != nil {
		t.Fatal(err)
	}
	cfg.MaxGenerations = 5
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestMutateTacticsStaysInOptions(t *testing.T) {
	r := &run{rng: rand.New(rand.NewSource(3))}
	starts := [][]string{nil, {"zombie_bait"}, {"zombie_bait", "zombie_door"}, zombieTactics}
	for _, cur := range starts {
		for i := 0; i < 500; i++ {
			next := r.mutateTactics(cur, zombieTactics)
			seen := map[string]bool{}
			for _, n := range next {
				if seen[n] {
					t.Fatalf("%v -> %v repeats %q", cur, next, n)
				}
				seen[n] = true
				if !slices.Contains(zombieTactics, n) {
					t.Fatalf("%v -> %v has unknown %q", cur, next, n)
				}
			}
			if len(next) > maxTactics {
				t.Fatalf("%v -> %v exceeds %d tactics", cur, next, maxTactics)
			}
		}
	}
}

func TestMutateTacticsCanReorder(t *testing.T) {
	r := &run{rng: rand.New(rand.NewSource(5))}
	cur := []string{"zombie_bait", "zombie_door"}
	for i := 0; i < 200; i++ {
		if next := r.mutateTactics(cur, zombieTactics); slices.Equal(next, []string{"zombie_door", "zombie_bait"}) {
			return
		}
	}
	t.Error("a two-tactic list was never reordered")
}
