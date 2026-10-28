package trainer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// trainingOn makes a control directory whose switch says "on".
func trainingOn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func baseConfig(t *testing.T, ev Evaluator) Config {
	return Config{
		ControlDir: trainingOn(t),
		Family:     tactics.FamilyZombie, Scenario: "pack", Store: newStore(t), Eval: ev,
		MaxGenerations: 1000, MaxFights: 1_000_000, Budget: time.Hour, MaxFailures: 1000, Seed: 7,
	}
}

// boundsGuard fails the test on any evaluated profile that has a value
// outside its bound, then delegates.
type boundsGuard struct {
	t     *testing.T
	inner Evaluator
	mu    sync.Mutex
	calls int
}

func (g *boundsGuard) Evaluate(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	if p.Version != tactics.Version {
		g.t.Errorf("profile %s version %d", p.Name, p.Version)
	}
	sets := []tactics.Params{p.Default}
	for _, o := range p.Overrides {
		sets = append(sets, o)
	}
	for _, set := range sets {
		for n, b := range tactics.Bounds() {
			v, _ := set.Get(n)
			if math.IsNaN(v) || v < b.Min || v > b.Max {
				g.t.Errorf("profile %s: %s = %v outside %v", p.Name, n, v, b)
			}
		}
	}
	return g.inner.Evaluate(ctx, p, s)
}

func TestConvergesTowardOptimumAndStaysInBounds(t *testing.T) {
	fake := NewFake(tactics.FamilyZombie)
	guard := &boundsGuard{t: t, inner: fake}
	cfg := baseConfig(t, guard)
	cfg.MaxGenerations = 80
	start := fake.Distance(tactics.Defaults())

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	end := fake.Distance(res.Champion.Params())
	t.Logf("distance %.4f -> %.4f, promotions %d, fights %d, stop %s", start, end, res.Promotions, res.Fights, res.StopReason)
	if res.StopReason != StopGenerations {
		t.Errorf("stop = %s, want generations", res.StopReason)
	}
	if end > 0.5*start {
		t.Errorf("champion distance %.4f did not fall below half of %.4f", end, start)
	}
	if fake.Fitness(res.Champion.Params()) <= fake.Fitness(tactics.Defaults())+5 {
		t.Error("champion is not clearly fitter than the defaults")
	}

	hist, err := cfg.Store.History(tactics.FamilyZombie)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != res.Promotions+1 {
		t.Errorf("history has %d records, want baseline + %d promotions", len(hist), res.Promotions)
	}
	for i, rec := range hist {
		if i > 0 && rec.Profile.Parent != hist[i-1].Profile.Name {
			t.Errorf("%s parent = %q, want %q", rec.Profile.Name, rec.Profile.Parent, hist[i-1].Profile.Name)
		}
		if i > 0 && (rec.Generation == 0 || rec.ScorecardsPath == "") {
			t.Errorf("%s lacks lineage: gen %d path %q", rec.Profile.Name, rec.Generation, rec.ScorecardsPath)
		}
		if rec.ScorecardsPath != "" {
			if _, err := os.Stat(filepath.Join(cfg.Store.Dir(), rec.ScorecardsPath)); err != nil {
				t.Errorf("scorecards missing: %v", err)
			}
		}
		for n, b := range tactics.Bounds() {
			if v, _ := rec.Params().Get(n); v < b.Min || v > b.Max {
				t.Errorf("%s stored %s = %v outside %v", rec.Profile.Name, n, v, b)
			}
		}
	}
}

func TestReproducibleWithSeed(t *testing.T) {
	run := func(seed int64) tactics.Params {
		cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
		cfg.MaxGenerations, cfg.Seed, cfg.RunID = 15, seed, "fixed"
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		return res.Champion.Params()
	}
	a, b, c := run(3), run(3), run(4)
	if a != b {
		t.Errorf("same seed gave different champions:\n%+v\n%+v", a, b)
	}
	if a == c {
		t.Error("different seeds gave the same champion")
	}
}

func TestOpponentMirrorCardNeverChangesTheMean(t *testing.T) {
	r := &run{Config: Config{ControlDir: trainingOn(t), MaxFights: 1000, MaxFailures: 1000, Now: time.Now, Logf: func(string, ...any) {}}, deadline: time.Now().Add(time.Hour)}
	own := tactics.Scorecard{Profile: "cand", Outcome: tactics.OutcomeWin, Seconds: 30}
	mirror := tactics.Scorecard{Profile: "rival", Outcome: tactics.OutcomeLoss, DamageTaken: 400}
	aborted := tactics.Scorecard{Profile: "rival", Outcome: tactics.OutcomeAborted}
	meanOf := func(cards ...tactics.Scorecard) float64 {
		r.Eval = evalFunc(func(context.Context, tactics.Profile, Scenario) ([]tactics.Scorecard, error) { return cards, nil })
		kept, mean, ok, err := r.fight(context.Background(), tactics.NewProfile("cand"), Scenario{})
		if err != nil || !ok {
			t.Fatalf("fight: ok=%v err=%v", ok, err)
		}
		for _, c := range kept {
			if c.Profile != "cand" {
				t.Errorf("kept a card for %q", c.Profile)
			}
		}
		return mean
	}
	alone := meanOf(own)
	for name, cards := range map[string][]tactics.Scorecard{
		"mirror after":  {own, mirror},
		"mirror before": {mirror, own},
		"aborted rival": {own, aborted, mirror},
	} {
		if got := meanOf(cards...); got != alone {
			t.Errorf("%s: mean %v, want %v", name, got, alone)
		}
	}
}

func TestCapsAreNeverExceeded(t *testing.T) {
	t.Run("generations", func(t *testing.T) {
		cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
		cfg.MaxGenerations = 3
		res, _ := Run(context.Background(), cfg)
		if res.Generations != 3 || res.StopReason != StopGenerations {
			t.Errorf("generations %d stop %s", res.Generations, res.StopReason)
		}
	})
	t.Run("fights", func(t *testing.T) {
		for _, max := range []int{1, 10, 100, 101, 250} {
			fake := NewFake(tactics.FamilyZombie)
			cfg := baseConfig(t, fake)
			cfg.MaxFights = max
			res, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if fake.Calls() > max || res.Fights > max {
				t.Errorf("cap %d: %d calls, %d counted", max, fake.Calls(), res.Fights)
			}
			if res.StopReason != StopFights {
				t.Errorf("cap %d: stop = %s", max, res.StopReason)
			}
		}
	})
	t.Run("wall clock", func(t *testing.T) {
		clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
		inner := NewFake(tactics.FamilyZombie)
		cfg := baseConfig(t, evalFunc(func(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
			clock.advance(time.Minute)
			return inner.Evaluate(ctx, p, s)
		}))
		cfg.Now, cfg.Budget = clock.now, 30*time.Minute
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != StopBudget || inner.Calls() != 30 {
			t.Errorf("stop %s after %d fights, want budget after 30", res.StopReason, inner.Calls())
		}
	})
	t.Run("real clock interrupts a slow fight", func(t *testing.T) {
		cfg := baseConfig(t, evalFunc(func(ctx context.Context, _ tactics.Profile, _ Scenario) ([]tactics.Scorecard, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}))
		cfg.Budget = 50 * time.Millisecond
		start := time.Now()
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != StopBudget || time.Since(start) > 2*time.Second {
			t.Errorf("stop %s after %v", res.StopReason, time.Since(start))
		}
	})
	t.Run("consecutive failures", func(t *testing.T) {
		calls := 0
		cfg := baseConfig(t, evalFunc(func(context.Context, tactics.Profile, Scenario) ([]tactics.Scorecard, error) {
			calls++
			return nil, errors.New("server down")
		}))
		cfg.MaxFailures = 4
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != StopFailures || calls != 4 {
			t.Errorf("stop %s after %d calls, want failures after 4", res.StopReason, calls)
		}
	})
	t.Run("unmeasured fights count as failures", func(t *testing.T) {
		cfg := baseConfig(t, evalFunc(func(context.Context, tactics.Profile, Scenario) ([]tactics.Scorecard, error) {
			return []tactics.Scorecard{{Outcome: tactics.OutcomeAborted}}, nil
		}))
		cfg.MaxFailures = 3
		res, _ := Run(context.Background(), cfg)
		if res.StopReason != StopFailures || res.Fights != 3 {
			t.Errorf("stop %s after %d fights", res.StopReason, res.Fights)
		}
	})
	t.Run("intermittent failures reset the count", func(t *testing.T) {
		inner := NewFake(tactics.FamilyZombie)
		n := 0
		cfg := baseConfig(t, evalFunc(func(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
			n++
			if n%3 != 0 {
				return inner.Evaluate(ctx, p, s)
			}
			return nil, errors.New("flaky")
		}))
		cfg.MaxFailures, cfg.MaxGenerations = 2, 5
		res, _ := Run(context.Background(), cfg)
		if res.StopReason != StopGenerations {
			t.Errorf("stop = %s, want the run to survive isolated failures", res.StopReason)
		}
	})
}

func TestKillSwitch(t *testing.T) {
	t.Run("flag file present at start", func(t *testing.T) {
		fake := NewFake(tactics.FamilyZombie)
		cfg := baseConfig(t, fake)
		cfg.KillFile = filepath.Join(t.TempDir(), "KILL")
		if err := os.WriteFile(cfg.KillFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		res, _ := Run(context.Background(), cfg)
		if res.StopReason != StopKilled || fake.Calls() != 0 {
			t.Errorf("stop %s after %d fights", res.StopReason, fake.Calls())
		}
	})
	t.Run("flag file appears mid-run", func(t *testing.T) {
		inner := NewFake(tactics.FamilyZombie)
		kill := filepath.Join(t.TempDir(), "KILL")
		n := 0
		cfg := baseConfig(t, evalFunc(func(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
			n++
			if n == 100 {
				_ = os.WriteFile(kill, nil, 0o644)
			}
			return inner.Evaluate(ctx, p, s)
		}))
		cfg.KillFile = kill
		res, _ := Run(context.Background(), cfg)
		if res.StopReason != StopKilled || n != 100 {
			t.Errorf("stop %s after %d fights, want killed at 100", res.StopReason, n)
		}
		if _, ok, _ := cfg.Store.Champion(tactics.FamilyZombie); !ok {
			t.Error("champion lost after a kill")
		}
	})
	t.Run("context cancelled", func(t *testing.T) {
		inner := NewFake(tactics.FamilyZombie)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		n := 0
		cfg := baseConfig(t, evalFunc(func(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
			n++
			if n == 40 {
				cancel()
			}
			return inner.Evaluate(ctx, p, s)
		}))
		res, _ := Run(ctx, cfg)
		if res.StopReason != StopCancelled || n > 41 {
			t.Errorf("stop %s after %d fights", res.StopReason, n)
		}
	})
}

func TestValidateRejectsMissingCaps(t *testing.T) {
	fake := NewFake(tactics.FamilyZombie)
	for name, mod := range map[string]func(*Config){
		"generations": func(c *Config) { c.MaxGenerations = 0 },
		"fights":      func(c *Config) { c.MaxFights = 0 },
		"budget":      func(c *Config) { c.Budget = 0 },
		"failures":    func(c *Config) { c.MaxFailures = 0 },
		"negative":    func(c *Config) { c.MaxFights = -1 },
		"family":      func(c *Config) { c.Family = "../x" },
		"evals":       func(c *Config) { c.Evals = 1 },
	} {
		cfg := baseConfig(t, fake)
		mod(&cfg)
		if _, err := Run(context.Background(), cfg); err == nil {
			t.Errorf("%s: config accepted", name)
		}
	}
	if fake.Calls() != 0 {
		t.Error("a rejected config ran fights")
	}
}

func TestChallengerMustBeatNoise(t *testing.T) {
	// With a flat landscape every challenger ties the champion, so nothing may
	// be promoted however noisy the scores are.
	fake := NewFake(tactics.FamilyZombie)
	fake.Scale = 0
	cfg := baseConfig(t, fake)
	cfg.MaxGenerations, cfg.Alpha = 30, 0.001
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Promotions != 0 {
		t.Errorf("%d promotions on a flat landscape", res.Promotions)
	}
}

func TestResumeKeepsLineage(t *testing.T) {
	cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
	cfg.MaxGenerations = 10
	first, _ := Run(context.Background(), cfg)
	cfg.Seed++
	second, _ := Run(context.Background(), cfg)
	hist, _ := cfg.Store.History(tactics.FamilyZombie)
	if len(hist) != first.Promotions+second.Promotions+1 {
		t.Errorf("history %d, promotions %d+%d", len(hist), first.Promotions, second.Promotions)
	}
	if second.Promotions > 0 {
		firstNew := hist[first.Promotions+1]
		if firstNew.Profile.Parent != hist[first.Promotions].Profile.Name {
			t.Errorf("second run did not continue from the first run's champion")
		}
	}
}

type evalFunc func(context.Context, tactics.Profile, Scenario) ([]tactics.Scorecard, error)

func (f evalFunc) Evaluate(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
	return f(ctx, p, s)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func jsonDecode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }
